package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

// providerTestConfig builds a two-provider catalog: a custom relay (active)
// plus the local echo provider.
func providerTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	if err := cfg.AddProvider(config.ProviderConfig{
		Name:    "relay",
		Label:   "中转",
		BaseURL: "https://relay.example.com/v1",
		Model:   "gpt-4o",
		Models:  []string{"gpt-4o", "deepseek-chat"},
	}); err != nil {
		t.Fatalf("AddProvider(relay): %v", err)
	}
	if err := cfg.AddProvider(config.ProviderConfig{Name: "echo", Type: "echo"}); err != nil {
		t.Fatalf("AddProvider(echo): %v", err)
	}
	if err := cfg.SetActiveProvider("relay"); err != nil {
		t.Fatalf("SetActiveProvider: %v", err)
	}
	return cfg
}

func providerTestModel(t *testing.T) *Model {
	t.Helper()
	cfg := providerTestConfig(t)
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model.(*Model)
}

// withTrackedRebuild installs a runtime carrying a transcript plus a factory
// that records how often it was asked to rebuild.
func withTrackedRebuild(t *testing.T, m *Model) (*int, *config.Config) {
	t.Helper()
	rt := agent.NewRuntime(agent.EchoProvider{})
	rt.SetMessages([]agent.Message{
		{Role: agent.RoleUser, Content: "hello"},
		{Role: agent.RoleAssistant, Content: "hi"},
	})
	m.SetRuntime(rt)

	calls := 0
	var seen config.Config
	m.SetRuntimeFactory(func(newCfg config.Config) (*agent.Runtime, error) {
		calls++
		seen = newCfg
		return agent.NewRuntime(agent.EchoProvider{}), nil
	})
	return &calls, &seen
}

// /provider 与 /permissions 一致：裸命令开浮层，不进转录。
func TestProviderMenuOpensOverlayAndListsCatalog(t *testing.T) {
	m := providerTestModel(t)
	before := len(m.blocks)

	m.handleInput("/provider")

	if m.state != stateProviderMenu {
		t.Fatalf("state = %v, want stateProviderMenu", m.state)
	}
	if len(m.blocks) != before {
		t.Fatalf("菜单写进了转录（blocks %d -> %d）", before, len(m.blocks))
	}
	menu := plainText(m.renderProviderMenu())
	for _, want := range []string{"relay", "中转", "echo", "https://relay.example.com/v1", "gpt-4o"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("菜单缺少 %q:\n%s", want, menu)
		}
	}
	if !strings.Contains(menu, "▶") {
		t.Fatalf("菜单没有光标标记:\n%s", menu)
	}
	if !strings.Contains(menu, "*") {
		t.Fatalf("菜单没有标出当前 provider:\n%s", menu)
	}
}

// 切换 provider 必须保住对话：重建 runtime 不能把 transcript 丢掉。
func TestProviderMenuEnterSwitchesAndKeepsConversation(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)

	m.handleInput("/provider")
	m = pressKey(m, key("down")) // relay -> echo
	m = pressKey(m, key("enter"))

	if *calls != 1 {
		t.Fatalf("runtime 重建了 %d 次，want 1", *calls)
	}
	if m.cfg.Provider.Name != "echo" {
		t.Fatalf("cfg provider = %q, want echo", m.cfg.Provider.Name)
	}
	if m.state != stateIdle {
		t.Fatalf("切换后没有回到 idle: %v", m.state)
	}
	msgs := m.Runtime().Messages()
	if len(msgs) != 2 || msgs[0].Content != "hello" {
		t.Fatalf("切换后对话丢失: %+v", msgs)
	}
}

// 选中当前 provider 是空操作：不该白白重建 runtime。
func TestProviderMenuEnterOnActiveIsNoOp(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)

	m.handleInput("/provider")
	m = pressKey(m, key("enter"))

	if *calls != 0 {
		t.Fatalf("选中当前 provider 仍重建了 %d 次", *calls)
	}
	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
	if m.cfg.Provider.Name != "relay" {
		t.Fatalf("provider = %q, want relay", m.cfg.Provider.Name)
	}
}

func TestProviderMenuEscKeepsProvider(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)

	m.handleInput("/provider")
	m = pressKey(m, key("down"))
	m = pressKey(m, key("esc"))

	if *calls != 0 {
		t.Fatalf("esc 之后仍重建了 runtime")
	}
	if m.cfg.Provider.Name != "relay" {
		t.Fatalf("esc 之后 provider 变成 %q", m.cfg.Provider.Name)
	}
	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
}

// 带参数的写法仍然可用 —— 浮层是补充，不是替代。
func TestProviderDirectCommandSwitches(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)

	m.handleInput("/provider echo")

	if m.state != stateIdle {
		t.Fatalf("直接命令不该打开浮层: %v", m.state)
	}
	if *calls != 1 || m.cfg.Provider.Name != "echo" {
		t.Fatalf("直接命令没有切换: calls=%d provider=%q", *calls, m.cfg.Provider.Name)
	}
	joined := strings.Join(m.blocks, "\n")
	if !strings.Contains(joined, "echo") {
		t.Fatalf("转录里没有结果说明: %q", joined)
	}
}

func TestProviderUnknownNameReportsAndKeepsState(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)

	m.handleInput("/provider nope")

	if *calls != 0 || m.cfg.Provider.Name != "relay" {
		t.Fatalf("未知 provider 改变了状态: calls=%d provider=%q", *calls, m.cfg.Provider.Name)
	}
	joined := strings.Join(m.blocks, "\n")
	for _, want := range []string{"nope", "relay", "echo"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("报错缺少 %q: %q", want, joined)
		}
	}
}

// /provider add 在 TUI 里也要写 config.json 并立即生效。
func TestProviderAddFromTUIWritesConfigAndRebuilds(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)

	m.handleInput("/provider add backup https://backup.example.com/v1 backup-model")

	if *calls != 1 {
		t.Fatalf("新增后重建了 %d 次 runtime，want 1", *calls)
	}
	if m.cfg.Provider.Name != "backup" {
		t.Fatalf("新增的 provider 未生效: %q", m.cfg.Provider.Name)
	}
	if _, err := config.Load(m.workingDir + "/config.json"); err != nil {
		t.Fatalf("config.json 未写入: %v", err)
	}
}

// 构建失败（例如新 provider 还没有密钥）时保留旧 runtime，但配置要停在新的
// provider 上，这样紧接着的 /key 才能把密钥存到正确的名字下。
func TestProviderRebuildFailureKeepsRuntimeAndConfig(t *testing.T) {
	m := providerTestModel(t)
	old := agent.NewRuntime(agent.EchoProvider{})
	old.SetMessages([]agent.Message{{Role: agent.RoleUser, Content: "hello"}})
	m.SetRuntime(old)
	m.SetRuntimeFactory(func(config.Config) (*agent.Runtime, error) {
		return nil, errors.New("api key is required")
	})

	m.handleInput("/provider echo")

	if m.Runtime() != old {
		t.Fatal("构建失败却换掉了 runtime")
	}
	if m.cfg.Provider.Name != "echo" {
		t.Fatalf("provider = %q, want echo（后续 /key 要写在这个名字下）", m.cfg.Provider.Name)
	}
	if joined := strings.Join(m.blocks, "\n"); !strings.Contains(joined, "api key is required") {
		t.Fatalf("失败原因没有显示: %q", joined)
	}
}

func TestProviderMenuWhileRunningIsRejected(t *testing.T) {
	m := providerTestModel(t)
	m.state = stateRunning

	m.handleInput("/provider")

	if m.state == stateProviderMenu {
		t.Fatal("运行中仍然打开了 provider 浮层")
	}
}

// /model 列出当前 provider 的模型并切换。
func TestModelMenuListsAndSwitches(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)

	m.handleInput("/model")
	if m.state != stateModelMenu {
		t.Fatalf("state = %v, want stateModelMenu", m.state)
	}
	menu := plainText(m.renderModelMenu())
	for _, want := range []string{"gpt-4o", "deepseek-chat", "relay"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("模型菜单缺少 %q:\n%s", want, menu)
		}
	}

	m = pressKey(m, key("down")) // gpt-4o -> deepseek-chat
	m = pressKey(m, key("enter"))

	if *calls != 1 {
		t.Fatalf("runtime 重建了 %d 次，want 1", *calls)
	}
	if m.cfg.Provider.Model != "deepseek-chat" {
		t.Fatalf("model = %q, want deepseek-chat", m.cfg.Provider.Model)
	}
	if msgs := m.Runtime().Messages(); len(msgs) != 2 {
		t.Fatalf("切换模型后对话丢失: %+v", msgs)
	}
}

func TestModelMenuEscCancels(t *testing.T) {
	m := providerTestModel(t)
	calls, _ := withTrackedRebuild(t, m)

	m.handleInput("/model")
	m = pressKey(m, key("down"))
	m = pressKey(m, key("esc"))

	if *calls != 0 || m.cfg.Provider.Model != "gpt-4o" {
		t.Fatalf("esc 后 model=%q calls=%d", m.cfg.Provider.Model, *calls)
	}
	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
}

// echo 没有模型：给一句说明，而不是打开一个空浮层。
func TestModelMenuEchoReportsInsteadOfOpening(t *testing.T) {
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)

	m.handleInput("/model")

	if m.state != stateIdle {
		t.Fatalf("echo 下不应打开模型浮层: %v", m.state)
	}
	if joined := strings.Join(m.blocks, "\n"); !strings.Contains(joined, "echo") {
		t.Fatalf("缺少说明: %q", joined)
	}
}

func TestProviderMenuBoundaries(t *testing.T) {
	t.Run("重复打开光标不漂移", func(t *testing.T) {
		m := providerTestModel(t)
		m.handleInput("/provider")
		first := m.providerCursor
		m.handleInput("/provider")
		if m.providerCursor != first {
			t.Fatalf("光标从 %d 变成 %d", first, m.providerCursor)
		}
	})

	t.Run("上下越界不 panic", func(t *testing.T) {
		m := providerTestModel(t)
		m.handleInput("/provider")
		for i := 0; i < 10; i++ {
			m = pressKey(m, key("up"))
		}
		if m.providerCursor != 0 {
			t.Fatalf("光标越界到 %d", m.providerCursor)
		}
		for i := 0; i < 10; i++ {
			m = pressKey(m, key("down"))
		}
		if m.providerCursor != len(m.providerMenu)-1 {
			t.Fatalf("光标越界到 %d", m.providerCursor)
		}
	})

	t.Run("单条目录也能渲染", func(t *testing.T) {
		cfg := config.Default()
		m := New(&cfg, t.TempDir(), "test")
		model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m = model.(*Model)
		m.handleInput("/provider")
		if m.state != stateProviderMenu {
			t.Fatalf("state = %v", m.state)
		}
		if strings.TrimSpace(plainText(m.renderProviderMenu())) == "" {
			t.Fatal("单条目录渲染为空")
		}
	})
}

// 浮层在窄/矮终端下不溢出，也不切碎边框。
func TestProviderAndModelMenusDoNotOverflow(t *testing.T) {
	for _, w := range []int{120, 80, 60, 40, 24} {
		for _, h := range []int{40, 24, 16, 12, 8} {
			for _, command := range []string{"/provider", "/model"} {
				m := providerTestModel(t)
				model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
				m = model.(*Model)
				m.handleInput(command)

				view := m.renderFrame()
				if rows := lineCount(view); rows > h {
					t.Fatalf("%dx%d %s: 帧 %d 行溢出", w, h, command, rows)
				}
				if opens, closes := strings.Count(view, "╭"), strings.Count(view, "╰"); opens != closes {
					t.Fatalf("%dx%d %s: 框不闭合 %d/%d", w, h, command, opens, closes)
				}
			}
		}
	}
}
