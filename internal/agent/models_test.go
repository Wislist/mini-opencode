package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// recordedModelRequest is the slice of an incoming request the tests care about.
// hasAuthorization is tracked separately from the header value because an empty
// apiKey must produce *no header at all*, not a header with an empty value.
type recordedModelRequest struct {
	method           string
	path             string
	accept           string
	authorization    string
	hasAuthorization bool
}

// modelServer records every request it receives so a test can assert on the
// exact endpoint FetchModels talked to. The mutex keeps -race happy: the handler
// runs on the server goroutine while the test reads afterwards.
type modelServer struct {
	*httptest.Server

	mu   sync.Mutex
	reqs []recordedModelRequest
}

func newModelServer(t *testing.T, handler http.HandlerFunc) *modelServer {
	t.Helper()
	ms := &modelServer{}
	ms.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasAuth := r.Header["Authorization"]
		ms.mu.Lock()
		ms.reqs = append(ms.reqs, recordedModelRequest{
			method:           r.Method,
			path:             r.URL.Path,
			accept:           r.Header.Get("Accept"),
			authorization:    r.Header.Get("Authorization"),
			hasAuthorization: hasAuth,
		})
		ms.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(ms.Close)
	return ms
}

func (ms *modelServer) requests() []recordedModelRequest {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return append([]recordedModelRequest(nil), ms.reqs...)
}

func (ms *modelServer) onlyRequest(t *testing.T) recordedModelRequest {
	t.Helper()
	reqs := ms.requests()
	if len(reqs) != 1 {
		t.Fatalf("server saw %d requests, want exactly 1: %#v", len(reqs), reqs)
	}
	return reqs[0]
}

const testModelList = `{"data":[{"id":"gpt-4o","object":"model"},{"id":"gpt-4o-mini"},{"id":"o3"}]}`

func jsonHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func TestFetchModelsHappyPath(t *testing.T) {
	ms := newModelServer(t, jsonHandler(testModelList))

	got, err := FetchModels(context.Background(), ms.URL, "sk-test-key", 5*time.Second)
	if err != nil {
		t.Fatalf("FetchModels() error = %v", err)
	}
	want := []string{"gpt-4o", "gpt-4o-mini", "o3"}
	if len(got) != len(want) {
		t.Fatalf("ids = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids[%d] = %q, want %q (order must match the endpoint listing)", i, got[i], want[i])
		}
	}

	req := ms.onlyRequest(t)
	if req.method != http.MethodGet {
		t.Errorf("method = %q, want GET", req.method)
	}
	if req.path != "/models" {
		t.Errorf("path = %q, want /models", req.path)
	}
	if req.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", req.accept)
	}
	if !req.hasAuthorization {
		t.Error("Authorization header missing, want it set for a non-empty apiKey")
	}
	if req.authorization != "Bearer sk-test-key" {
		t.Errorf("Authorization = %q, want %q", req.authorization, "Bearer sk-test-key")
	}
}

func TestFetchModelsBaseURLJoining(t *testing.T) {
	cases := []struct {
		name     string
		baseURL  string
		wantPath string
	}{
		{name: "no trailing slash", baseURL: "", wantPath: "/models"},
		{name: "trailing slash", baseURL: "/", wantPath: "/models"},
		{name: "versioned no slash", baseURL: "/v1", wantPath: "/v1/models"},
		{name: "versioned trailing slash", baseURL: "/v1/", wantPath: "/v1/models"},
		{name: "already ends with /models", baseURL: "/v1/models", wantPath: "/v1/models"},
		{name: "already ends with /models plus slash", baseURL: "/v1/models/", wantPath: "/v1/models"},
		{name: "already ends with bare /models", baseURL: "/models", wantPath: "/models"},
		{name: "surrounding whitespace", baseURL: "/v1/  ", wantPath: "/v1/models"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := newModelServer(t, jsonHandler(`{"data":[]}`))

			_, err := FetchModels(context.Background(), ms.URL+tc.baseURL, "k", 5*time.Second)
			if err != nil {
				t.Fatalf("FetchModels() error = %v", err)
			}
			req := ms.onlyRequest(t)
			if req.path != tc.wantPath {
				t.Fatalf("path = %q, want %q (baseURL %q)", req.path, tc.wantPath, tc.baseURL)
			}
		})
	}
}

func TestFetchModelsOmitsAuthorizationWithoutAPIKey(t *testing.T) {
	ms := newModelServer(t, jsonHandler(testModelList))

	if _, err := FetchModels(context.Background(), ms.URL, "", 5*time.Second); err != nil {
		t.Fatalf("FetchModels() error = %v", err)
	}
	req := ms.onlyRequest(t)
	if req.hasAuthorization {
		t.Fatalf("Authorization header present (%q) for an empty apiKey; local relays reject or mis-handle it", req.authorization)
	}
	if req.authorization != "" {
		t.Fatalf("Authorization = %q, want empty", req.authorization)
	}
}

func TestFetchModelsResponseShapes(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		want        []string
		wantErr     bool
		errContains string
	}{
		{
			name: "openai data objects",
			body: `{"data":[{"id":"a","object":"model"},{"id":"b"}]}`,
			want: []string{"a", "b"},
		},
		{
			name: "data string array",
			body: `{"data":["x","y"]}`,
			want: []string{"x", "y"},
		},
		{
			name: "top level array of objects",
			body: `[{"id":"p"},{"id":"q"}]`,
			want: []string{"p", "q"},
		},
		{
			name: "top level array of strings",
			body: `["s","t"]`,
			want: []string{"s", "t"},
		},
		{
			name: "empty data is valid but empty",
			body: `{"data":[]}`,
			want: []string{},
		},
		{
			name: "empty top level array is valid but empty",
			body: `[]`,
			want: []string{},
		},
		{
			name: "trims whitespace, skips blank and de-duplicates",
			body: `{"data":[{"id":" gpt-4o "},{"id":"gpt-4o"},{},{"id":""},{"id":"   "},{"id":"gpt-4o-mini"},{"id":"gpt-4o-mini"}]}`,
			want: []string{"gpt-4o", "gpt-4o-mini"},
		},
		{
			name: "entry missing id is skipped",
			body: `{"data":[{"object":"model","owned_by":"x"}]}`,
			want: []string{},
		},
		{
			name:        "no data field",
			body:        `{}`,
			wantErr:     true,
			errContains: "data",
		},
		{
			name:        "error envelope instead of a model list",
			body:        `{"error":"unauthorized"}`,
			wantErr:     true,
			errContains: "data",
		},
		{
			name:        "oauth style error object",
			body:        `{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`,
			wantErr:     true,
			errContains: "data",
		},
		{
			name:        "data is null",
			body:        `{"data":null}`,
			wantErr:     true,
			errContains: "data",
		},
		{
			name:        "data is not an array",
			body:        `{"data":"gpt-4o"}`,
			wantErr:     true,
			errContains: "data",
		},
		{
			name:        "data object instead of array",
			body:        `{"data":{"id":"a"}}`,
			wantErr:     true,
			errContains: "data",
		},
		{
			name:        "invalid json",
			body:        `{"data":[`,
			wantErr:     true,
			errContains: "json",
		},
		{
			name:        "not json at all",
			body:        `<html>login</html>`,
			wantErr:     true,
			errContains: "json",
		},
		{
			name:        "empty body",
			body:        ``,
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "trailing garbage after json",
			body:        `{"data":[{"id":"a"}]} trailing`,
			wantErr:     true,
			errContains: "json",
		},
		{
			name:        "entry is neither object nor string",
			body:        `{"data":[{"id":"a"},42]}`,
			wantErr:     true,
			errContains: "entry",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := newModelServer(t, jsonHandler(tc.body))

			got, err := FetchModels(context.Background(), ms.URL, "k", 5*time.Second)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("FetchModels() error = nil, want error (body %s)", tc.body)
				}
				if tc.errContains != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.errContains)) {
					t.Fatalf("error %q does not mention %q", err.Error(), tc.errContains)
				}
				if got != nil {
					t.Fatalf("ids = %#v, want nil alongside an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchModels() error = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ids = %#v, want %#v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("ids = %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}

func TestFetchModelsEmptyDataReturnsEmptyNonNilSlice(t *testing.T) {
	ms := newModelServer(t, jsonHandler(`{"data":[]}`))

	got, err := FetchModels(context.Background(), ms.URL, "k", 5*time.Second)
	if err != nil {
		t.Fatalf("FetchModels() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(ids) = %d, want 0", len(got))
	}
}

func TestFetchModelsNon2xxStatusErrors(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus string
	}{
		{
			name:       "unauthorized",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`,
			wantStatus: "401",
		},
		{
			name:       "server error",
			status:     http.StatusInternalServerError,
			body:       `upstream exploded`,
			wantStatus: "500",
		},
		{
			name:       "not found",
			status:     http.StatusNotFound,
			body:       `404 page not found`,
			wantStatus: "404",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := newModelServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			const apiKey = "sk-status-secret"
			got, err := FetchModels(context.Background(), ms.URL, apiKey, 5*time.Second)
			if err == nil {
				t.Fatalf("FetchModels() error = nil, want error for status %d", tc.status)
			}
			if got != nil {
				t.Fatalf("ids = %#v, want nil", got)
			}
			if !strings.Contains(err.Error(), tc.wantStatus) {
				t.Fatalf("error %q does not contain status %s", err.Error(), tc.wantStatus)
			}
			if tc.body != "" && !strings.Contains(err.Error(), strings.TrimSpace(tc.body)) {
				t.Fatalf("error %q does not include the response body summary %q", err.Error(), tc.body)
			}
			if strings.Contains(err.Error(), apiKey) {
				t.Fatalf("error leaks the api key: %q", err.Error())
			}
		})
	}
}

func TestFetchModelsErrorNeverLeaksAPIKey(t *testing.T) {
	const apiKey = "sk-super-secret-value"

	// A misbehaving relay that echoes the credential back in its error body is
	// exactly how a key ends up in a log file, so the excerpt must be scrubbed.
	ms := newModelServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key: Bearer ` + apiKey + `"}`))
	})

	_, err := FetchModels(context.Background(), ms.URL, apiKey, 5*time.Second)
	if err == nil {
		t.Fatal("FetchModels() error = nil, want error")
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Fatalf("error leaks the api key: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error %q lost the status code while redacting", err.Error())
	}
}

func TestFetchModelsTruncatesErrorBody(t *testing.T) {
	ms := newModelServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("A", 4096)))
	})

	_, err := FetchModels(context.Background(), ms.URL, "k", 5*time.Second)
	if err == nil {
		t.Fatal("FetchModels() error = nil, want error")
	}
	if len(err.Error()) > 1024 {
		t.Fatalf("error length = %d, want the body summary truncated to <=512 bytes", len(err.Error()))
	}
	if strings.Contains(err.Error(), strings.Repeat("A", 1024)) {
		t.Fatal("error contains an untruncated response body")
	}
	if !strings.Contains(err.Error(), "AAAA") {
		t.Fatalf("error %q dropped the body summary entirely", err.Error())
	}
}

func TestFetchModelsTruncatedErrorBodyStaysValidUTF8(t *testing.T) {
	ms := newModelServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		// 400 CJK runes = 1200 bytes, so a naive 512-byte cut lands mid-rune.
		_, _ = w.Write([]byte(strings.Repeat("模", 400)))
	})

	_, err := FetchModels(context.Background(), ms.URL, "k", 5*time.Second)
	if err == nil {
		t.Fatal("FetchModels() error = nil, want error")
	}
	if !utf8.ValidString(err.Error()) {
		t.Fatalf("error is not valid UTF-8 (byte cut landed mid-rune): %q", err.Error())
	}
}

func TestFetchModelsTimeout(t *testing.T) {
	release := make(chan struct{})
	ms := newModelServer(t, func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		_, _ = w.Write([]byte(testModelList))
	})
	defer close(release)

	start := time.Now()
	_, err := FetchModels(context.Background(), ms.URL, "k", 10*time.Millisecond)
	if err == nil {
		t.Fatal("FetchModels() error = nil, want a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("errors.Is(err, context.DeadlineExceeded) = false for %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("FetchModels() took %v, want it bounded by the 10ms timeout", elapsed)
	}
}

func TestFetchModelsDefaultTimeoutWhenTimeoutNonPositive(t *testing.T) {
	if defaultModelFetchTimeout != 20*time.Second {
		t.Fatalf("defaultModelFetchTimeout = %v, want 20s", defaultModelFetchTimeout)
	}

	for _, timeout := range []time.Duration{0, -1 * time.Second} {
		ms := newModelServer(t, jsonHandler(testModelList))

		start := time.Now()
		got, err := FetchModels(context.Background(), ms.URL, "k", timeout)
		if err != nil {
			t.Fatalf("FetchModels(timeout=%v) error = %v", timeout, err)
		}
		if len(got) != 3 {
			t.Fatalf("FetchModels(timeout=%v) ids = %#v, want 3 models", timeout, got)
		}
		// The default is 20s; a responsive server must not make the caller wait.
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("FetchModels(timeout=%v) took %v, want an immediate response", timeout, elapsed)
		}
	}
}

func TestFetchModelsContextCanceled(t *testing.T) {
	release := make(chan struct{})
	ms := newModelServer(t, func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
		_, _ = w.Write([]byte(testModelList))
	})
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	_, err := FetchModels(ctx, ms.URL, "k", 10*time.Second)
	if err == nil {
		t.Fatal("FetchModels() error = nil, want a cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is(err, context.Canceled) = false for %v", err)
	}
}

func TestFetchModelsAlreadyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := FetchModels(ctx, "http://127.0.0.1:1", "k", 5*time.Second)
	if err == nil {
		t.Fatal("FetchModels() error = nil, want a cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("errors.Is(err, context.Canceled) = false for %v", err)
	}
}

func TestFetchModelsEmptyBaseURL(t *testing.T) {
	ms := newModelServer(t, jsonHandler(testModelList))

	for _, baseURL := range []string{"", "   ", "\t\n"} {
		got, err := FetchModels(context.Background(), baseURL, "k", 5*time.Second)
		if err == nil {
			t.Fatalf("FetchModels(baseURL=%q) error = nil, want error", baseURL)
		}
		if got != nil {
			t.Fatalf("FetchModels(baseURL=%q) ids = %#v, want nil", baseURL, got)
		}
		if !strings.Contains(err.Error(), "base_url") {
			t.Fatalf("error %q should tell the caller base_url is missing", err.Error())
		}
	}
	if reqs := ms.requests(); len(reqs) != 0 {
		t.Fatalf("an empty baseURL must not reach any endpoint, got %#v", reqs)
	}
}

func TestFetchModelsRepeatCallsAreIdempotent(t *testing.T) {
	ms := newModelServer(t, jsonHandler(testModelList))

	first, err := FetchModels(context.Background(), ms.URL, "k", 5*time.Second)
	if err != nil {
		t.Fatalf("first FetchModels() error = %v", err)
	}
	second, err := FetchModels(context.Background(), ms.URL, "k", 5*time.Second)
	if err != nil {
		t.Fatalf("second FetchModels() error = %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("first = %#v, second = %#v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("first = %#v, second = %#v", first, second)
		}
	}
	if reqs := ms.requests(); len(reqs) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(reqs))
	}
}
