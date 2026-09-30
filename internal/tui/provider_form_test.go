package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

// fetchStub records every model-fetch call and answers from fields the test
// sets, so no test touches the network.
type fetchStub struct {
	calls  []fetchCall
	models []agent.ModelInfo
	err    error
	// block, when non-nil, holds the fetch until the context is cancelled —
	// the shape a slow provider has.
	block bool
}

// catalog builds fetched entries from ids, with no window reported.
func catalog(ids ...string) []agent.ModelInfo {
	infos := make([]agent.ModelInfo, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, agent.ModelInfo{ID: id})
	}
	return infos
}

// catalogWith builds entries that advertise a context window.
func catalogWith(pairs map[string]int) []agent.ModelInfo {
	infos := make([]agent.ModelInfo, 0, len(pairs))
	for id, window := range pairs {
		infos = append(infos, agent.ModelInfo{ID: id, ContextWindow: window})
	}
	return infos
}

// windowOf returns the reported window of one entry.
func windowOf(infos []agent.ModelInfo, id string) int {
	for _, info := range infos {
		if info.ID == id {
			return info.ContextWindow
		}
	}
	return 0
}

type fetchCall struct {
	baseURL string
	apiKey  string
}

func (s *fetchStub) install(m *Model) {
	m.SetModelFetcher(func(ctx context.Context, baseURL, apiKey string) ([]agent.ModelInfo, error) {
		s.calls = append(s.calls, fetchCall{baseURL: baseURL, apiKey: apiKey})
		if s.block {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return s.models, s.err
	})
}

// runCmd executes one command and feeds its message back into the model, the
// way the tea runtime would.
func runCmd(t *testing.T, m *Model, cmd tea.Cmd) *Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if msg == nil {
		return m
	}
	model, _ := m.Update(msg)
	return model.(*Model)
}

// activeProviderWithKey gives the active provider an inline key, which is what
// /model refresh needs before it can call the provider at all.
func activeProviderWithKey(t *testing.T, cfg *config.Config) {
	t.Helper()
	if err := cfg.AddProvider(config.ProviderConfig{Name: cfg.Provider.Name, APIKey: "sk-test"}); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
}

func openProviderForm(t *testing.T, m *Model) *Model {
	t.Helper()
	m.handleInput("/provider add")
	if m.state != stateProviderForm {
		t.Fatalf("state = %v, want stateProviderForm", m.state)
	}
	return m
}

// typeIntoForm fills the three fields in order.
func typeIntoForm(t *testing.T, m *Model, name, url, apiKey string) *Model {
	t.Helper()
	m = typeText(m, name)
	m = pressKey(m, key("tab"))
	m = typeText(m, url)
	m = pressKey(m, key("tab"))
	m = typeText(m, apiKey)
	return m
}

// 表单完成后必须回到「三项都填好」的状态，而不是留下一半的配置。
func TestProviderFormSubmitFetchesFromTheEnteredEndpoint(t *testing.T) {
	m := providerTestModel(t)
	stub := &fetchStub{models: catalog("gpt-4o", "deepseek-chat", "o3")}
	stub.install(m)
	calls, _ := withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk-backup")
	model, cmd := m.handleKey(key("enter"))
	m = model.(*Model)

	if m.state != stateModelsFetching {
		t.Fatalf("state = %v, want stateModelsFetching", m.state)
	}
	if cmd == nil {
		t.Fatal("submitting did not schedule the model fetch")
	}
	if *calls != 0 {
		t.Fatal("runtime rebuilt before the models were chosen")
	}

	m = runCmd(t, m, cmd)
	// The request runs as a command, not inside Update: the UI must not block
	// on the provider while the user waits.
	if len(stub.calls) != 1 {
		t.Fatalf("fetcher called %d times, want 1", len(stub.calls))
	}
	if stub.calls[0].baseURL != "https://backup.example.com/v1" || stub.calls[0].apiKey != "sk-backup" {
		t.Fatalf("fetch args = %+v, want the values typed into the form", stub.calls[0])
	}
	if m.state != stateModelSelect {
		t.Fatalf("state = %v, want stateModelSelect", m.state)
	}
	if len(m.modelSelectItems) != 3 || !m.modelSelectChecked[0] {
		t.Fatalf("select state = %v / %v", m.modelSelectItems, m.modelSelectChecked)
	}
}

// 全选是默认：拉回来的模型一个都不该被悄悄丢掉。
func TestModelSelectDefaultsToAllChecked(t *testing.T) {
	m := providerTestModel(t)
	stub := &fetchStub{models: catalog("a", "b", "c")}
	stub.install(m)
	withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk")
	_, cmd := m.handleKey(key("enter"))
	m = runCmd(t, m, cmd)

	for i, checked := range m.modelSelectChecked {
		if !checked {
			t.Fatalf("item %d (%q) not checked by default", i, m.modelSelectItems[i])
		}
	}
	if m.modelSelectCursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.modelSelectCursor)
	}
	rendered := plainText(m.renderModelSelect())
	for _, want := range []string{"[x] a", "[x] b", "[x] c", "space", "enter"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("select overlay missing %q:\n%s", want, rendered)
		}
	}
}

func TestModelSelectTogglesAndConfirms(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: catalog("m1", "m2", "m3")}
	stub.install(m)
	calls, _ := withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk-backup")
	_, cmd := m.handleKey(key("enter"))
	m = runCmd(t, m, cmd)

	// Uncheck the middle one, then confirm.
	m = pressKey(m, key("down"))
	m = pressKey(m, key("space"))
	model, _ = m.handleKey(key("enter"))
	m = model.(*Model)

	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
	if *calls != 1 {
		t.Fatalf("runtime rebuilt %d times, want 1", *calls)
	}
	if cfg.Provider.Name != "backup" {
		t.Fatalf("active provider = %q, want backup", cfg.Provider.Name)
	}
	if got := strings.Join(cfg.Provider.CatalogModels(), ","); got != "m1,m3" {
		t.Fatalf("models = %q, want the checked subset", got)
	}
	if cfg.Provider.Model != "m1" {
		t.Fatalf("active model = %q, want the first checked one", cfg.Provider.Model)
	}

	// The provider and its key are persisted: a form that forgets the key would
	// leave a provider nobody can use.
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	added, ok := reloaded.ProviderByName("backup")
	if !ok {
		t.Fatal("added provider missing from config.json")
	}
	if strings.Join(added.CatalogModels(), ",") != "m1,m3" {
		t.Fatalf("persisted models = %v", added.CatalogModels())
	}
	store, err := config.LoadSecretStore(dir)
	if err != nil {
		t.Fatalf("LoadSecretStore: %v", err)
	}
	if store.ProviderKeys["backup"] != "sk-backup" {
		t.Fatalf("key store = %+v, want the typed key", store.ProviderKeys)
	}
	joined := strings.Join(m.blocks, "\n")
	if !strings.Contains(joined, "backup") {
		t.Fatalf("no confirmation in the transcript: %q", joined)
	}
}

func TestModelSelectAllKeyTogglesEverything(t *testing.T) {
	m := providerTestModel(t)
	stub := &fetchStub{models: catalog("a", "b")}
	stub.install(m)
	withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk")
	_, cmd := m.handleKey(key("enter"))
	m = runCmd(t, m, cmd)

	m = pressKey(m, key("a"))
	for i, checked := range m.modelSelectChecked {
		if checked {
			t.Fatalf("item %d still checked after toggle-all", i)
		}
	}
	m = pressKey(m, key("a"))
	for i, checked := range m.modelSelectChecked {
		if !checked {
			t.Fatalf("item %d not re-checked", i)
		}
	}
}

// 一个模型都不选就没有可用配置：停在下拉层并说明，而不是存一个空 provider。
func TestModelSelectRejectsEmptySelection(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: catalog("a")}
	stub.install(m)
	calls, _ := withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk")
	_, cmd := m.handleKey(key("enter"))
	m = runCmd(t, m, cmd)

	m = pressKey(m, key("space")) // uncheck the only item
	model, _ = m.handleKey(key("enter"))
	m = model.(*Model)

	if m.state != stateModelSelect {
		t.Fatalf("state = %v, want to stay in the select overlay", m.state)
	}
	if *calls != 0 {
		t.Fatal("runtime rebuilt without a model")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		t.Fatal("config.json written despite an empty selection")
	}
	if !strings.Contains(plainText(m.renderModelSelect()), "至少") {
		t.Fatalf("no explanation shown:\n%s", plainText(m.renderModelSelect()))
	}
}

// 中途取消 = 什么都没发生：新增的 provider 不落盘，也不改内存目录。
func TestModelSelectEscDiscardsTheAdd(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	before := len(cfg.Providers)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: catalog("a")}
	stub.install(m)
	calls, _ := withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk")
	_, cmd := m.handleKey(key("enter"))
	m = runCmd(t, m, cmd)
	m = pressKey(m, key("esc"))

	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
	if len(cfg.Providers) != before {
		t.Fatalf("catalog changed on cancel: %d -> %d", before, len(cfg.Providers))
	}
	if *calls != 0 {
		t.Fatal("runtime rebuilt on cancel")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		t.Fatal("config.json written on cancel")
	}
	store, _ := config.LoadSecretStore(dir)
	if store.ProviderKeys["backup"] != "" {
		t.Fatal("key stored on cancel")
	}
}

func TestProviderFormEscCancels(t *testing.T) {
	m := providerTestModel(t)
	stub := &fetchStub{models: catalog("a")}
	stub.install(m)

	m = openProviderForm(t, m)
	m = typeText(m, "half-typed")
	m = pressKey(m, key("esc"))

	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
	if len(stub.calls) != 0 {
		t.Fatal("fetch happened on cancel")
	}
}

// 校验必须在表单里当场报错：等到请求时才说「base_url 缺失」太晚了。
func TestProviderFormValidatesInPlace(t *testing.T) {
	cases := []struct {
		name     string
		formName string
		formURL  string
		formKey  string
		want     string
	}{
		{name: "empty name", formName: "", formURL: "https://x.example.com/v1", formKey: "sk", want: "name"},
		{name: "relative url", formName: "backup", formURL: "backup.example.com/v1", formKey: "sk", want: "base_url"},
		{name: "missing url", formName: "backup", formURL: "", formKey: "sk", want: "base_url"},
		{name: "name with space", formName: "my backup", formURL: "https://x.example.com/v1", formKey: "sk", want: "whitespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := providerTestModel(t)
			stub := &fetchStub{models: catalog("a")}
			stub.install(m)

			m = openProviderForm(t, m)
			m = typeIntoForm(t, m, tc.formName, tc.formURL, tc.formKey)
			model, cmd := m.handleKey(key("enter"))
			m = model.(*Model)

			if m.state != stateProviderForm {
				t.Fatalf("state = %v, want to stay in the form", m.state)
			}
			if cmd != nil {
				t.Fatal("a command was scheduled for invalid input")
			}
			if len(stub.calls) != 0 {
				t.Fatal("fetch attempted with invalid input")
			}
			if !strings.Contains(m.providerForm.err, tc.want) {
				t.Fatalf("form error %q should mention %q", m.providerForm.err, tc.want)
			}
			if !strings.Contains(plainText(m.renderProviderForm()), tc.want) {
				t.Fatalf("error not visible in the form:\n%s", plainText(m.renderProviderForm()))
			}
		})
	}
}

// 拉取失败要把已填内容原样留住，否则用户得把 URL 重打一遍。
func TestProviderFormFetchFailureReturnsWithValues(t *testing.T) {
	m := providerTestModel(t)
	stub := &fetchStub{err: errors.New("provider returned status 401")}
	stub.install(m)
	calls, _ := withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk-bad")
	_, cmd := m.handleKey(key("enter"))
	m = runCmd(t, m, cmd)

	if m.state != stateProviderForm {
		t.Fatalf("state = %v, want back in the form", m.state)
	}
	if m.providerForm.name.Value() != "backup" ||
		m.providerForm.url.Value() != "https://backup.example.com/v1" ||
		m.providerForm.key.Value() != "sk-bad" {
		t.Fatalf("form fields lost: %q / %q / %q",
			m.providerForm.name.Value(), m.providerForm.url.Value(), m.providerForm.key.Value())
	}
	if !strings.Contains(m.providerForm.err, "401") {
		t.Fatalf("form error = %q, want the fetch failure", m.providerForm.err)
	}
	if *calls != 0 {
		t.Fatal("runtime rebuilt on a failed fetch")
	}
}

// 不填 key 也能保存（配合 api_key_env），并说明下一步。
func TestProviderFormEmptyKeySavesWithoutFetching(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: catalog("a")}
	stub.install(m)
	calls, _ := withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "")
	model, cmd := m.handleKey(key("enter"))
	m = model.(*Model)

	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
	if len(stub.calls) != 0 {
		t.Fatal("fetched models without a key")
	}
	if *calls != 1 {
		t.Fatalf("runtime rebuilt %d times, want 1", *calls)
	}
	if cfg.Provider.Name != "backup" {
		t.Fatalf("active provider = %q", cfg.Provider.Name)
	}
	if _, err := config.Load(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("config.json not written: %v", err)
	}
	if joined := strings.Join(m.blocks, "\n"); !strings.Contains(joined, "api_key_env") {
		t.Fatalf("no next-step hint: %q", joined)
	}
	if cmd == nil {
		// idle returns a blink command; anything else is fine as long as the
		// form is gone.
		t.Log("no command returned")
	}
}

// 拉回来是空列表 → 保存但不写模型，也不假装成功。
func TestProviderFormEmptyModelListSaves(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{}
	stub.install(m)
	calls, _ := withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk")
	_, cmd := m.handleKey(key("enter"))
	m = runCmd(t, m, cmd)

	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
	if *calls != 1 {
		t.Fatalf("runtime rebuilt %d times, want 1", *calls)
	}
	if len(cfg.Provider.CatalogModels()) != 0 {
		t.Fatalf("models = %v, want none", cfg.Provider.CatalogModels())
	}
	if joined := strings.Join(m.blocks, "\n"); !strings.Contains(joined, "模型") {
		t.Fatalf("no explanation of the empty model list: %q", joined)
	}
}

// esc 取消拉取后，迟到的结果必须被丢弃（否则会凭空弹出一个选择层）。
func TestModelsFetchCancelDropsLateResult(t *testing.T) {
	m := providerTestModel(t)
	stub := &fetchStub{block: true}
	stub.install(m)
	withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk")
	_, cmd := m.handleKey(key("enter"))
	if m.state != stateModelsFetching {
		t.Fatalf("state = %v", m.state)
	}
	m = pressKey(m, key("esc"))
	if m.state != stateIdle {
		t.Fatalf("state = %v after esc", m.state)
	}

	// The blocked fetch now resolves; its message must not reopen anything.
	m = runCmd(t, m, cmd)
	if m.state != stateIdle {
		t.Fatalf("late result reopened a state: %v", m.state)
	}
	if len(m.modelSelectItems) != 0 {
		t.Fatalf("late result populated the select list: %v", m.modelSelectItems)
	}
}

func TestModelRefreshFetchesAndPreselects(t *testing.T) {
	cfg := providerTestConfig(t)
	activeProviderWithKey(t, &cfg)
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: catalog("deepseek-chat", "brand-new")}
	stub.install(m)
	withTrackedRebuild(t, m)

	_, cmd := m.handleInput("/model refresh")
	if m.state != stateModelsFetching {
		t.Fatalf("state = %v, want stateModelsFetching", m.state)
	}
	m = runCmd(t, m, cmd)
	if len(stub.calls) != 1 {
		t.Fatalf("fetch calls = %d, want 1", len(stub.calls))
	}
	if stub.calls[0].baseURL != cfg.Provider.BaseURL {
		t.Fatalf("fetched %q, want the active provider's endpoint", stub.calls[0].baseURL)
	}
	if stub.calls[0].apiKey != "sk-test" {
		t.Fatalf("api key = %q, want the resolved key", stub.calls[0].apiKey)
	}
	if m.state != stateModelSelect {
		t.Fatalf("state = %v, want stateModelSelect", m.state)
	}
	// The union of what was configured and what the provider offers, all
	// checked: a refresh must not drop a hand-configured model.
	want := "gpt-4o,deepseek-chat,brand-new"
	if got := strings.Join(m.modelSelectItems, ","); got != want {
		t.Fatalf("select items = %q, want %q", got, want)
	}
	for i, checked := range m.modelSelectChecked {
		if !checked {
			t.Fatalf("item %d not pre-checked", i)
		}
	}
}

func TestModelRefreshRequiresKeyAndSkipsEcho(t *testing.T) {
	t.Run("echo", func(t *testing.T) {
		cfg := config.Default()
		m := New(&cfg, t.TempDir(), "test")
		model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m = model.(*Model)
		stub := &fetchStub{models: catalog("a")}
		stub.install(m)

		m.handleInput("/model refresh")

		if len(stub.calls) != 0 {
			t.Fatal("fetched models for the echo provider")
		}
		if joined := strings.Join(m.blocks, "\n"); !strings.Contains(joined, "echo") {
			t.Fatalf("no explanation: %q", joined)
		}
	})

	t.Run("no key", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config.Default()
		if err := cfg.AddProvider(config.ProviderConfig{Name: "relay", BaseURL: "https://relay.example.com/v1", Model: "m"}); err != nil {
			t.Fatalf("AddProvider: %v", err)
		}
		m := New(&cfg, dir, "test")
		model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		m = model.(*Model)
		stub := &fetchStub{models: catalog("a")}
		stub.install(m)

		m.handleInput("/model refresh")

		if len(stub.calls) != 0 {
			t.Fatal("fetched models without a usable key")
		}
		if joined := strings.Join(m.blocks, "\n"); !strings.Contains(joined, "api_key_env") {
			t.Fatalf("hint should point at the key setting: %q", joined)
		}
	})
}

// 刷新后确认：选中的集合落盘，仍在列表里的当前模型保持不变。
func TestModelRefreshConfirmPersists(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	activeProviderWithKey(t, &cfg)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: catalog("gpt-4o", "brand-new")}
	stub.install(m)
	calls, _ := withTrackedRebuild(t, m)

	_, cmd := m.handleInput("/model refresh")
	m = runCmd(t, m, cmd)
	if m.state != stateModelSelect {
		t.Fatalf("state = %v, want stateModelSelect", m.state)
	}
	model, _ = m.handleKey(key("enter"))
	m = model.(*Model)

	if m.state != stateIdle {
		t.Fatalf("state = %v, want idle", m.state)
	}
	if *calls != 1 {
		t.Fatalf("runtime rebuilt %d times, want 1", *calls)
	}
	if got := strings.Join(cfg.Provider.CatalogModels(), ","); got != "gpt-4o,deepseek-chat,brand-new" {
		t.Fatalf("models = %q", got)
	}
	if cfg.Provider.Model != "gpt-4o" {
		t.Fatalf("active model = %q, want the still-offered gpt-4o kept", cfg.Provider.Model)
	}
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := strings.Join(reloaded.Provider.CatalogModels(), ","); got != "gpt-4o,deepseek-chat,brand-new" {
		t.Fatalf("persisted models = %q", got)
	}
}

func TestProviderMenuAddRowOpensTheForm(t *testing.T) {
	m := providerTestModel(t)

	m.handleInput("/provider")
	for i := 0; i < len(m.providerMenu)-1; i++ {
		m = pressKey(m, key("down"))
	}
	last := m.providerMenu[m.providerCursor]
	if !last.add {
		t.Fatalf("last menu row is not the add row: %+v", last)
	}
	if !strings.Contains(plainText(m.renderProviderMenu()), "新增") {
		t.Fatalf("add row not rendered:\n%s", plainText(m.renderProviderMenu()))
	}
	model, _ := m.handleKey(key("enter"))
	m = model.(*Model)
	if m.state != stateProviderForm {
		t.Fatalf("state = %v, want stateProviderForm", m.state)
	}
}

func TestProviderFormFocusCycles(t *testing.T) {
	m := openProviderForm(t, m0(t))

	if m.providerForm.focus != 0 {
		t.Fatalf("initial focus = %d, want 0", m.providerForm.focus)
	}
	m = pressKey(m, key("tab"))
	m = pressKey(m, key("tab"))
	if m.providerForm.focus != 2 {
		t.Fatalf("focus after two tabs = %d, want 2", m.providerForm.focus)
	}
	m = pressKey(m, key("tab"))
	if m.providerForm.focus != 0 {
		t.Fatalf("focus should wrap to 0, got %d", m.providerForm.focus)
	}
	m = pressKey(m, key("shift+tab"))
	if m.providerForm.focus != 2 {
		t.Fatalf("shift+tab should wrap backwards to 2, got %d", m.providerForm.focus)
	}
	m = pressKey(m, key("down"))
	m = pressKey(m, key("up"))
	if m.providerForm.focus != 2 {
		t.Fatalf("window style arrows should also move focus, got %d", m.providerForm.focus)
	}
}

// 密钥框必须遮起来：屏幕上的 key 会被肩窥与截图带走。
func TestProviderFormMasksTheKey(t *testing.T) {
	m := openProviderForm(t, m0(t))
	m = typeIntoForm(t, m, "backup", "https://backup.example.com/v1", "sk-secret-value")

	rendered := plainText(m.renderProviderForm())
	if strings.Contains(rendered, "sk-secret-value") {
		t.Fatalf("the API key is rendered in the clear:\n%s", rendered)
	}
	if !strings.Contains(rendered, "*") {
		t.Fatalf("no mask characters in the key row:\n%s", rendered)
	}
	for _, want := range []string{"名称", "地址", "密钥"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("form missing the %q row:\n%s", want, rendered)
		}
	}
}

func TestProviderFormAndModelSelectDoNotOverflow(t *testing.T) {
	for _, w := range []int{120, 80, 60, 40, 24} {
		for _, h := range []int{40, 24, 16, 12, 8} {
			states := []struct {
				name  string
				state appState
			}{
				{"form", stateProviderForm},
				{"select", stateModelSelect},
				{"fetching", stateModelsFetching},
			}
			for _, tc := range states {
				m := providerTestModel(t)
				model, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
				m = model.(*Model)
				m.state = tc.state
				m.providerForm = newProviderForm(w)
				m.providerForm.err = "provider \"backup\": base_url 必须是绝对 http(s) 地址"
				m.modelSelectItems = []string{"m1", "m2", "m3", "m4", "m5"}
				m.modelSelectChecked = []bool{true, true, true, true, true}

				view := m.renderFrame()
				if rows := lineCount(view); rows > h {
					t.Fatalf("%dx%d %s: frame is %d rows, over the terminal", w, h, tc.name, rows)
				}
				if opens, closes := strings.Count(view, "╭"), strings.Count(view, "╰"); opens != closes {
					t.Fatalf("%dx%d %s: box not closed %d/%d", w, h, tc.name, opens, closes)
				}
			}
		}
	}
}

// m0 is a bare model for the focus/mask tests, which do not need a catalog.
func m0(t *testing.T) *Model {
	t.Helper()
	cfg := config.Default()
	m := New(&cfg, t.TempDir(), "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return model.(*Model)
}

// 表单与选择层不能把 provider 菜单的状态搞丢：打断一次之后仍能重新打开。
func TestProviderFlowIsRepeatable(t *testing.T) {
	m := providerTestModel(t)
	stub := &fetchStub{models: catalog("a")}
	stub.install(m)
	withTrackedRebuild(t, m)

	for i := 0; i < 3; i++ {
		m = openProviderForm(t, m)
		m = pressKey(m, key("esc"))
		if m.state != stateIdle {
			t.Fatalf("iteration %d: state = %v", i, m.state)
		}
	}
	m.handleInput("/provider")
	if m.state != stateProviderMenu {
		t.Fatalf("provider menu broken after repeated cancels: %v", m.state)
	}
}

// cursorLine returns the row the overlay marks as current.
func cursorLine(rendered string) string {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "▶") {
			return line
		}
	}
	return ""
}

// 长列表必须滚动，而不是把框推出屏幕——并且光标始终可见，否则用户根本不知道
// 自己在选哪一项。
func TestLongMenusScrollWithTheCursor(t *testing.T) {
	longConfig := func(t *testing.T) config.Config {
		t.Helper()
		cfg := config.Default()
		for i := 0; i < 20; i++ {
			name := "p" + itoa(i)
			if err := cfg.AddProvider(config.ProviderConfig{
				Name:    name,
				BaseURL: "https://" + name + ".example.com/v1",
				Model:   "m",
			}); err != nil {
				t.Fatalf("AddProvider(%s): %v", name, err)
			}
		}
		if err := cfg.SetActiveProvider("p0"); err != nil {
			t.Fatalf("SetActiveProvider: %v", err)
		}
		return cfg
	}

	t.Run("provider menu", func(t *testing.T) {
		cfg := longConfig(t)
		m := New(&cfg, t.TempDir(), "test")
		model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
		m = model.(*Model)
		m.handleInput("/provider")

		for i := 0; i < 5; i++ {
			m = pressKey(m, key("down"))
		}
		rendered := plainText(m.renderProviderMenu())
		if line := cursorLine(rendered); !strings.Contains(line, "p5") {
			t.Fatalf("cursor row is %q, want it on p5:\n%s", line, rendered)
		}
		if !strings.Contains(rendered, "显示") {
			t.Fatalf("no window indicator for a scrolled list:\n%s", rendered)
		}
		if rows := lineCount(m.renderFrame()); rows > 12 {
			t.Fatalf("frame is %d rows, over the terminal", rows)
		}
	})

	t.Run("model select", func(t *testing.T) {
		m := providerTestModel(t)
		model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
		m = model.(*Model)
		m.state = stateModelSelect
		for i := 0; i < 20; i++ {
			m.modelSelectItems = append(m.modelSelectItems, "m"+itoa(i))
		}
		m.modelSelectChecked = make([]bool, len(m.modelSelectItems))
		for i := range m.modelSelectChecked {
			m.modelSelectChecked[i] = true
		}
		for i := 0; i < 15; i++ {
			m = pressKey(m, key("down"))
		}

		rendered := plainText(m.renderModelSelect())
		if line := cursorLine(rendered); !strings.Contains(line, "m15") || !strings.Contains(line, "[x]") {
			t.Fatalf("cursor row is %q, want it on the checked m15:\n%s", line, rendered)
		}
		if rows := lineCount(m.renderFrame()); rows > 12 {
			t.Fatalf("frame is %d rows, over the terminal", rows)
		}
	})

	t.Run("window helper keeps the cursor inside", func(t *testing.T) {
		cases := []struct {
			total, cursor, budget int
			wantStart, wantEnd    int
		}{
			{total: 0, cursor: 0, budget: 3, wantStart: 0, wantEnd: 0},
			{total: 5, cursor: 0, budget: 0, wantStart: 0, wantEnd: 5},
			{total: 5, cursor: 9, budget: 0, wantStart: 0, wantEnd: 5},
			{total: 5, cursor: 0, budget: 9, wantStart: 0, wantEnd: 5},
			{total: 10, cursor: 0, budget: 3, wantStart: 0, wantEnd: 3},
			{total: 10, cursor: 9, budget: 3, wantStart: 7, wantEnd: 10},
			{total: 10, cursor: 5, budget: 4, wantStart: 3, wantEnd: 7},
			{total: 10, cursor: -3, budget: 3, wantStart: 0, wantEnd: 3},
			{total: 10, cursor: 99, budget: 4, wantStart: 6, wantEnd: 10},
		}
		for _, tc := range cases {
			start, end := visibleMenuWindow(tc.total, tc.cursor, tc.budget)
			if start != tc.wantStart || end != tc.wantEnd {
				t.Fatalf("visibleMenuWindow(total=%d, cursor=%d, budget=%d) = (%d,%d), want (%d,%d)",
					tc.total, tc.cursor, tc.budget, start, end, tc.wantStart, tc.wantEnd)
			}
		}
	})
}

// 从 /models 读到的窗口必须写进 context_window：否则第三方中转商的模型会一直用
// 8k 的猜测值，ctx 一开机就显示 57%。
func TestFetchedWindowIsSavedWithTheProvider(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: []agent.ModelInfo{
		{ID: "qwen3:30b", ContextWindow: 262144},
		{ID: "tiny", ContextWindow: 4096},
	}}
	stub.install(m)
	withTrackedRebuild(t, m)

	m = openProviderForm(t, m)
	m = typeIntoForm(t, m, "local", "https://local.example.com/v1", "sk")
	_, cmd := m.handleKey(key("enter"))
	m = runCmd(t, m, cmd)

	// The catalog order is kept, so the first entry is the active model.
	if m.modelSelectItems[0] != "qwen3:30b" {
		t.Fatalf("select items = %v", m.modelSelectItems)
	}
	model2, _ := m.handleKey(key("enter"))
	m = model2.(*Model)

	if cfg.Provider.ContextWindow != 262144 {
		t.Fatalf("context_window = %d, want the window the provider reported", cfg.Provider.ContextWindow)
	}
	if cfg.Provider.ContextWindowSource() != config.ContextWindowSourceConfig {
		t.Fatalf("source = %q, want config", cfg.Provider.ContextWindowSource())
	}
	reloaded, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Provider.ContextWindow != 262144 {
		t.Fatalf("persisted context_window = %d", reloaded.Provider.ContextWindow)
	}
}

// 勾选的模型没有上报窗口时不能把用户手填的窗口清掉。
func TestFetchedWindowDoesNotClearAConfiguredOne(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	if err := cfg.AddProvider(config.ProviderConfig{
		Name: "relay", BaseURL: "https://relay.example.com/v1", Model: "gpt-4o", ContextWindow: 64000,
	}); err != nil {
		t.Fatalf("AddProvider: %v", err)
	}
	activeProviderWithKey(t, &cfg)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: catalog("gpt-4o", "other")} // no windows reported
	stub.install(m)
	withTrackedRebuild(t, m)

	_, cmd := m.handleInput("/model refresh")
	m = runCmd(t, m, cmd)
	if m.state != stateModelSelect {
		t.Fatalf("state = %v, want the multi-select", m.state)
	}
	model2, _ := m.handleKey(key("enter"))
	m = model2.(*Model)

	if cfg.Provider.ContextWindow != 64000 {
		t.Fatalf("context_window = %d, want the configured 64000 kept", cfg.Provider.ContextWindow)
	}
}

// 刷新时，被选中的模型如果上报了窗口就更新 context_window。
func TestRefreshUpdatesTheWindowOfTheActiveModel(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	activeProviderWithKey(t, &cfg)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: []agent.ModelInfo{
		{ID: "gpt-4o", ContextWindow: 128000},
		{ID: "deepseek-chat", ContextWindow: 65536},
	}}
	stub.install(m)
	withTrackedRebuild(t, m)

	_, cmd := m.handleInput("/model refresh")
	m = runCmd(t, m, cmd)
	if m.state != stateModelSelect {
		t.Fatalf("state = %v, want the multi-select", m.state)
	}
	model2, _ := m.handleKey(key("enter"))
	m = model2.(*Model)

	if cfg.Provider.Model != "gpt-4o" {
		t.Fatalf("active model = %q", cfg.Provider.Model)
	}
	if cfg.Provider.ContextWindow != 128000 {
		t.Fatalf("context_window = %d, want the active model's reported window", cfg.Provider.ContextWindow)
	}
}

// 同一次会话里把模型切到另一个已拉取过的模型时，窗口要跟着模型走。
func TestSwitchingModelAppliesItsFetchedWindow(t *testing.T) {
	dir := t.TempDir()
	cfg := providerTestConfig(t)
	activeProviderWithKey(t, &cfg)
	m := New(&cfg, dir, "test")
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = model.(*Model)
	stub := &fetchStub{models: []agent.ModelInfo{
		{ID: "gpt-4o", ContextWindow: 128000},
		{ID: "deepseek-chat", ContextWindow: 65536},
	}}
	stub.install(m)
	withTrackedRebuild(t, m)

	_, cmd := m.handleInput("/model refresh")
	m = runCmd(t, m, cmd)
	model2, _ := m.handleKey(key("enter"))
	m = model2.(*Model)
	if cfg.Provider.ContextWindow != 128000 {
		t.Fatalf("window after refresh = %d", cfg.Provider.ContextWindow)
	}

	// Now switch models: the window must follow, otherwise the indicator keeps
	// dividing by the previous model's window.
	m.handleInput("/model deepseek-chat")
	if cfg.Provider.Model != "deepseek-chat" {
		t.Fatalf("model = %q", cfg.Provider.Model)
	}
	if cfg.Provider.ContextWindow != 65536 {
		t.Fatalf("context_window = %d, want 65536 after switching", cfg.Provider.ContextWindow)
	}
}

// 猜测出来的窗口必须在界面上看得出来，否则用户会以为模型真的只有 8k。
func TestGuessedWindowIsMarked(t *testing.T) {
	t.Run("header", func(t *testing.T) {
		cfg := providerTestConfig(t)
		m := New(&cfg, t.TempDir(), "test")
		model, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
		m = model.(*Model)
		m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))

		// relay serves gpt-4o, which the built-in table knows.
		if got := plainText(m.renderContextSegment()); strings.Contains(got, "估") {
			t.Fatalf("known model marked as a guess: %q", got)
		}

		m.cfg.Provider.Model = "qwen3:30b"
		if got := plainText(m.renderContextSegment()); !strings.Contains(got, "估") {
			t.Fatalf("guessed window not marked in the header: %q", got)
		}

		m.cfg.Provider.ContextWindow = 200000
		if got := plainText(m.renderContextSegment()); strings.Contains(got, "估") {
			t.Fatalf("explicit window still marked as a guess: %q", got)
		}
	})

	t.Run("status", func(t *testing.T) {
		cfg := providerTestConfig(t)
		cfg.Provider.Model = "qwen3:30b"
		m := New(&cfg, t.TempDir(), "test")
		model, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
		m = model.(*Model)
		m.SetRuntime(agent.NewRuntime(agent.EchoProvider{}))

		status := plainText(m.renderStatus())
		if !strings.Contains(status, "估值") || !strings.Contains(status, "context_window") {
			t.Fatalf("status should explain the guessed window:\n%s", status)
		}
	})
}
