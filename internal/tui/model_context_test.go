package tui

import (
	"testing"

	"github.com/wislist/mini-opencode/internal/config"
)

func TestModelContextSettingKeepsConversation(t *testing.T) {
	m := providerTestModel(t)
	calls, seen := withTrackedRebuild(t, m)
	m.handleInput("/model context 1M")
	if *calls != 1 || seen.Provider.ContextWindow != 1000000 {
		t.Fatalf("rebuild=%d window=%d", *calls, seen.Provider.ContextWindow)
	}
	if msgs := m.Runtime().Messages(); len(msgs) != 2 || msgs[0].Content != "hello" {
		t.Fatalf("lost conversation: %+v", msgs)
	}
	path, err := m.configPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Provider.ContextWindow != 1000000 {
		t.Fatalf("persist: %v %+v", err, cfg.Provider)
	}
}

func TestModelMenuContextShortcut(t *testing.T) {
	m := providerTestModel(t)
	m.handleInput("/model")
	m = pressKey(m, key("c"))
	if m.state != stateIdle || m.input.Value() != "/model context " {
		t.Fatalf("state=%v input=%q", m.state, m.input.Value())
	}
}

func TestModelContextInvalidAndQueryDoNotRebuild(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)
	before := m.cfg.Provider.ContextWindow
	for _, input := range []string{"/model context", "/model context 0", "/model context -1", "/model context 999999999999999999999M"} {
		m.handleInput(input)
		if *calls != 0 || m.cfg.Provider.ContextWindow != before {
			t.Fatalf("%s changed runtime/config", input)
		}
	}
}
