package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/agent"
	"github.com/wislist/mini-opencode/internal/config"
)

// modelFetchTimeout bounds one model-list request. A relay that hangs must not
// leave the picker spinning forever, and the whole flow is abandonable anyway.
const modelFetchTimeout = 20 * time.Second

// providerFormState is the three-field add form: name, endpoint, key.
//
// It is a value (not a pointer) so resetting the form is an assignment, and so
// a test can build one without a live Model.
type providerFormState struct {
	name  textinput.Model
	url   textinput.Model
	key   textinput.Model
	focus int
	// err is the validation or fetch error shown inside the form. Keeping it
	// here rather than in the transcript is deliberate: the message belongs
	// next to the fields it is about, and the fields must stay editable.
	err string
}

const providerFormFields = 3

// newProviderForm builds the three inputs sized to the overlay.
func newProviderForm(width int) providerFormState {
	field := func(placeholder string) textinput.Model {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholder
		input.CharLimit = 0
		input.SetWidth(max(8, width))
		return input
	}
	form := providerFormState{
		name: field("例如 relay"),
		url:  field("https://relay.example.com/v1"),
		key:  field("sk-...（留空则稍后用 /key 或 api_key_env 配置）"),
	}
	// The key is masked: a screen full of credentials survives screenshots,
	// shoulder surfing and screen sharing far longer than it should.
	form.key.EchoMode = textinput.EchoPassword
	form.name.Focus()
	return form
}

// pendingProviderAdd is a provider the form has collected but not yet written.
//
// Nothing is persisted before the model selection is confirmed, so cancelling
// anywhere in the flow leaves the configuration exactly as it was.
type pendingProviderAdd struct {
	name string
	url  string
	key  string
	// refresh marks the /model refresh flow, which edits the active provider
	// instead of adding one.
	refresh bool
}

// modelsFetchedMsg carries one model-list fetch back to the UI. token makes a
// cancelled fetch's late answer identifiable and droppable.
type modelsFetchedMsg struct {
	models []agent.ModelInfo
	err    error
	token  int
}

// openProviderForm enters the add form with empty fields.
func (m *Model) openProviderForm() {
	m.providerForm = newProviderForm(m.providerFormWidth())
	m.pendingAdd = nil
	m.state = stateProviderForm
	m.invalidateFooter()
	m.refreshViewport()
}

// providerFormWidth is the input width for the overlay at the current terminal
// size, leaving room for the box borders, padding and the field labels.
func (m *Model) providerFormWidth() int {
	if m.width <= 0 {
		return 48
	}
	return min(64, max(12, boxWidth(m)-14))
}

// providerFormFields returns the inputs in focus order.
func (m *Model) providerFormField(index int) *textinput.Model {
	switch index {
	case 0:
		return &m.providerForm.name
	case 1:
		return &m.providerForm.url
	default:
		return &m.providerForm.key
	}
}

// focusProviderFormField moves focus, wrapping at both ends so tab and
// shift+tab always land somewhere.
func (m *Model) focusProviderFormField(index int) {
	index = ((index % providerFormFields) + providerFormFields) % providerFormFields
	for i := 0; i < providerFormFields; i++ {
		if i == index {
			m.providerFormField(i).Focus()
			continue
		}
		m.providerFormField(i).Blur()
	}
	m.providerForm.focus = index
	m.invalidateFooter()
	m.refreshViewport()
}

func (m *Model) closeProviderForm() (tea.Model, tea.Cmd) {
	m.state = stateIdle
	m.pendingAdd = nil
	m.providerForm = providerFormState{}
	m.invalidateFooter()
	m.refreshViewport()
	return m, m.input.Focus()
}

// handleProviderFormKey drives the form.
func (m *Model) handleProviderFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "down", "ctrl+n":
		m.focusProviderFormField(m.providerForm.focus + 1)
		return m, nil
	case "shift+tab", "up", "ctrl+p":
		m.focusProviderFormField(m.providerForm.focus - 1)
		return m, nil
	case "enter":
		return m.submitProviderForm()
	case "esc", "ctrl+c":
		return m.closeProviderForm()
	}
	field := m.providerFormField(m.providerForm.focus)
	var cmd tea.Cmd
	*field, cmd = field.Update(msg)
	return m, cmd
}

// submitProviderForm validates what was typed and either saves immediately (no
// key: nothing to fetch, and the user may be using api_key_env) or asks the
// provider which models it serves.
func (m *Model) submitProviderForm() (tea.Model, tea.Cmd) {
	entry := config.ProviderConfig{
		Name:    strings.TrimSpace(m.providerForm.name.Value()),
		BaseURL: strings.TrimSpace(m.providerForm.url.Value()),
	}
	if err := entry.Validate(); err != nil {
		m.providerForm.err = err.Error()
		m.invalidateFooter()
		m.refreshViewport()
		return m, nil
	}
	key := strings.TrimSpace(m.providerForm.key.Value())
	m.pendingAdd = &pendingProviderAdd{name: entry.Name, url: entry.BaseURL, key: key}
	m.providerForm.err = ""

	if key == "" {
		m.savePendingProvider(nil)
		return m.closeProviderForm()
	}
	return m.startModelsFetch(entry.BaseURL, key)
}

// startModelsFetch switches to the waiting state and schedules the request.
func (m *Model) startModelsFetch(baseURL, apiKey string) (tea.Model, tea.Cmd) {
	m.modelsFetchToken++
	token := m.modelsFetchToken
	m.state = stateModelsFetching
	ctx, cancel := context.WithTimeout(context.Background(), modelFetchTimeout)
	m.modelsFetchCancel = cancel
	m.invalidateFooter()
	m.refreshViewport()

	fetcher := m.modelFetcher
	return m, func() tea.Msg {
		if fetcher == nil {
			return modelsFetchedMsg{err: errors.New("model fetcher is not configured"), token: token}
		}
		models, err := fetcher(ctx, baseURL, apiKey)
		return modelsFetchedMsg{models: models, err: err, token: token}
	}
}

// modelsFetched applies one fetch result. Answers from a cancelled fetch are
// dropped: they belong to a flow the user has already left.
func (m *Model) modelsFetched(msg modelsFetchedMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.modelsFetchToken || m.state != stateModelsFetching {
		return m, nil
	}
	m.modelsFetchCancel = nil

	if msg.err != nil {
		// Back to the form with everything still typed in: retyping the
		// endpoint to fix a typo in one character is the kind of friction that
		// makes a user give up on the picker.
		m.state = stateProviderForm
		m.providerForm.err = "拉取模型失败：" + msg.err.Error()
		m.invalidateFooter()
		m.refreshViewport()
		return m, nil
	}
	if len(msg.models) == 0 {
		m.savePendingProvider(nil)
		return m.closeProviderForm()
	}

	// Remember what the provider said about each model's context length. This
	// is the whole reason the fetch returns metadata: a guessed window is what
	// makes the ctx indicator read as nearly full on a fresh session.
	if m.modelWindows == nil {
		m.modelWindows = map[string]int{}
	}
	fetched := make([]string, 0, len(msg.models))
	for _, info := range msg.models {
		fetched = append(fetched, info.ID)
		if info.ContextWindow > 0 {
			m.modelWindows[info.ID] = info.ContextWindow
		}
	}

	// The union, not the fetched list: refreshing must not silently drop a
	// model that was configured by hand.
	var known []string
	if m.pendingAdd != nil && m.pendingAdd.refresh {
		known = m.cfg.Provider.CatalogModels()
	}
	m.setModelSelectItems(config.MergeModels(known, fetched))
	m.state = stateModelSelect
	m.invalidateFooter()
	m.refreshViewport()
	return m, nil
}

// setModelSelectItems fills the multi-select with everything checked, which is
// the safe default: an unchecked model is one the provider will not offer.
func (m *Model) setModelSelectItems(items []string) {
	m.modelSelectItems = items
	m.modelSelectChecked = make([]bool, len(items))
	for i := range m.modelSelectChecked {
		m.modelSelectChecked[i] = true
	}
	m.modelSelectCursor = 0
	m.modelSelectErr = ""
}

// renderProviderForm draws the three-field form.
func (m *Model) renderProviderForm() string {
	w := boxWidth(m)
	inner := max(1, w-4)

	width := m.providerFormWidth()
	for i := 0; i < providerFormFields; i++ {
		m.providerFormField(i).SetWidth(width)
	}

	labels := []string{"名称", "地址", "密钥"}
	lines := []string{
		keyLabel.Render("新增提供商:") + "  " +
			dimStyle.Render("tab/↑↓ 切换 · enter 提交 · esc 取消"),
	}
	for i, label := range labels {
		marker := "  "
		if i == m.providerForm.focus {
			marker = "▶ "
		}
		lines = append(lines, fmt.Sprintf("%s%s  %s", marker, toolLabelStyle(i == m.providerForm.focus, label), m.providerFormField(i).View()))
	}
	if m.providerForm.err != "" {
		lines = append(lines, errorStyle.Render("! "+m.providerForm.err))
	} else {
		lines = append(lines, dimStyle.Render("提交后会向该地址请求 /models，再让你勾选可用模型。"))
	}
	// Only the header and the three fields are essential. At eight rows nothing
	// more fits, and dropping the trailing hint (or the error line) is a far
	// smaller cost than a box that runs off the screen.
	return sessionBox.Width(w).Render(strings.Join(m.wrapMenuRows(lines, inner, 1+providerFormFields), "\n"))
}

// toolLabelStyle marks the focused field.
func toolLabelStyle(focused bool, label string) string {
	if focused {
		return toolName.Render(label)
	}
	return dimStyle.Render(label)
}

// renderModelsFetching is the waiting state: one line, so the user can see the
// request is in flight and that esc abandons it.
func (m *Model) renderModelsFetching() string {
	w := boxWidth(m)
	name := ""
	if m.pendingAdd != nil {
		name = m.pendingAdd.name + "  "
	}
	content := keyLabel.Render("拉取模型列表:") + "  " +
		dimStyle.Render(name+"请求 "+providerFetchURL(m.pendingAdd)+" · esc 取消")
	return sessionBox.Width(w).Render(content)
}

func providerFetchURL(pending *pendingProviderAdd) string {
	if pending == nil {
		return "(当前 provider)"
	}
	return strings.TrimRight(pending.url, "/") + "/models"
}

// renderModelSelect draws the fetched-model multi-select.
func (m *Model) renderModelSelect() string {
	w := boxWidth(m)
	inner := max(1, w-4)

	checked := 0
	for _, on := range m.modelSelectChecked {
		if on {
			checked++
		}
	}
	start, end := visibleMenuWindow(len(m.modelSelectItems), m.modelSelectCursor, m.menuItemBudget())
	lines := []string{
		keyLabel.Render("选择模型:") + "  " + dimStyle.Render(fmt.Sprintf(
			"space 勾选 · a 全选/全不选 · enter 确认（%d/%d）· esc 取消%s",
			checked, len(m.modelSelectItems), windowLabel(start, end, len(m.modelSelectItems)))),
	}
	for i := start; i < end; i++ {
		item := m.modelSelectItems[i]
		marker := "  "
		name := dimStyle.Render(item)
		if i == m.modelSelectCursor {
			marker = "▶ "
			name = toolName.Render(item)
		}
		box := "[ ]"
		if m.modelSelectChecked[i] {
			box = "[x]"
		}
		lines = append(lines, fmt.Sprintf("%s%s %s", marker, box, name))
	}
	if m.modelSelectErr != "" {
		lines = append(lines, errorStyle.Render("! "+m.modelSelectErr))
	} else {
		lines = append(lines, dimStyle.Render("勾选的模型会写进 config.json 的 models，第一个勾选的成为当前 model。"))
	}
	return sessionBox.Width(w).Render(strings.Join(m.wrapMenuRows(lines, inner, 1+(end-start)), "\n"))
}

// handleModelSelectKey drives the multi-select.
func (m *Model) handleModelSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "shift+tab":
		if m.modelSelectCursor > 0 {
			m.modelSelectCursor--
		}
	case "down", "j", "tab":
		if m.modelSelectCursor < len(m.modelSelectItems)-1 {
			m.modelSelectCursor++
		}
	case "space", "x":
		if m.modelSelectCursor < len(m.modelSelectChecked) {
			m.modelSelectChecked[m.modelSelectCursor] = !m.modelSelectChecked[m.modelSelectCursor]
			m.modelSelectErr = ""
		}
	case "a":
		all := true
		for _, on := range m.modelSelectChecked {
			if !on {
				all = false
				break
			}
		}
		for i := range m.modelSelectChecked {
			m.modelSelectChecked[i] = !all
		}
		m.modelSelectErr = ""
	case "enter":
		return m.confirmModelSelection()
	case "esc", "ctrl+c":
		return m.cancelModelSelection()
	default:
		return m, nil
	}
	m.invalidateFooter()
	m.refreshViewport()
	return m, nil
}

// confirmModelSelection writes the checked models and, for the add flow, the
// provider and its key.
func (m *Model) confirmModelSelection() (tea.Model, tea.Cmd) {
	selected := make([]string, 0, len(m.modelSelectItems))
	for i, item := range m.modelSelectItems {
		if i < len(m.modelSelectChecked) && m.modelSelectChecked[i] {
			selected = append(selected, item)
		}
	}
	if len(selected) == 0 {
		// A provider with no model cannot answer a single request, so an empty
		// selection is a dead end rather than a valid choice.
		m.modelSelectErr = "至少勾选一个模型（a 可全选）"
		m.invalidateFooter()
		m.refreshViewport()
		return m, nil
	}
	m.savePendingProvider(selected)
	return m.closeModelSelect()
}

func (m *Model) cancelModelSelection() (tea.Model, tea.Cmd) {
	m.state = stateIdle
	m.modelSelectItems = nil
	m.modelSelectChecked = nil
	m.modelSelectErr = ""
	m.pendingAdd = nil
	m.invalidateFooter()
	m.refreshViewport()
	return m, m.input.Focus()
}

func (m *Model) closeModelSelect() (tea.Model, tea.Cmd) {
	m.state = stateIdle
	m.modelSelectItems = nil
	m.modelSelectChecked = nil
	m.modelSelectErr = ""
	m.pendingAdd = nil
	m.invalidateFooter()
	m.refreshViewport()
	return m, m.input.Focus()
}

// savePendingProvider persists whatever the flow collected: the provider entry,
// its key, and the chosen models — then rebuilds so the switch takes effect.
//
// The order matters. The entry is validated and added first, then the models
// are attached, then the key lands in secrets.json, and only then is the file
// written: a partial failure leaves the in-memory catalog consistent with what
// the next save will contain.
func (m *Model) savePendingProvider(models []string) {
	pending := m.pendingAdd
	if pending == nil {
		return
	}
	if pending.refresh {
		if _, err := m.cfg.SetProviderModels(pending.name, models, ""); err != nil {
			m.addBlock(errorStyle.Render("✗ " + err.Error()))
			m.refreshViewport()
			return
		}
		if err := config.Save(m.configPath(), *m.cfg); err != nil {
			m.addBlock(errorStyle.Render("✗ 写入 config.json 失败：" + err.Error()))
			m.refreshViewport()
			return
		}
		windowNote := m.applyFetchedWindow(pending.name)
		m.addBlock(toolArrow.Render(fmt.Sprintf("已更新 provider %s 的模型（%d 个）· 当前 %s%s",
			pending.name, len(m.cfg.Provider.CatalogModels()), m.cfg.Provider.Model, windowNote)))
		m.rebuildWithNotice()
		return
	}

	entry := config.ProviderConfig{Name: pending.name, BaseURL: pending.url}
	if err := m.cfg.AddProvider(entry); err != nil {
		m.addBlock(errorStyle.Render("✗ " + err.Error()))
		m.refreshViewport()
		return
	}
	if _, err := m.cfg.SetProviderModels(pending.name, models, ""); err != nil {
		m.addBlock(errorStyle.Render("✗ " + err.Error()))
		m.refreshViewport()
		return
	}
	windowNote := m.applyFetchedWindow(pending.name)
	if pending.key != "" {
		if err := config.SaveProviderKey(m.workingDir, pending.name, pending.key); err != nil {
			m.addBlock(errorStyle.Render("✗ 保存密钥失败：" + err.Error()))
			m.refreshViewport()
			return
		}
	}
	if err := config.Save(m.configPath(), *m.cfg); err != nil {
		m.addBlock(errorStyle.Render("✗ 写入 config.json 失败：" + err.Error()))
		m.refreshViewport()
		return
	}

	switch {
	case pending.key == "" && len(models) == 0:
		m.addBlock(toolArrow.Render(fmt.Sprintf("已新增 provider %s → %s（已验证并写入 config.json）",
			pending.name, pending.url)))
		m.addBlock(dimStyle.Render("未填密钥，因此没有拉取模型：设置 api_key_env，或用 /provider add 以同名提供商补上密钥（第三个框），再用 /model refresh 拉取。"))
	case len(models) == 0:
		m.addBlock(toolArrow.Render(fmt.Sprintf("已新增 provider %s → %s（已验证并写入 config.json）",
			pending.name, pending.url)))
		m.addBlock(dimStyle.Render("该地址没有返回任何模型：用 /model <id> 指定，或在 config.json 的 models 里列出。"))
	default:
		m.addBlock(toolArrow.Render(fmt.Sprintf("已新增 provider %s → %s，模型 %d 个，当前 %s%s",
			pending.name, pending.url, len(m.cfg.Provider.CatalogModels()), m.cfg.Provider.Model, windowNote)))
	}
	m.rebuildWithNotice()
}

// applyFetchedWindow records the context length the provider reported for the
// model that just became active, and returns a note for the transcript.
//
// A provider that reports nothing leaves the configured value alone: clearing a
// hand-set window because a listing omitted the field would silently undo the
// user's own configuration.
func (m *Model) applyFetchedWindow(providerName string) string {
	model := m.cfg.Provider.Model
	if model == "" {
		return ""
	}
	window := m.modelWindows[model]
	if window <= 0 {
		return ""
	}
	changed, err := m.cfg.SetProviderContextWindow(providerName, window)
	if err != nil || !changed {
		return ""
	}
	return fmt.Sprintf(" · 窗口 %s（来自 /models）", config.FormatContextWindow(window))
}

// refreshModels re-fetches the model list for the active provider.
func (m *Model) refreshModels() (tea.Model, tea.Cmd) {
	if m.state != stateIdle {
		m.addBlock(errorStyle.Render("请等待当前操作结束，再拉取模型。"))
		m.refreshViewport()
		return m, nil
	}
	if m.cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
		m.addBlock(dimStyle.Render(fmt.Sprintf(
			"provider %q 是本地回显，没有模型列表可拉取。", m.cfg.Provider.Name)))
		m.refreshViewport()
		return m, nil
	}
	key := m.cfg.Provider.ResolvedAPIKeyFrom(m.workingDir)
	if key == "" {
		// The env var may be unset, so name the field rather than an empty
		// string: "设置 " with nothing after it reads as a bug.
		m.addBlock(errorStyle.Render(fmt.Sprintf(
			"provider %q 还没有可用的密钥：用 /provider add 以同名提供商补上（第三个框写密钥），或在 config.json 里填 api_key / api_key_env。",
			m.cfg.Provider.Name)))
		m.refreshViewport()
		return m, nil
	}
	m.pendingAdd = &pendingProviderAdd{name: m.cfg.Provider.Name, url: m.cfg.Provider.BaseURL, refresh: true}
	return m.startModelsFetch(m.cfg.Provider.BaseURL, key)
}

// modelFetchTimeout bounds a provider request from the picker.
func (m *Model) configFilePath() string { return filepath.Join(m.workingDir, "config.json") }
