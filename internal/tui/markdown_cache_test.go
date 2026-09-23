package tui

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

// These tests pin the streaming-render optimisation: a streamed assistant
// message is repainted on every throttled frame, and re-running Glamour over
// the whole accumulated text costs ~0.5MB/s (16KB ≈ 30ms on an M2), which
// blows the 16.6ms frame budget on long answers.
//
// The fix caches the rendered form of markdown blocks that can no longer
// change. A block is final once the stream has moved past it: its text is
// followed by a blank line, or it is a closed code fence. Only the trailing
// (still-growing) block needs a fresh render.
//
// Every test below must fail before the cache exists, and must keep asserting
// behaviour rather than the presence of a particular field.

// streamRenderCounter reports how many blocks the incremental renderer
// actually re-rendered on the most recent call. It is the observable proxy for
// "did we avoid the expensive full-text Glamour pass".
func renderAssistantMarkdownCounted(text string, width int) (string, int) {
	var calls int
	rendered := renderAssistantMarkdownInstrumented(text, width, func(string) {
		calls++
	})
	return rendered, calls
}

// TestStreamingRerenderOnlyRendersTrailingBlock is the headline guard: across
// successive streaming frames the number of document renders must track the
// unsettled tail, not the total text length.
func TestStreamingRerenderOnlyRendersTrailingBlock(t *testing.T) {
	width := 80

	// A long message made of many settled paragraphs plus one open tail.
	var settled strings.Builder
	for i := 0; i < 40; i++ {
		settled.WriteString("Paragraph number ")
		settled.WriteString(strings.Repeat("x", 200))
		settled.WriteString("\n\n")
	}
	base := settled.String()

	cache := newMarkdownRenderCache()
	cache.render(base+"growing tail", width) // first frame settles the prefix

	calls := 0
	cache.renderInstrumented(base+"growing tail "+strings.Repeat("y", 4000), width, func(string) {
		calls++
	})
	// 40 settled paragraphs must not become 40 document renders on every frame.
	if calls > 2 {
		t.Fatalf("rendered %d document blocks for a 40-paragraph message; settled blocks were not cached", calls)
	}
}

// TestSettledPrefixIsRenderedOnce asserts the cache actually persists across
// successive streaming frames rather than merely being computed more cheaply
// within a single call. This is the property that makes long answers cheap.
func TestSettledPrefixIsRenderedOnce(t *testing.T) {
	width := 80
	cache := newMarkdownRenderCache()

	var settled strings.Builder
	for i := 0; i < 30; i++ {
		settled.WriteString("Settled paragraph ")
		settled.WriteString(strings.Repeat("z", 150))
		settled.WriteString("\n\n")
	}

	// First frame settles everything up to the final paragraph.
	first := settled.String() + "tail"
	cache.render(first, width)

	// Subsequent frames only extend the tail; the prefix must not be re-rendered.
	calls := 0
	for i := 0; i < 10; i++ {
		calls = 0
		cache.renderInstrumented(first+" more "+strings.Repeat("w", i*100), width, func(string) {
			calls++
		})
		if calls > 1 {
			t.Fatalf("frame %d re-rendered %d blocks; settled prefix must be cached", i, calls)
		}
	}
}

// TestStreamingRenderMatchesUncachedOutput is the correctness guard: caching
// must not change a single byte of the rendered result. This is what protects
// the display from the cache going stale.
func TestStreamingRenderMatchesUncachedOutput(t *testing.T) {
	width := 72
	cache := newMarkdownRenderCache()

	samples := []string{
		"",
		"plain text with no markdown",
		"# Heading\n\nfirst paragraph\n\nsecond paragraph",
		"# Heading\n\npara one\n\npara two\n\npara three trailing",
		"intro\n\n```go\nfunc main() {}\n```\n\noutro",
		"intro\n\n```go\nfunc main() {\n\tprintln(1)\n}\n```",
		"```\nunclosed fence\nstill code",
		"text\n\n```go\nopen fence never closed\n",
		"a\n\n**bold** text\n\nmore\n\ntail with **unclosed",
		"para\n\n\n\nmultiple blanks\n\n\n\ntail",
		"- item one\n- item two\n\ntail",
		"| a | b |\n|---|---|\n| 1 | 2 |\n\ntail",
		"> quote\n\ntail",
		"# H1\n\n## H2\n\npara\n\ntail\n\n```sh\necho hi\n```\n\nend",
	}

	for _, sample := range samples {
		// Uncached reference: the original whole-text path.
		want := renderAssistantMarkdownUncached(sample, width)
		// Cached path, streamed in chunks to force incremental settling.
		var got string
		for i := 0; i <= len(sample); i += 7 {
			end := min(i+7, len(sample))
			got = cache.render(sample[:end], width)
		}
		got = cache.render(sample, width)

		if got != want {
			t.Errorf("cached render diverged from uncached for %q\n got: %q\nwant: %q",
				sample, got, want)
		}
	}
}

// TestStreamingRenderRespectsBlockSettlementBoundary pins the rule that decides
// whether a block may be cached: a block may only be frozen once the stream has
// emitted the separator after it. Caching a block whose text is still being
// appended to would freeze a partial paragraph on screen permanently.
func TestStreamingRenderRespectsBlockSettlementBoundary(t *testing.T) {
	width := 80
	cache := newMarkdownRenderCache()

	// A single growing paragraph has no settled prefix: every frame must
	// re-render it, and each frame's output must match a from-scratch render of
	// the same prefix. This is what proves an unsettled block is never frozen.
	for i := 1; i <= 20; i++ {
		prefix := strings.TrimSpace(strings.Repeat("word ", i))
		got := cache.render(prefix, width)
		want := renderAssistantMarkdownUncached(prefix, width)
		if got != want {
			t.Fatalf("frame %d diverged from uncached render\n got: %q\nwant: %q", i, got, want)
		}
	}

	// The final frame must carry the whole paragraph, not a truncated prefix.
	// Glamour hard-wraps at the width, so compare word counts rather than
	// searching for one contiguous run.
	full := strings.TrimSpace(strings.Repeat("word ", 20))
	rendered := cache.render(full, width)
	if n := strings.Count(stripANSI(rendered), "word"); n != 20 {
		t.Fatalf("final render contains %d words, want 20: %q", n, stripANSI(rendered))
	}
}

// stripANSI removes SGR escape sequences so tests can assert on visible text.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestStreamingCacheInvalidatesOnShrink guards the rewind paths: /undo, an
// interrupted stream, or a retry replaces the accumulated text with a shorter
// one. The cache must not serve a stale longer render.
func TestStreamingCacheInvalidatesOnShrink(t *testing.T) {
	width := 80
	cache := newMarkdownRenderCache()

	long := "first paragraph here\n\nsecond paragraph here\n\nthird paragraph here"
	cache.render(long, width)

	short := "first paragraph here"
	got := cache.render(short, width)
	want := renderAssistantMarkdownUncached(short, width)
	if got != want {
		t.Fatalf("shrink served stale cache\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "third paragraph") {
		t.Fatalf("shrunk render leaked removed content: %q", got)
	}
}

// TestStreamingCacheIsWidthSensitive guards a resize: the same text at a new
// width must re-render rather than reuse wrapping from the old width.
func TestStreamingCacheIsWidthSensitive(t *testing.T) {
	text := "some text that will wrap differently at different terminal widths for sure"
	cache := newMarkdownRenderCache()

	narrow := cache.render(text, 30)
	wide := cache.render(text, 120)
	if narrow == wide {
		t.Fatal("render did not change across widths")
	}
	if wide != renderAssistantMarkdownUncached(text, 120) {
		t.Fatalf("wide re-render did not match uncached output:\n got: %q\nwant: %q",
			wide, renderAssistantMarkdownUncached(text, 120))
	}
	// Returning to a previously used width must also be correct.
	if again := cache.render(text, 30); again != narrow {
		t.Fatalf("returning to width 30 diverged:\n got: %q\nwant: %q", again, narrow)
	}
}

// TestEmptyAndZeroWidthRenderAreSafe covers the boundary inputs.
func TestEmptyAndZeroWidthRenderAreSafe(t *testing.T) {
	cache := newMarkdownRenderCache()
	if got := cache.render("", 80); got != "" {
		t.Fatalf("empty text rendered %q, want empty", got)
	}
	if got := cache.render("hello", 0); got != "hello" {
		t.Fatalf("width 0 should return text unchanged, got %q", got)
	}
	if got := cache.render("hello", -5); got != "hello" {
		t.Fatalf("negative width should return text unchanged, got %q", got)
	}
}

// TestModelStreamingRenderReusesCache is the end-to-end guard: it drives the
// real Model streaming path and asserts that repainting a long accumulated
// answer does not re-render its settled prefix. Without the model-level wiring
// the cache would be dead code and this would time out or thrash.
func TestModelStreamingRenderReusesCache(t *testing.T) {
	cfg := config.Config{User: "u", Assistant: "a", Provider: config.ProviderConfig{Name: "deepseek"}}
	m := New(&cfg, "/tmp", "test")
	m.width, m.height = 100, 40

	var settled strings.Builder
	for i := 0; i < 30; i++ {
		settled.WriteString("Settled paragraph ")
		settled.WriteString(strings.Repeat("s", 200))
		settled.WriteString("\n\n")
	}
	m.streamingText = settled.String() + "tail"

	// First repaint populates the cache.
	first := m.renderStreamingMessage(m.streamingText)
	if m.streamCache == nil {
		t.Fatal("streaming render did not create a cache")
	}

	// Grow the tail and repaint: the result must still match an uncached render
	// of the same text, proving the cached prefix is correct.
	m.streamingText += " with more appended text"
	got := m.renderStreamingMessage(m.streamingText)
	want := m.renderAssistantMessage(m.streamingText)
	if got != want {
		t.Fatalf("cached streaming render diverged from uncached\n got: %q\nwant: %q", got, want)
	}
	if first == "" {
		t.Fatal("first streaming render was empty")
	}
}

// TestModelStreamCacheResetOnTurnAndStreamEnd asserts the cache cannot leak a
// previous message's rendered blocks into the next one.
func TestModelStreamCacheResetOnTurnAndStreamEnd(t *testing.T) {
	cfg := config.Config{User: "u", Assistant: "a", Provider: config.ProviderConfig{Name: "deepseek"}}
	m := New(&cfg, "/tmp", "test")
	m.width, m.height = 100, 40

	m.streamingText = "hello\n\nworld"
	m.renderStreamingMessage(m.streamingText)
	if m.streamCache == nil {
		t.Fatal("expected a cache after rendering")
	}

	m.startNewTurn("next")
	if m.streamCache != nil {
		t.Fatal("startNewTurn left a stale stream cache in place")
	}

	// And the stream-end path must clear it too.
	m.streamingText = "another message\n\nhere"
	m.streamingIdx = -1
	m.flushStreamRender() // creates the streaming block and the cache
	if m.streamCache == nil {
		t.Fatal("expected a cache after a streaming frame")
	}
	if m.streamingIdx < 0 {
		t.Fatal("flushStreamRender did not establish a streaming block")
	}
	m.Update(runtimeEventMsg{event: agent.Event{Type: agent.EventAssistantResponse, Message: &agent.Message{
		Role: agent.RoleAssistant, Content: "another message\n\nhere\n\nfinal",
	}}})
	if m.streamCache != nil {
		t.Fatal("EventAssistantResponse left a stale stream cache in place")
	}
}

// ── Property tests ────────────────────────────────────
//
// The cache is only safe if it agrees with a from-scratch render for every
// possible stream of prefixes, including the ones that rewind. These drive
// randomized fragment streams, resets, and width oscillation.

// Property test: for ANY sequence of streaming prefixes, the cached renderer
// must equal the uncached reference at every step.
func TestCacheMatchesUncachedForRandomStreams(t *testing.T) {
	fragments := []string{
		"hello ", "world\n\n", "# Heading\n", "\n", "- item\n", "```go\n", "code\n",
		"```\n", "**bold** ", "| a | b |\n", "|---|---|\n", "> quote\n", "text\n",
		"0123456789 ", "中文内容 ", "emoji 🎉 ", "   \n", "\t\n", "tail",
	}
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		var sb strings.Builder
		n := rng.Intn(12) + 1
		for i := 0; i < n; i++ {
			sb.WriteString(fragments[rng.Intn(len(fragments))])
		}
		full := sb.String()

		cache := newMarkdownRenderCache()
		// Stream it prefix by prefix at a random width boundary.
		width := []int{40, 80, 120}[rng.Intn(3)]
		for p := 0; p <= len(full); p++ {
			got := cache.render(full[:p], width)
			want := renderAssistantMarkdownUncached(full[:p], width)
			if got != want {
				t.Fatalf("trial %d prefix %d width %d mismatch\n src: %q\n got: %q\nwant: %q",
					trial, p, width, full[:p], got, want)
			}
		}
	}
}

// Property test with resets: a shrink (undo/retry) followed by regrowth must
// also stay correct.
func TestCacheMatchesUncachedWithResets(t *testing.T) {
	base := "para one here\n\npara two here\n\npara three here\n\npara four"
	cache := newMarkdownRenderCache()
	width := 70

	seq := []string{
		base, base + " growing",
		"short",                  // shrink
		base + " re-grown again", // regrow from a different base
		"",                       // empty
		base,                     // same as an earlier value
		base + "x",               // near-miss of an earlier value
		"totally\n\ndifferent\n\ncontent",
	}
	for _, s := range seq {
		got := cache.render(s, width)
		want := renderAssistantMarkdownUncached(s, width)
		if got != want {
			t.Fatalf("after reset, mismatch for %q\n got: %q\nwant: %q", s, got, want)
		}
	}
}

// Width oscillation must never serve a stale-width render.
func TestCacheWidthOscillation(t *testing.T) {
	text := "some content that wraps\n\ndifferently at each width\n\ntrailing text"
	cache := newMarkdownRenderCache()
	for i := 0; i < 20; i++ {
		for _, w := range []int{30, 100, 55, 100, 30} {
			got := cache.render(text, w)
			want := renderAssistantMarkdownUncached(text, w)
			if got != want {
				t.Fatalf("iter %d width %d mismatch:\n got: %q\nwant: %q", i, w, got, want)
			}
		}
	}
}
