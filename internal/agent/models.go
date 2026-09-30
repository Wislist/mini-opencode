package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultModelFetchTimeout bounds the listing when the caller passes a
	// non-positive timeout. It is short on purpose: /models fills a picker, so a
	// hung endpoint must fail fast instead of looking like a frozen UI.
	defaultModelFetchTimeout = 20 * time.Second

	// maxModelErrorBodyBytes caps the slice of a failed response that goes into
	// the error string. An upstream failure is often an HTML error page of
	// unbounded size, while the error text is what lands in logs and in the UI.
	maxModelErrorBodyBytes = 512

	// maxModelResponseBytes caps a successful listing. /models is a JSON array of
	// ids; the cap exists only so a broken or hostile endpoint cannot make the
	// process allocate without bound.
	maxModelResponseBytes = 8 << 20

	// maxModelContextWindow rejects an absurd advertised window. A typo in a
	// provider's metadata must not turn the ctx indicator into a nonsense
	// percentage (or, worse, disable compaction for the session).
	maxModelContextWindow = 100_000_000

	// redactedKeyPlaceholder replaces the api key if an endpoint echoes the
	// credential back inside its error body.
	redactedKeyPlaceholder = "[redacted]"
)

// ModelInfo is one entry of a provider's model list.
type ModelInfo struct {
	ID string
	// ContextWindow is the context length the endpoint reported for this model,
	// or 0 when it reported none. It is what lets a custom model get a real
	// window instead of a placeholder: guessing is what made the ctx indicator
	// read as nearly full from the very first turn.
	ContextWindow int
}

// FetchModels asks an OpenAI-compatible endpoint which models it serves.
// Returns the model ids in the order the endpoint listed them, de-duplicated.
func FetchModels(ctx context.Context, baseURL, apiKey string, timeout time.Duration) ([]string, error) {
	entries, err := fetchModelEntries(ctx, baseURL, apiKey, timeout)
	if err != nil {
		return nil, err
	}
	return collectModelIDs(entries)
}

// FetchModelCatalog is FetchModels plus whatever per-model metadata the endpoint
// chose to advertise: providers disagree on the field name for the context
// length, and none of them are required to send it.
func FetchModelCatalog(ctx context.Context, baseURL, apiKey string, timeout time.Duration) ([]ModelInfo, error) {
	entries, err := fetchModelEntries(ctx, baseURL, apiKey, timeout)
	if err != nil {
		return nil, err
	}
	return collectModelInfos(entries)
}

// fetchModelEntries performs the request and unwraps the listing envelope,
// leaving per-entry parsing to the callers that need different fields.
func fetchModelEntries(ctx context.Context, baseURL, apiKey string, timeout time.Duration) ([]json.RawMessage, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		// Bail out before building a request: an empty base_url would otherwise
		// turn into a relative "GET /models" against the zero-value URL, and a
		// relative request is never what the caller meant.
		return nil, fmt.Errorf("models: base_url is required")
	}
	endpoint := buildModelsEndpoint(base)

	key := strings.TrimSpace(apiKey)
	// The endpoint is redacted too: a base_url with an embedded credential
	// ("https://user:token@host/v1") would otherwise leak through every error.
	endpointLabel := redactKey(endpoint, key)

	if timeout <= 0 {
		timeout = defaultModelFetchTimeout
	}
	// WithTimeout (not http.Client.Timeout) keeps the failure inspectable:
	// errors.Is(err, context.DeadlineExceeded) still holds, and the caller's own
	// ctx stays the parent so its cancellation wins first.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("models: build request for %s: %w", endpointLabel, err)
	}
	req.Header.Set("Accept", "application/json")
	if key != "" {
		// Only send a credential when there is one. Local relays and keyless
		// endpoints commonly reject, or mis-route, a bare "Bearer " header.
		req.Header.Set("Authorization", "Bearer "+key)
	}

	// No client-level timeout: the context above is the single source of truth,
	// so a caller cancellation and our own deadline stay distinguishable.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("models: request to %s: %w", endpointLabel, contextAwareError(ctx, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		status := strings.TrimSpace(fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
		return nil, fmt.Errorf("models: %s returned %s: %s", endpointLabel, status, summarizeErrorBody(resp.Body, key))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("models: read response from %s: %w", endpointLabel, contextAwareError(ctx, err))
	}
	if len(body) > maxModelResponseBytes {
		return nil, fmt.Errorf("models: response from %s exceeds %d bytes", endpointLabel, maxModelResponseBytes)
	}

	entries, err := parseModelEntries(body)
	if err != nil {
		return nil, fmt.Errorf("models: %s: %w", endpointLabel, err)
	}
	return entries, nil
}

// buildModelsEndpoint appends /models unless the caller already pasted a full
// endpoint. Provider docs hand out "https://host/v1/models" verbatim, and
// appending anyway would request /v1/models/models — a 404 that reads like a
// wrong host instead of a wrong URL.
func buildModelsEndpoint(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/models") {
		return base
	}
	return base + "/models"
}

// contextAwareError prefers the context's own error over the transport error.
// The transport only reports "context canceled" through an opaque *url.Error, so
// returning ctx.Err() here is what keeps errors.Is(err, context.Canceled) and
// errors.Is(err, context.DeadlineExceeded) true for the caller.
func contextAwareError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

// parseModelIDs is parseModelEntries plus id collection, kept for callers that
// only want the names.
func parseModelIDs(body []byte) ([]string, error) {
	entries, err := parseModelEntries(body)
	if err != nil {
		return nil, err
	}
	return collectModelIDs(entries)
}

// parseModelEntries accepts the three listings seen in the wild: the OpenAI
// {"data":[{"id":...}]} envelope, a relay that answers with a bare
// {"data":["id",...]}, and a top-level array of either shape.
func parseModelEntries(body []byte) ([]json.RawMessage, error) {
	payload := strings.TrimSpace(string(body))
	if payload == "" {
		return nil, fmt.Errorf("empty response body")
	}

	switch payload[0] {
	case '[':
		var entries []json.RawMessage
		if err := json.Unmarshal([]byte(payload), &entries); err != nil {
			return nil, fmt.Errorf("invalid JSON model list: %w", err)
		}
		return entries, nil

	case '{':
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			return nil, fmt.Errorf("invalid JSON model list: %w", err)
		}
		if len(envelope.Data) == 0 || strings.TrimSpace(string(envelope.Data)) == "null" {
			// No usable "data" member means the endpoint is not speaking the
			// OpenAI /models contract (an auth error envelope lands here). Say
			// so instead of reporting a provider that serves zero models.
			return nil, fmt.Errorf(`response has no "data" field, so it is not an OpenAI-compatible /models list`)
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(envelope.Data, &entries); err != nil {
			return nil, fmt.Errorf(`response "data" field is not an array of models: %w`, err)
		}
		return entries, nil

	default:
		return nil, fmt.Errorf("response is not valid JSON (want an object with a \"data\" array or a top-level array)")
	}
}

// modelListEntry is the standard OpenAI model object. Everything besides the id
// is optional, and every provider names the context length differently, so the
// window is pulled out of the raw JSON rather than off a struct tag.
type modelListEntry struct {
	ID string `json:"id"`
}

// modelWindowFields are the field names seen in the wild for a model's context
// length. Order matters: the first positive value wins, and the OpenAI-style
// name is the most likely to mean what it says.
var modelWindowFields = []string{
	"context_length",
	"context_window",
	"max_context_length",
	"max_context_tokens",
	"max_model_len",
	"max_input_tokens",
	"input_token_limit",
	"context_size",
	"n_ctx",
}

// parseModelWindow extracts the advertised context length from one entry,
// returning 0 when the provider did not report a usable one. Nested objects are
// consulted too: OpenRouter puts it under "top_provider".
func parseModelWindow(raw json.RawMessage) int {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return 0
	}
	for _, name := range modelWindowFields {
		if window := positiveInt(fields[name]); window > 0 {
			return window
		}
	}
	if nested, ok := fields["top_provider"]; ok {
		var nestedFields map[string]json.RawMessage
		if json.Unmarshal(nested, &nestedFields) == nil {
			for _, name := range modelWindowFields {
				if window := positiveInt(nestedFields[name]); window > 0 {
					return window
				}
			}
		}
	}
	return 0
}

// positiveInt reads a token count that may arrive as a JSON number or as a
// numeric string, which some relays use. Anything else - null, an object, a
// non-numeric string - means "not reported" rather than an error: a missing
// window must not cost the user the whole model list.
func positiveInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		if number <= 0 || number > float64(maxModelContextWindow) {
			return 0
		}
		return int(number)
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return 0
		}
		if value, err := strconv.Atoi(trimmed); err == nil && value > 0 && value <= maxModelContextWindow {
			return value
		}
	}
	return 0
}

// collectModelInfos flattens entries into ids plus the window each provider
// advertised, keeping the endpoint's own order and de-duplicating by id.
func collectModelInfos(entries []json.RawMessage) ([]ModelInfo, error) {
	infos := make([]ModelInfo, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i, raw := range entries {
		id, err := entryID(raw, i)
		if err != nil {
			return nil, err
		}
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		infos = append(infos, ModelInfo{ID: id, ContextWindow: parseModelWindow(raw)})
	}
	return infos, nil
}

// entryID reads the id of one listing entry, which may be a model object or a
// bare string.
func entryID(raw json.RawMessage, index int) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(trimmed, "{"):
		var entry modelListEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return "", fmt.Errorf("model entry %d is not a model object: %w", index, err)
		}
		return strings.TrimSpace(entry.ID), nil
	case strings.HasPrefix(trimmed, `"`):
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return "", fmt.Errorf("model entry %d is not a usable string: %w", index, err)
		}
		return strings.TrimSpace(id), nil
	default:
		return "", fmt.Errorf("model entry %d is neither an object nor a string: %s", index, truncateForError(trimmed))
	}
}

// collectModelIDs flattens entries into ids, keeping the endpoint's own order.
func collectModelIDs(entries []json.RawMessage) ([]string, error) {
	// Non-nil even when empty: "no models" and "not a model list" are different
	// answers, and only the error case should look like a failure.
	ids := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i, raw := range entries {
		id, err := entryID(raw, i)
		if err != nil {
			return nil, err
		}
		// A whitespace-only id cannot be sent back as a model name, so treat it
		// like a missing id rather than offering a blank, unusable entry.
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			// Keep the first occurrence: that is the position the provider
			// advertised, and callers use the order to rank defaults.
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

// summarizeErrorBody returns a bounded, credential-free, valid-UTF-8 slice of a
// failed response. Redaction happens before the final cut so a body repeated
// from the key cannot push the summary past the cap.
func summarizeErrorBody(r io.Reader, key string) string {
	body, _ := io.ReadAll(io.LimitReader(r, maxModelErrorBodyBytes))
	excerpt := redactKey(string(body), key)
	if len(excerpt) > maxModelErrorBodyBytes {
		excerpt = excerpt[:maxModelErrorBodyBytes]
	}
	// A byte-wise cut can land mid-rune; invalid UTF-8 in an error string
	// corrupts the log line and the TUI it is rendered into.
	return strings.TrimSpace(strings.ToValidUTF8(excerpt, ""))
}

// redactKey strips the api key from text that may be echoed into an error. Error
// strings are the part of a failure that gets logged and shown, so a relay that
// reflects the Authorization header must not be able to persist the secret.
func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, redactedKeyPlaceholder)
}

// truncateForError bounds a value quoted back into an error message.
func truncateForError(s string) string {
	const limit = 64
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
