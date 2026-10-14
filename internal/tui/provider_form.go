package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// providerFormState is either the custom three-field form (name, endpoint,
// key) or the key-only form used for a known provider.
//
// It is a value (not a pointer) so resetting the form is an assignment, and so
// a test can build one without a live Model.
type providerFormState struct {
	name  textinput.Model
	url   textinput.Model
	key   textinput.Model
	focus int
	// preset carries fields that are not edited in the compact known-provider
	// form, notably its display label and conventional API-key environment name.
	preset config.ProviderConfig
	known  bool
	// returnToSetup makes esc go back to the first-run provider picker instead
	// of closing the whole flow.
	returnToSetup bool
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
		name: field("e.g. relay"),
		url:  field("https://relay.example.com/v1"),
		key:  field("Paste API key (optional for custom providers)"),
	}
	// The key is masked: a screen full of credentials survives screenshots,
	// shoulder surfing and screen sharing far longer than it should.
	form.key.EchoMode = textinput.EchoPassword
	form.name.Focus()
	return form
}

func newKnownProviderForm(width int, provider config.ProviderConfig, returnToSetup bool) providerFormState {
	form := newProviderForm(width)
	form.preset = provider
	form.known = true
	form.returnToSetup = returnToSetup
	form.name.SetValue(provider.Name)
	form.url.SetValue(provider.BaseURL)
	form.name.Blur()
	form.url.Blur()
	form.key.Focus()
	form.focus = 2
	if provider.APIKey != "" {
		form.key.SetValue(provider.APIKey)
	}
	return form
}

// pendingProviderAdd is a provider the form has collected but not yet written.
//
// Nothing is persisted before the model selection is confirmed, so cancelling
// anywhere in the flow leaves the configuration exactly as it was.
type pendingProviderAdd struct {
	provider config.ProviderConfig
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

func (m *Model) openKnownProviderForm(provider config.ProviderConfig, returnToSetup bool) {
	m.providerForm = newKnownProviderForm(m.providerFormWidth(), provider, returnToSetup)
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

func (m *Model) providerFormFieldIndexes() []int {
	if m.providerForm.known {
		return []int{2}
	}
	return []int{0, 1, 2}
}

func (m *Model) moveProviderFormFocus(delta int) {
	indexes := m.providerFormFieldIndexes()
	position := 0
	for i, index := range indexes {
		if index == m.providerForm.focus {
			position = i
			break
		}
	}
	position = ((position+delta)%len(indexes) + len(indexes)) % len(indexes)
	m.focusProviderFormField(indexes[position])
}

// focusProviderFormField moves focus, wrapping at both ends so tab and
// shift+tab always land somewhere.
func (m *Model) focusProviderFormField(index int) {
	index = ((index % providerFormFields) + providerFormFields) % providerFormFields
	if m.providerForm.known {
		index = 2
	}
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
	returnToSetup := m.providerForm.returnToSetup
	m.state = stateIdle
	m.pendingAdd = nil
	m.providerForm = providerFormState{}
	if returnToSetup {
		m.openProviderSetup()
		return m, nil
	}
	m.invalidateFooter()
	m.refreshViewport()
	return m, m.input.Focus()
}

// handleProviderFormKey drives the form.
func (m *Model) handleProviderFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "down", "ctrl+n":
		m.moveProviderFormFocus(1)
		return m, nil
	case "shift+tab", "up", "ctrl+p":
		m.moveProviderFormFocus(-1)
		return m, nil
	case "ctrl+e":
		if m.providerForm.known {
			m.providerForm.known = false
			m.focusProviderFormField(1)
			m.invalidateFooter()
			m.refreshViewport()
		}
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

// submitProviderForm validates what was typed and either saves a custom
// keyless endpoint or asks the provider which models it serves. Known remote
// providers require a typed key or their conventional environment variable.
func (m *Model) submitProviderForm() (tea.Model, tea.Cmd) {
	entry := m.providerForm.preset
	entry.Name = strings.TrimSpace(m.providerForm.name.Value())
	entry.BaseURL = strings.TrimSpace(m.providerForm.url.Value())
	key := strings.TrimSpace(m.providerForm.key.Value())
	if key != "" {
		entry.APIKey = key
	}
	if err := entry.Validate(); err != nil {
		m.providerForm.err = err.Error()
		m.invalidateFooter()
		m.refreshViewport()
		return m, nil
	}
	m.pendingAdd = &pendingProviderAdd{provider: entry}
	m.providerForm.err = ""

	resolvedKey := entry.ResolvedAPIKey()
	if resolvedKey == "" && m.providerForm.known {
		envHint := ""
		if entry.APIKeyEnv != "" {
			envHint = ", or set " + entry.APIKeyEnv
		}
		m.pendingAdd = nil
		m.providerForm.err = "API key is required" + envHint
		m.invalidateFooter()
		m.refreshViewport()
		return m, nil
	}
	if resolvedKey == "" {
		if !m.savePendingProvider(nil) {
			return m, nil
		}
		m.providerForm.returnToSetup = false
		return m.closeProviderForm()
	}
	return m.startModelsFetch(entry.BaseURL, resolvedKey)
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
		m.providerForm.err = "Failed to fetch models: " + msg.err.Error()
		m.invalidateFooter()
		m.refreshViewport()
		return m, nil
	}
	if len(msg.models) == 0 {
		m.state = stateProviderForm
		if !m.savePendingProvider(nil) {
			m.invalidateFooter()
			m.refreshViewport()
			return m, nil
		}
		m.providerForm.returnToSetup = false
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

	if m.providerForm.known {
		label := m.providerForm.preset.DisplayName()
		if label == "" {
			label = m.providerForm.name.Value()
		}
		lines := []string{
			keyLabel.Render("Connect to "+label+":") + "  " + dimStyle.Render("enter verify · esc back · ctrl+e advanced edit"),
			dimStyle.Render("API URL  ") + m.providerForm.url.Value(),
			"▶ " + toolName.Render("API Key") + "  " + m.providerForm.key.View(),
		}
		if m.providerForm.err != "" {
			lines = append(lines, errorStyle.Render("! "+m.providerForm.err))
		} else if env := strings.TrimSpace(m.providerForm.preset.APIKeyEnv); env != "" {
			lines = append(lines, dimStyle.Render("Leave blank to use "+env+"; global config is written only after verification and model selection."))
		} else {
			lines = append(lines, dimStyle.Render("Global ~/.mini-opencode/config.json is written only after verification and model selection."))
		}
		return sessionBox.Width(w).Render(strings.Join(m.wrapMenuRows(lines, inner, 3), "\n"))
	}

	labels := []string{"Name", "API URL", "API Key"}
	lines := []string{
		keyLabel.Render("Add provider:") + "  " +
			dimStyle.Render("tab/↑↓ move · enter submit · esc cancel"),
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
		lines = append(lines, dimStyle.Render("Submitting requests /models from this URL, then lets you select available models."))
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
	if w < 32 {
		return sessionBox.Width(w).Render(keyLabel.Render("Fetching · esc"))
	}
	name := ""
	if m.pendingAdd != nil {
		name = m.pendingAdd.provider.Name + "  "
	}
	content := keyLabel.Render("Fetch models:") + "  " +
		dimStyle.Render(name+"requesting "+providerFetchURL(m.pendingAdd)+" · esc cancel")
	return sessionBox.Width(w).Render(content)
}

func providerFetchURL(pending *pendingProviderAdd) string {
	if pending == nil {
		return "(current provider)"
	}
	return strings.TrimRight(pending.provider.BaseURL, "/") + "/models"
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
		keyLabel.Render("Select models:") + "  " + dimStyle.Render(fmt.Sprintf(
			"space toggle · a select/clear all · enter confirm (%d/%d) · esc cancel%s",
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
		lines = append(lines, dimStyle.Render("Selected models are saved to global ~/.mini-opencode/config.json; the first selected model becomes active."))
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
		m.modelSelectErr = "Select at least one model (press a to select all)"
		m.invalidateFooter()
		m.refreshViewport()
		return m, nil
	}
	if !m.savePendingProvider(selected) {
		return m, nil
	}
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
// The complete provider entry is staged in a copy and written to the global
// config before it replaces live state. A failed write leaves memory and disk
// on the same catalog.
func (m *Model) savePendingProvider(models []string) bool {
	pending := m.pendingAdd
	if pending == nil {
		return false
	}
	path, err := config.GlobalConfigPath()
	if err != nil {
		m.addBlock(errorStyle.Render("✗ Failed to locate global config.json: " + err.Error()))
		m.refreshViewport()
		return false
	}
	next := *m.cfg
	next.Providers = slices.Clone(m.cfg.Providers)
	if pending.refresh {
		if _, err := next.SetProviderModels(pending.provider.Name, models, ""); err != nil {
			m.addBlock(errorStyle.Render("✗ " + err.Error()))
			m.refreshViewport()
			return false
		}
		windowNote := m.applyFetchedWindowTo(&next, pending.provider.Name)
		if err := config.SaveProviderCatalog(path, next); err != nil {
			m.addBlock(errorStyle.Render("✗ Failed to write global ~/.mini-opencode/config.json: " + err.Error()))
			m.refreshViewport()
			return false
		}
		*m.cfg = next
		m.addBlock(toolArrow.Render(fmt.Sprintf("Updated provider %s models (%d) · active %s%s",
			pending.provider.Name, len(m.cfg.Provider.CatalogModels()), m.cfg.Provider.Model, windowNote)))
		m.rebuildWithNotice()
		return true
	}

	entry := pending.provider
	if err := next.AddProvider(entry); err != nil {
		m.addBlock(errorStyle.Render("✗ " + err.Error()))
		m.refreshViewport()
		return false
	}
	if _, err := next.SetProviderModels(entry.Name, models, ""); err != nil {
		m.addBlock(errorStyle.Render("✗ " + err.Error()))
		m.refreshViewport()
		return false
	}
	windowNote := m.applyFetchedWindowTo(&next, entry.Name)
	if err := config.SaveProviderCatalog(path, next); err != nil {
		m.addBlock(errorStyle.Render("✗ Failed to write global ~/.mini-opencode/config.json: " + err.Error()))
		m.refreshViewport()
		return false
	}
	*m.cfg = next

	switch {
	case entry.ResolvedAPIKey() == "" && len(models) == 0:
		m.addBlock(toolArrow.Render(fmt.Sprintf("Added provider %s → %s (verified and saved to global ~/.mini-opencode/config.json)",
			entry.Name, entry.BaseURL)))
		m.addBlock(dimStyle.Render("No models were fetched because no key was provided. Set api_key_env, or update the same provider with /provider add and enter the key in the third field, then run /model refresh."))
	case len(models) == 0:
		m.addBlock(toolArrow.Render(fmt.Sprintf("Added provider %s → %s (verified and saved to global ~/.mini-opencode/config.json)",
			entry.Name, entry.BaseURL)))
		m.addBlock(dimStyle.Render("This endpoint returned no models. Use /model <id>, or list models in global ~/.mini-opencode/config.json."))
	default:
		m.addBlock(toolArrow.Render(fmt.Sprintf("Added provider %s → %s, %d models, active %s%s",
			entry.Name, entry.BaseURL, len(m.cfg.Provider.CatalogModels()), m.cfg.Provider.Model, windowNote)))
	}
	m.rebuildWithNotice()
	return true
}

// applyFetchedWindow records the context length the provider reported for the
// model that just became active, and returns a note for the transcript.
//
// A provider that reports nothing leaves the configured value alone: clearing a
// hand-set window because a listing omitted the field would silently undo the
// user's own configuration.
func (m *Model) applyFetchedWindow(providerName string) string {
	return m.applyFetchedWindowTo(m.cfg, providerName)
}

func (m *Model) applyFetchedWindowTo(cfg *config.Config, providerName string) string {
	model := cfg.Provider.Model
	if model == "" {
		return ""
	}
	window := m.modelWindows[model]
	if window <= 0 {
		return ""
	}
	changed, err := cfg.SetProviderContextWindow(providerName, window)
	if err != nil || !changed {
		return ""
	}
	return fmt.Sprintf(" · window %s (from /models)", config.FormatContextWindow(window))
}

// refreshModels re-fetches the model list for the active provider.
func (m *Model) refreshModels() (tea.Model, tea.Cmd) {
	if m.state != stateIdle {
		m.addBlock(errorStyle.Render("Wait for the current operation to finish before fetching models."))
		m.refreshViewport()
		return m, nil
	}
	if m.cfg.Provider.EffectiveType() == config.ProviderTypeEcho {
		m.addBlock(dimStyle.Render(fmt.Sprintf(
			"Provider %q is local echo and has no model catalog to fetch.", m.cfg.Provider.Name)))
		m.refreshViewport()
		return m, nil
	}
	key := m.cfg.Provider.ResolvedAPIKey()
	if key == "" {
		// The env var may be unset, so name the field rather than an empty
		// string: "set " with nothing after it reads as a bug.
		m.addBlock(errorStyle.Render(fmt.Sprintf(
			"Provider %q has no usable API key. Update the same provider with /provider add and enter the key in the third field, or set api_key / api_key_env in global ~/.mini-opencode/config.json.",
			m.cfg.Provider.Name)))
		m.refreshViewport()
		return m, nil
	}
	m.pendingAdd = &pendingProviderAdd{provider: m.cfg.Provider, refresh: true}
	return m.startModelsFetch(m.cfg.Provider.BaseURL, key)
}

// modelFetchTimeout bounds a provider request from the picker.
func (m *Model) configFilePath() string {
	path, _ := config.GlobalConfigPath()
	return path
}
