package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/wislist/mini-opencode/internal/config"
)

// providerMenuItem is one row of the /provider picker.
//
// The rows are snapshotted when the menu opens rather than rebuilt per frame:
// a picker whose content shifts while the user is moving through it is how a
// keypress ends up applying the wrong provider. It also makes the menu
// testable without a live config file.
type providerMenuItem struct {
	name    string
	summary string
	active  bool
	// add marks the trailing "add a provider" row, which opens the form
	// instead of switching: adding is discoverable exactly where a user looks
	// for providers they do not have yet.
	add bool
}

// handleProvider drives /provider in the TUI. A bare command opens the picker;
// anything else goes through the same parser and state changes as the CLI, so
// the two front ends cannot drift apart.
func (m *Model) handleProvider(input string) (tea.Model, tea.Cmd) {
	if m.state != stateIdle {
		m.addBlock(errorStyle.Render("请等待当前操作结束，再切换 provider。"))
		m.refreshViewport()
		return m, nil
	}
	cmd, err := config.ParseProviderCommand(input)
	if err != nil {
		m.addBlock(errorStyle.Render(err.Error()))
		m.refreshViewport()
		return m, nil
	}
	if cmd.Kind == config.ProviderCommandList {
		m.openProviderMenu()
		return m, nil
	}
	if cmd.Kind == config.ProviderCommandAdd && cmd.Name == "" {
		// "/provider add" without arguments is the interactive path: three
		// fields, then the provider's own model list to choose from.
		m.openProviderForm()
		return m, nil
	}

	previous := m.cfg.Provider.Name
	changed, _, err := m.cfg.ApplyProviderCommand(cmd, m.configPath())
	if err != nil {
		m.addBlock(errorStyle.Render(err.Error()))
		m.refreshViewport()
		return m, nil
	}
	switch cmd.Kind {
	case config.ProviderCommandAdd:
		m.addBlock(toolArrow.Render(fmt.Sprintf("已写入 config.json：provider %s → %s（type %s）",
			m.cfg.Provider.Name, m.cfg.Provider.BaseURL, m.cfg.Provider.EffectiveType())))
		m.addBlock(dimStyle.Render("接着用 /key <api-key> 保存它的密钥，或改用 api_key_env。"))
	default:
		if !changed {
			m.addBlock(dimStyle.Render("provider 未变：" + m.cfg.Provider.Name))
			m.refreshViewport()
			return m, nil
		}
		m.addBlock(toolArrow.Render(fmt.Sprintf("已切换到 provider %s（原 %s）· 仅本次进程生效",
			m.cfg.Provider.Name, previous)))
	}
	if changed {
		m.rebuildWithNotice()
		return m, m.input.Focus()
	}
	m.refreshViewport()
	return m, nil
}

// handleModel drives /model in the TUI with the same split: a bare command
// opens the picker, an id switches directly.
func (m *Model) handleModel(input string) (tea.Model, tea.Cmd) {
	// "/model refresh" re-reads the provider's own list, which is the only way
	// to learn about models added on the provider side since the last fetch.
	if strings.TrimSpace(strings.TrimPrefix(input, "/model")) == "refresh" {
		return m.refreshModels()
	}
	if m.state != stateIdle {
		m.addBlock(errorStyle.Render("请等待当前操作结束，再切换模型。"))
		m.refreshViewport()
		return m, nil
	}
	cmd, err := config.ParseModelCommand(input)
	if err != nil {
		m.addBlock(errorStyle.Render(err.Error()))
		m.refreshViewport()
		return m, nil
	}
	if cmd.List {
		m.openModelMenu()
		return m, nil
	}

	previous := m.cfg.Provider.Model
	changed, err := m.cfg.ApplyModelCommand(cmd)
	if err != nil {
		m.addBlock(errorStyle.Render(err.Error()))
		m.refreshViewport()
		return m, nil
	}
	if !changed {
		m.addBlock(dimStyle.Render(fmt.Sprintf("模型未变：%s（provider %s）", cmd.ID, m.cfg.Provider.Name)))
		m.refreshViewport()
		return m, nil
	}
	m.addBlock(toolArrow.Render(fmt.Sprintf("已切换模型 %s（原 %s）· 仅本次进程生效%s",
		cmd.ID, previous, m.applyFetchedWindow(m.cfg.Provider.Name))))
	m.rebuildWithNotice()
	return m, m.input.Focus()
}

// openProviderMenu enters the picker with the cursor already on the active
// provider, so the common case starts where the user is.
func (m *Model) openProviderMenu() {
	m.providerMenu = m.providerMenuItems()
	m.providerCursor = m.activeProviderIndex()
	m.state = stateProviderMenu
	m.invalidateFooter()
	m.refreshViewport()
}

// providerMenuItems snapshots the catalog into display rows, with the add row
// last.
func (m *Model) providerMenuItems() []providerMenuItem {
	items := make([]providerMenuItem, 0, len(m.cfg.Providers)+1)
	for _, p := range m.cfg.Providers {
		items = append(items, providerMenuItem{
			name:    p.Name,
			summary: p.Summary(m.workingDir),
			active:  strings.EqualFold(p.Name, m.cfg.ActiveProvider),
		})
	}
	items = append(items, providerMenuItem{
		name:    "＋ 新增提供商",
		summary: "名称 / 地址 / 密钥，然后从该地址拉取模型并勾选",
		add:     true,
	})
	return items
}

func (m *Model) activeProviderIndex() int {
	for i, item := range m.providerMenu {
		if item.active {
			return i
		}
	}
	return 0
}

// openModelMenu enters the model picker. It is only reachable for a provider
// with models: echo has none, and an empty overlay would read as a bug.
func (m *Model) openModelMenu() {
	m.modelMenu = m.cfg.Provider.CatalogModels()
	if len(m.modelMenu) == 0 {
		m.addBlock(dimStyle.Render(fmt.Sprintf(
			"provider %s 没有配置模型：用 /model <id> 指定，或在 config.json 的 models 里列出。",
			m.cfg.Provider.Name)))
		m.refreshViewport()
		return
	}
	m.modelCursor = 0
	for i, model := range m.modelMenu {
		if model == m.cfg.Provider.Model {
			m.modelCursor = i
			break
		}
	}
	m.state = stateModelMenu
	m.invalidateFooter()
	m.refreshViewport()
}

// renderProviderMenu draws the picker over the footer.
func (m *Model) renderProviderMenu() string {
	w := boxWidth(m)
	inner := max(1, w-4)

	start, end := visibleMenuWindow(len(m.providerMenu), m.providerCursor, m.menuItemBudget())
	lines := []string{
		keyLabel.Render("provider:") + "  " +
			dimStyle.Render("↑↓ select · enter switch · esc cancel"+windowLabel(start, end, len(m.providerMenu))),
	}
	for i := start; i < end; i++ {
		item := m.providerMenu[i]
		marker := "  "
		name := dimStyle.Render(item.name)
		summary := dimStyle.Render(item.summary)
		if i == m.providerCursor {
			marker = "▶ "
			name = toolName.Render(item.name)
		}
		// Mark the provider in effect, so the list reads as "where am I" and
		// not only "what can I pick".
		current := " "
		if item.active {
			current = "*"
		}
		lines = append(lines, fmt.Sprintf("%s%s %s  %s", marker, current, name, summary))
	}
	lines = append(lines, dimStyle.Render("切换仅本次进程生效 · /provider add 新增（依次填名称/地址/密钥）"))

	return sessionBox.Width(w).Render(strings.Join(m.wrapMenuRows(lines, inner, 1+(end-start)), "\n"))
}

// renderModelMenu draws the model picker over the footer.
func (m *Model) renderModelMenu() string {
	w := boxWidth(m)
	inner := max(1, w-4)

	start, end := visibleMenuWindow(len(m.modelMenu), m.modelCursor, m.menuItemBudget())
	lines := []string{
		keyLabel.Render("model:") + "  " +
			dimStyle.Render(fmt.Sprintf("provider %s · ↑↓ select · enter apply · esc cancel%s",
				m.cfg.Provider.Name, windowLabel(start, end, len(m.modelMenu)))),
	}
	for i := start; i < end; i++ {
		model := m.modelMenu[i]
		marker := "  "
		name := dimStyle.Render(model)
		if i == m.modelCursor {
			marker = "▶ "
			name = toolName.Render(model)
		}
		current := " "
		if model == m.cfg.Provider.Model {
			current = "*"
		}
		lines = append(lines, fmt.Sprintf("%s%s %s", marker, current, name))
	}
	lines = append(lines, dimStyle.Render("切换仅本次进程生效 · /model <id> 直接指定 · /model refresh 从提供商重新拉取模型"))

	return sessionBox.Width(w).Render(strings.Join(m.wrapMenuRows(lines, inner, 1+(end-start)), "\n"))
}

// wrapMenuRows word-wraps every logical row and then trims the physical rows so
// the overlay fits the terminal.
//
// Wrapping before trimming is the point: wordWrap returns a multi-line string
// per entry, so trimming the logical slice would count entries rather than the
// rows they occupy, and a narrow terminal would fold the text into extra rows
// that push the box off the screen.
func (m *Model) wrapMenuRows(lines []string, inner, essential int) []string {
	var rows []string
	for _, line := range lines {
		rows = append(rows, strings.Split(wordWrap(line, inner), "\n")...)
	}
	return m.fitMenuRows(rows, min(len(rows), essential))
}

// handleProviderMenuKey drives the provider picker.
func (m *Model) handleProviderMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "shift+tab":
		if m.providerCursor > 0 {
			m.providerCursor--
		}
		m.invalidateFooter()
		m.refreshViewport()
	case "down", "j", "tab":
		if m.providerCursor < len(m.providerMenu)-1 {
			m.providerCursor++
		}
		m.invalidateFooter()
		m.refreshViewport()
	case "enter":
		if m.providerCursor < len(m.providerMenu) {
			return m.applyProviderSelection(m.providerMenu[m.providerCursor])
		}
		return m.closeProviderMenu()
	case "esc", "ctrl+c", "q":
		return m.closeProviderMenu()
	}
	return m, nil
}

// applyProviderSelection switches to the highlighted provider and leaves the
// overlay. Selecting the provider already in effect closes without rebuilding:
// the runtime is expensive to build and nothing would change.
func (m *Model) applyProviderSelection(item providerMenuItem) (tea.Model, tea.Cmd) {
	if item.add {
		m.providerMenu = nil
		m.openProviderForm()
		return m, nil
	}
	m.state = stateIdle
	m.providerMenu = nil
	m.invalidateFooter()
	if item.active {
		m.refreshViewport()
		return m, nil
	}
	if _, _, err := m.cfg.ApplyProviderCommand(
		config.ProviderCommand{Kind: config.ProviderCommandSwitch, Name: item.name}, m.configPath()); err != nil {
		m.addBlock(errorStyle.Render(err.Error()))
		m.refreshViewport()
		return m, nil
	}
	m.addBlock(toolArrow.Render(fmt.Sprintf("已切换到 provider %s · 仅本次进程生效", m.cfg.Provider.Name)))
	m.rebuildWithNotice()
	return m, m.input.Focus()
}

func (m *Model) closeProviderMenu() (tea.Model, tea.Cmd) {
	m.state = stateIdle
	m.providerMenu = nil
	m.invalidateFooter()
	m.refreshViewport()
	return m, nil
}

// handleModelMenuKey drives the model picker.
func (m *Model) handleModelMenuKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k", "shift+tab":
		if m.modelCursor > 0 {
			m.modelCursor--
		}
		m.invalidateFooter()
		m.refreshViewport()
	case "down", "j", "tab":
		if m.modelCursor < len(m.modelMenu)-1 {
			m.modelCursor++
		}
		m.invalidateFooter()
		m.refreshViewport()
	case "enter":
		if m.modelCursor < len(m.modelMenu) {
			return m.applyModelSelection(m.modelMenu[m.modelCursor])
		}
		return m.closeModelMenu()
	case "esc", "ctrl+c", "q":
		return m.closeModelMenu()
	}
	return m, nil
}

func (m *Model) applyModelSelection(id string) (tea.Model, tea.Cmd) {
	m.state = stateIdle
	m.modelMenu = nil
	m.invalidateFooter()
	if id == m.cfg.Provider.Model {
		m.refreshViewport()
		return m, nil
	}
	if _, err := m.cfg.ApplyModelCommand(config.ModelCommand{ID: id}); err != nil {
		m.addBlock(errorStyle.Render(err.Error()))
		m.refreshViewport()
		return m, nil
	}
	m.addBlock(toolArrow.Render(fmt.Sprintf("已切换模型 %s（provider %s）· 仅本次进程生效%s",
		id, m.cfg.Provider.Name, m.applyFetchedWindow(m.cfg.Provider.Name))))
	m.rebuildWithNotice()
	return m, m.input.Focus()
}

func (m *Model) closeModelMenu() (tea.Model, tea.Cmd) {
	m.state = stateIdle
	m.modelMenu = nil
	m.invalidateFooter()
	m.refreshViewport()
	return m, nil
}

// configPath is the file /provider add and /key write. It mirrors the path the
// assembly layer loads, so a change made in the TUI survives a restart.
func (m *Model) configPath() string {
	return filepath.Join(m.workingDir, "config.json")
}

// rebuildRuntime builds a runtime from the current configuration and moves the
// live conversation onto it.
//
// A provider or model switch has to rebuild — the provider is fixed at
// construction — but the transcript must not be a casualty of the switch, so
// the state is handed over through AdoptStateFrom.
func (m *Model) rebuildRuntime() error {
	if m.runtimeFactory == nil {
		return fmt.Errorf("runtime factory is not configured")
	}
	next, err := m.runtimeFactory(*m.cfg)
	if err != nil {
		return err
	}
	next.AdoptStateFrom(m.runtime)
	m.SetRuntime(next)
	return nil
}

// rebuildWithNotice rebuilds the runtime and reports a failure in the
// transcript. The old runtime keeps serving on failure, while the configured
// provider stays on the new one: that is what lets a follow-up /key store the
// key under the name the user just selected.
func (m *Model) rebuildWithNotice() {
	if err := m.rebuildRuntime(); err != nil {
		m.addBlock(errorStyle.Render("✗ " + err.Error()))
	}
	m.refreshViewport()
}
