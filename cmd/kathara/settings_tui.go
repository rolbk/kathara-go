// The shape is consolemenu's, because that is what the screen's users know: a
// list of items, each showing its current value, and selecting one opens its
// own page — a menu of the legal answers, or a prompt. What is *not* carried
// over is the vendored library, the curses dependency, and the per-item
// re-implementation of the same three validators.
// The model is pure — [settingsModel.Update] is a function of the message and
// the model — so the whole screen is testable headlessly, which is what
// settings_tui_test.go does.

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/settings"
)

// settingsChoice is one `FunctionItem` of a submenu: the text the user picks
// and the word it hands to [settings.Settings.SetString].
type settingsChoice struct {
	label string
	value string
	// extra are the further assignments the menu item performed. Exactly one
	// item had any: "Reset remote Docker connection to default", which cleared
	// `remote_url` *and* `cert_path` (`DockerOptionsHandler.py:252`).
	extra []settingsAssign
}

// settingsAssign is one of those further assignments.
type settingsAssign struct{ key, value string }

// settingsField is one row of the top-level list: a key, the menu item's own
// text, its prologue, and how it is answered.
type settingsField struct {
	key   string
	title string
	help  string
	// choices is the submenu. Empty means the item went straight to a prompt.
	choices []settingsChoice
	// freeText adds the "Choose another …" escape the menus offered next to
	// their suggestions.
	freeText bool
	// prompt is the prompt line, i.e. `update_value`'s `prompt_msg`.
	prompt string
	// prefill is the prompt's `default_value`. Empty means "the current
	// value", which is a form convenience: consolemenu offered no default at
	// all except on `docker_config_json`, so pressing Enter there failed the
	// regex and re-prompted. Here it is a no-op re-set.
	prefill string
}

// isPath reports whether the answer to this field is a *path to read* rather
// than the value to store. Exactly one key is (see [settings.EncodeDockerConfigJSON]).
func (f settingsField) isPath() bool { return f.key == dockerConfigJSONKey }

// yesNo is the `Yes`/`No` submenu every bool key had.
var yesNo = []settingsChoice{{label: "Yes", value: "true"}, {label: "No", value: "false"}}

// resetToNull is the "Reset value to Empty String" item. The label says
// "Empty String" while the stored value is `None`; preserve the visible label
// for compatibility.
var resetToNull = settingsChoice{label: "Reset value to Empty String", value: ""}

// shellHints is `SHELLS_HINT` (`CommonOptionsHandler.py:14`).
var shellHints = []string{"/bin/bash", "/bin/sh", "/bin/ash", "/bin/ksh", "/bin/zsh", "/bin/fish", "/bin/csh", "/bin/tcsh"}

// terminalHints is the per-platform terminal submenu: xterm and
// gnome-terminal on Linux, `TERMINALS_OSX` on macOS, and TMUX everywhere.
func terminalHints() []string {
	names := util.ExecByPlatform(
		func() []string { return []string{"/usr/bin/xterm", "/usr/bin/gnome-terminal"} },
		func() []string { return nil },
		func() []string { return []string{"Terminal", "iTerm"} },
	)
	return append(names, "TMUX")
}

// literalChoices turns a list of values into a submenu whose labels are the
// values, which is how most of the `settings` menus are spelled.
func literalChoices(values []string) []settingsChoice {
	return labelledChoices(values, nil)
}

// labelledChoices is [literalChoices] with a label for the values the menu
// spelled differently from what it stored. The vocabulary still comes from
// `settings`, so a value that gains no label is offered under its own name
// rather than dropped from the submenu.
func labelledChoices(values []string, labels map[string]string) []settingsChoice {
	out := make([]settingsChoice, 0, len(values))
	for _, v := range values {
		label := v
		if spelled, ok := labels[v]; ok {
			label = spelled
		}
		out = append(out, settingsChoice{label: label, value: v})
	}
	return out
}

func settingsFields(s *settings.Settings, managers []settingsChoice) []settingsField {
	fields := []settingsField{
		{
			key: "manager_type", title: "Choose default manager",
			help:    "Manager is the Engine used to run Kathara labs.",
			choices: managers,
		},
		{
			key: "image", title: "Choose default image",

			help:     "Default Docker image when you start a network scenario or a single Kathara device.",
			freeText: true,
			prompt:   "Write the name of a Docker image available on Docker Hub:",
		},
		{
			key: "open_terminals", title: "Automatically open terminals on startup",
			help:    "Determines if the device terminal should be opened when starting it.",
			choices: yesNo,
		},
		{
			key: "device_shell", title: "Choose device shell to be used",
			help: "The shell to use inside the device. " +
				"The application must be correctly installed in the Docker image used for the device!",
			choices: literalChoices(shellHints), freeText: true,
			prompt: "Write the name of a shell:",
		},
		{
			key: "terminal", title: "Choose terminal emulator to be used",
			help: "Terminal emulator application to be used for device terminals. " +
				"The application must be correctly installed in the host system!",

			choices: literalChoices(terminalHints()), freeText: true,
			prompt: "Write the path of a terminal emulator:",
		},
		{
			key: "net_prefix", title: "Insert Kathara networks prefix",
			help:     "Prefix assigned to the network names when deployed.",
			freeText: true,
			prompt:   "Write a Kathara networks prefix:",
		},
		{
			key: "device_prefix", title: "Insert Kathara devices prefix",
			help:     "Prefix assigned to the device names when deployed.",
			freeText: true,
			prompt:   "Write a Kathara devices prefix:",
		},
		{
			key: "debug_level", title: "Choose logging level to be used",
			help:    "Logging level of Kathara messages.",
			choices: literalChoices(settings.AvailableDebugLevels()),
		},
		{
			key: "print_startup_log", title: "Print Startup Logs on device startup",
			help:    "When opening a device terminal, print its startup log.",
			choices: yesNo,
		},
		{
			key: "enable_ipv6", title: "Enable IPv6",
			help:    "This option enables IPv6 inside the devices.",
			choices: yesNo,
		},
		{
			key: "volume_mount_policy", title: "Device Volume Mount Policy",
			help:    "Choose the policy to apply when a device is trying to mount a volume from the host.",
			choices: literalChoices(settings.AvailableVolumeMountPolicies()),
		},
	}

	switch strings.ToLower(s.ManagerType) {
	case "kubernetes":
		fields = append(fields, kubernetesFields()...)
	default:
		fields = append(fields, dockerFields()...)
	}
	return fields
}

// dockerFields is `DockerOptionsHandler.add_items`, in its append order.
func dockerFields() []settingsField {
	return []settingsField{
		{
			key: "network_plugin", title: "Choose Docker Network Plugin version",
			help: "`kathara/katharanp` is based on Linux bridges; " +
				"`kathara/katharanp_vde` is based on VDE switches.",
			choices: literalChoices(settings.AvailableNetworkPlugins()),
		},
		{
			key: "hosthome_mount", title: "Automatically mount /hosthome on startup",
			help: "The home directory of the current user is made available for reading/writing " +
				"inside the device under the special directory `/hosthome`.",
			choices: yesNo,
		},
		{
			key: "shared_mount", title: "Automatically mount /shared on startup",
			help: "The shared directory inside the network scenario folder is made available for " +
				"reading/writing inside the device under the special directory `/shared`.",
			choices: yesNo,
		},
		{
			key: "image_update_policy", title: "Docker Image Update Policy",
			help:    "Choose the policy when a Docker image update is available for a running device.",
			choices: literalChoices(settings.AvailableImageUpdatePolicies()),
		},
		{
			key: "shared_cds", title: "Enable Shared Collision Domains",
			help: "This option allows sharing collision domains between network scenarios and users.",
			// The menu's own order, which is not the enum's: labs, users, then
			// not-shared (`DockerOptionsHandler.py:196-224`).
			choices: []settingsChoice{
				{label: settings.SharedBetweenLabs.ToString(), value: strconv.Itoa(int(settings.SharedBetweenLabs))},
				{label: settings.SharedBetweenUsers.ToString(), value: strconv.Itoa(int(settings.SharedBetweenUsers))},
				{label: "Do not share collision domains", value: strconv.Itoa(int(settings.NotShared))},
			},
		},
		{
			key: "remote_url", title: "Configure a remote Docker connection",
			help:     "You can specify a remote Docker Daemon URL.",
			freeText: true,
			prompt:   "Write a Docker Daemon URL (format http[s]://<remote-url>:<remote-port>):",
			choices: []settingsChoice{{
				label: "Reset remote Docker connection to default", value: "",
				extra: []settingsAssign{{key: "cert_path", value: ""}},
			}},
		},
		{
			key: "cert_path", title: "Configure a Docker Daemon TLS Cert Path",
			help:     "When using a remote Docker Daemon, a TLS Cert could be required.",
			freeText: true,
			prompt:   "Write a TLS Cert Path:",
			choices:  []settingsChoice{resetToNull},
		},
	}
}

// kubernetesFields is `KubernetesOptionsHandler.add_items`, in its append order.
func kubernetesFields() []settingsField {
	return []settingsField{
		{
			key: "api_server_url", title: "Insert a Kubernetes API Server URL",
			help: "You can specify a remote Kubernetes API Server URL to connect to when " +
				"Megalos is not used on a Kubernetes master.",
			freeText: true,
			prompt:   "Write a Kubernetes API Server URL:",
			choices:  []settingsChoice{resetToNull},
		},
		{
			key: "api_token", title: "Insert a Kubernetes API Token",
			help: "When using a remote Kubernetes API Server, you must also specify the " +
				"authentication token to use.",
			freeText: true,
			prompt:   "Write a Kubernetes API Token:",
			choices:  []settingsChoice{resetToNull},
		},
		{
			key: "host_shared", title: "Automatically mount /shared on startup",
			help: "Each Kubernetes worker node creates a /home/shared directory and it is made " +
				"available for reading/writing inside the device under the special directory `/shared`.",
			choices: yesNo,
		},
		{
			key: "image_pull_policy", title: "Image Pull Policy",
			help: "Specify the image pull policy for Docker images used by devices.",
			// The middle row is the one menu whose label is not its value:
			// Python spelled it "If Not Present" and stored `IfNotPresent`
			// (`KubernetesOptionsHandler.py:142-149`).
			choices: labelledChoices(settings.AvailableImagePullPolicies(),
				map[string]string{"IfNotPresent": "If Not Present"}),
		},
		{
			key: "docker_config_json", title: "Configure a Docker Private Registry",
			help: "Insert the config.json file path containing the configuration to access private " +
				"container registries. The content is stored as a base64 string and is used to create " +
				"a Kubernetes Secret of type `kubernetes.io/dockerconfigjson`.",
			freeText: true,
			prompt:   "Write the path to the Docker Config JSON file:",
			prefill:  settings.DefaultDockerConfigJSONPath,
			choices:  []settingsChoice{resetToNull},
		},
	}
}

// settingsMode is which page is on screen.
type settingsMode int

const (
	// settingsBrowse is the top-level item list.
	settingsBrowse settingsMode = iota
	// settingsChoosing is one item's submenu of legal answers.
	settingsChoosing
	// settingsEditing is one item's prompt.
	settingsEditing
	// settingsConfirmQuit is the unsaved-changes question.
	settingsConfirmQuit
)

// settingsModel is the form.
type settingsModel struct {
	cfg      *settings.Settings
	managers []settingsChoice
	fields   []settingsField
	save     func(*settings.Settings) error

	mode   settingsMode
	cursor int
	choice int
	input  textinput.Model

	// status is the line under the list: a rejection from `settings`, or a
	// notice. failed says which, so the two can be styled apart.
	status string
	failed bool
	// dirty is "edited since the last save", which is what makes quitting ask.
	dirty bool
	// err is a failure the command itself must report — a save that could not
	// write the file — rather than something the user can correct in the form.
	err error

	width int
}

// newSettingsModel builds the form over cfg, which it owns and mutates. save is
// what "s" calls; it is a parameter so a test can drive the whole screen
// without a filesystem.
func newSettingsModel(cfg *settings.Settings, managers []settingsChoice, save func(*settings.Settings) error) *settingsModel {
	input := textinput.New()
	input.Prompt = "> "
	input.CharLimit = 0

	return &settingsModel{
		cfg:      cfg,
		managers: managers,
		fields:   settingsFields(cfg, managers),
		save:     save,
		input:    input,
		width:    cliWidthFallback,
	}
}

// cliWidthFallback is rich's non-TTY console width, reused so the form renders
// identically before the first [tea.WindowSizeMsg] arrives.
const cliWidthFallback = 80

// Init starts nothing: the form has no I/O of its own.
func (m *settingsModel) Init() tea.Cmd { return nil }

// Update is the whole of the form's behaviour.
func (m *settingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// A pty with no window size set reports zero, and bubbletea forwards
		// it. Keeping the fallback there is what stops the list from
		// collapsing into a column of ellipses.
		if msg.Width > 0 {
			m.width = msg.Width
		}
		return m, nil
	case tea.KeyMsg:
		// Ctrl-C leaves at once and saves nothing, on every page. It is the one
		// key that must not be captured by a text field.
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		// bubbletea folds a burst of printable bytes that arrive in one read
		// into a single KeyRunes message — a paste, or the autorepeat of a
		// held-down `j`. The text field wants that whole; the two menus want
		// one keystroke per rune, or holding a key down does nothing at all.
		if m.mode != settingsEditing && msg.Type == tea.KeyRunes && len(msg.Runes) > 1 {
			return m.replayRunes(msg.Runes)
		}
		switch m.mode {
		case settingsBrowse:
			return m.updateBrowse(msg)
		case settingsChoosing:
			return m.updateChoosing(msg)
		case settingsEditing:
			return m.updateEditing(msg)
		case settingsConfirmQuit:
			return m.updateConfirmQuit(msg)
		}
	}
	return m, nil
}

// replayRunes feeds a coalesced burst back through Update one rune at a time,
// stopping at the first one that returns a command: a quit, or the cursor
// kickoff of a prompt that rune has just opened. Either way the rest of the
// burst was typed at a screen that is no longer on.
func (m *settingsModel) replayRunes(runes []rune) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, r := range runes {
		_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if cmd != nil {
			break
		}
	}
	return m, cmd
}

func (m *settingsModel) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "enter", " ":
		return m, m.open()
	case "s":
		m.doSave()
	case "q", "esc":
		if m.dirty {
			m.mode = settingsConfirmQuit
			return m, nil
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m *settingsModel) updateChoosing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	field := m.fields[m.cursor]
	options := m.options(field)

	switch msg.String() {
	case "up", "k":
		if m.choice > 0 {
			m.choice--
		}
	case "down", "j":
		if m.choice < len(options)-1 {
			m.choice++
		}
	case "esc", "q":
		m.mode = settingsBrowse
	case "enter", " ":
		if len(options) == 0 {
			m.mode = settingsBrowse
			return m, nil
		}
		selected := options[m.choice]
		if selected.value == freeTextSentinel {
			return m, m.startEditing(field)
		}
		// A rejected answer keeps the submenu open, which is what consolemenu's
		// re-prompt loop did with a failing validator.
		if m.apply(field, selected) {
			m.mode = settingsBrowse
		}
	}
	return m, nil
}

func (m *settingsModel) updateEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	field := m.fields[m.cursor]

	switch msg.Type {
	case tea.KeyEsc:
		m.mode = settingsBrowse
		m.input.Blur()
		return m, nil
	case tea.KeyEnter:
		if m.apply(field, settingsChoice{value: m.input.Value()}) {
			m.mode = settingsBrowse
			m.input.Blur()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *settingsModel) updateConfirmQuit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if !m.doSave() {
			// The file could not be written. Going back to the list rather than
			// quitting is the only way the user keeps the edits at all.
			m.mode = settingsBrowse
			return m, nil
		}
		return m, tea.Quit
	case "n", "N":
		return m, tea.Quit
	case "esc":
		m.mode = settingsBrowse
	}
	return m, nil
}

// move walks the item list, clamped at both ends — consolemenu did not wrap.
func (m *settingsModel) move(delta int) {
	next := m.cursor + delta
	if next < 0 || next >= len(m.fields) {
		return
	}
	m.cursor = next
	m.status, m.failed = "", false
}

// open enters the selected item: its submenu, or its prompt when it has none.
func (m *settingsModel) open() tea.Cmd {
	field := m.fields[m.cursor]
	m.status, m.failed = "", false

	if len(m.options(field)) == 0 {
		return m.startEditing(field)
	}
	m.mode = settingsChoosing
	m.choice = 0
	return nil
}

// freeTextSentinel marks the "Choose another …" row, which opens the prompt
// instead of writing a value. It is not a legal answer for any key — `parse`
// would take it as a literal string — and never reaches [settings.Settings.SetString].
const freeTextSentinel = "\x00free-text"

// options is the field's submenu with the free-text escape appended, which is
// where every handler put it.
func (m *settingsModel) options(field settingsField) []settingsChoice {
	options := field.choices
	if field.freeText && len(field.choices) > 0 {
		options = append(append([]settingsChoice(nil), options...),
			settingsChoice{label: "Choose another value…", value: freeTextSentinel})
	}
	return options
}

// startEditing opens the prompt, prefilled with the field's default answer or,
// where it has none, with the value already stored.
func (m *settingsModel) startEditing(field settingsField) tea.Cmd {
	m.mode = settingsEditing
	value := field.prefill
	if value == "" {
		value = editableSettingValue(m.cfg, field.key)
	}
	m.input.SetValue(value)
	m.input.CursorEnd()
	return m.input.Focus()
}

// apply writes one answer through `settings`' own validators and reports
// whether it was accepted. A rejection becomes the status line, in that
// package's words.
func (m *settingsModel) apply(field settingsField, choice settingsChoice) bool {
	value := choice.value
	if field.isPath() && value != "" {
		// The answer is a path; the value is the base64 of what it holds
		// (`store_b64_docker_json_callback`). `config set docker_config_json`
		// goes through the same call.
		encoded, err := settings.EncodeDockerConfigJSON(value)
		if err != nil {
			m.fail(err)
			return false
		}
		value = encoded
	}

	if err := m.cfg.SetString(field.key, value); err != nil {
		m.fail(err)
		return false
	}
	for _, assign := range choice.extra {
		if err := m.cfg.SetString(assign.key, assign.value); err != nil {
			m.fail(err)
			return false
		}
	}

	m.dirty = true
	m.status, m.failed = "", false
	// `manager_type` decides which addon keys exist, and setting it has already
	// reset that addon to its defaults inside `settings`. The list has to be
	// rebuilt for the same reason `update_manager_value` rebuilt the menu.
	m.fields = settingsFields(m.cfg, m.managers)
	if m.cursor >= len(m.fields) {
		m.cursor = len(m.fields) - 1
	}
	return true
}

// doSave writes the file and reports whether it got there.
func (m *settingsModel) doSave() bool {
	if err := m.save(m.cfg); err != nil {
		m.fail(err)
		m.err = err
		return false
	}
	m.dirty = false
	// `SAVED_STRING` (`cli/ui/setting/utils.py:9`).
	m.status, m.failed = "Saved successfully!", false
	m.err = nil
	return true
}

func (m *settingsModel) fail(err error) {
	m.status, m.failed = err.Error(), true
}

// displaySettingValue is the menu's own rendering of a current value:
// `format_bool` for a bool, `SharedCollisionDomainsOption.to_string` for
// `shared_cds`, and Python's `"%s" % None` — the word `None` — for a nullable
// key holding null.
func displaySettingValue(v any) string {
	switch value := v.(type) {
	case bool:
		if value {
			return "Yes"
		}
		return "No"
	case settings.SharedCollisionDomains:
		if label := value.ToString(); label != "" {
			return label
		}
		return strconv.Itoa(int(value))
	}
	return formatSettingValue(v)
}

// editableSettingValue is the value spelled as [settings.Settings.SetString]
// would take it back, which is what the prompt is prefilled with. A null
// nullable prefills empty, and an empty answer sets it back to null.
func editableSettingValue(s *settings.Settings, key string) string {
	v, err := s.Get(key)
	if err != nil {
		return ""
	}
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	case settings.SharedCollisionDomains:
		return strconv.Itoa(int(value))
	}
	return fmt.Sprint(v)
}

// The three styles the form uses. They are deliberately few: this screen is
// read over SSH on terminals whose colour support is unknown, and lipgloss
// degrades a colour it cannot render but not a layout built out of one.
var (
	settingsTitleStyle    = lipgloss.NewStyle().Bold(true)
	settingsSelectedStyle = lipgloss.NewStyle().Bold(true)
	settingsFaintStyle    = lipgloss.NewStyle().Faint(true)
	settingsErrorStyle    = lipgloss.NewStyle().Bold(true)
)

// View renders the current page.
func (m *settingsModel) View() string {
	switch m.mode {
	case settingsChoosing:
		return m.viewChoosing()
	case settingsEditing:
		return m.viewEditing()
	case settingsConfirmQuit:
		return m.viewConfirmQuit()
	}
	return m.viewBrowse()
}

func (m *settingsModel) viewBrowse() string {
	var b strings.Builder
	b.WriteString(settingsTitleStyle.Render("Kathara Settings"))
	b.WriteString("\n\n")

	width := m.titleWidth()
	for i, field := range m.fields {
		marker := "  "
		line := padRight(truncate(field.title, width), width) + "  " + m.currentValue(field)
		if i == m.cursor {
			marker = "> "
			line = settingsSelectedStyle.Render(line)
		}
		b.WriteString(marker)
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString(settingsFaintStyle.Render("↑/↓ move • enter edit • s save • q quit"))
	if m.dirty {
		b.WriteString(settingsFaintStyle.Render("  •  unsaved changes"))
	}
	return b.String()
}

func (m *settingsModel) viewChoosing() string {
	field := m.fields[m.cursor]

	var b strings.Builder
	b.WriteString(settingsTitleStyle.Render(field.title))
	b.WriteString("\n")
	b.WriteString(settingsFaintStyle.Render("Current: " + m.currentValue(field)))
	b.WriteString("\n\n")
	if field.help != "" {
		b.WriteString(faintBlock(fillText(field.help, m.bodyWidth())))
		b.WriteString("\n\n")
	}

	for i, option := range m.options(field) {
		marker := "  "
		label := option.label
		if i == m.choice {
			marker = "> "
			label = settingsSelectedStyle.Render(label)
		}
		b.WriteString(marker)
		b.WriteString(label)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString(settingsFaintStyle.Render("↑/↓ move • enter select • esc back"))
	return b.String()
}

func (m *settingsModel) viewEditing() string {
	field := m.fields[m.cursor]

	var b strings.Builder
	b.WriteString(settingsTitleStyle.Render(field.title))
	b.WriteString("\n")
	b.WriteString(settingsFaintStyle.Render("Current: " + m.currentValue(field)))
	b.WriteString("\n\n")
	if field.prompt != "" {
		b.WriteString(fillText(field.prompt, m.bodyWidth()))
		b.WriteString("\n")
	}
	b.WriteString(m.input.View())
	b.WriteString("\n\n")
	b.WriteString(m.statusLine())
	b.WriteString(settingsFaintStyle.Render("enter confirm • esc cancel"))
	return b.String()
}

func (m *settingsModel) viewConfirmQuit() string {
	return settingsTitleStyle.Render("Kathara Settings") + "\n\n" +
		"You have unsaved changes. Save them before quitting? [y/n]\n\n" +
		settingsFaintStyle.Render("y save and quit • n discard and quit • esc keep editing")
}

// currentValue renders one field's value, truncated to what is left of the
// line: `docker_config_json` holds a base64 blob that would otherwise wrap the
// list into unreadability.
func (m *settingsModel) currentValue(field settingsField) string {
	value, err := m.cfg.Get(field.key)
	if err != nil {
		return ""
	}
	return truncate(displaySettingValue(value), m.valueWidth())
}

// valueWidth is what is left of the line after the marker and the item-name
// column. It never goes below twelve columns: on a narrow terminal it is better
// to clip the item names than to reduce every value to an ellipsis.
func (m *settingsModel) valueWidth() int {
	width := m.bodyWidth() - m.titleWidth() - 4
	if width < 12 {
		return 12
	}
	return width
}

func (m *settingsModel) statusLine() string {
	if m.status == "" {
		return ""
	}
	if m.failed {
		return settingsErrorStyle.Render(m.status) + "\n\n"
	}
	return m.status + "\n\n"
}

// titleWidth is the item-name column: the longest title, so the values line up,
// capped at half the terminal so a narrow window still leaves room for a value.
func (m *settingsModel) titleWidth() int {
	width := 0
	for _, field := range m.fields {
		if n := lipgloss.Width(field.title); n > width {
			width = n
		}
	}
	if half := m.bodyWidth() / 2; width > half {
		return half
	}
	return width
}

func (m *settingsModel) bodyWidth() int {
	if m.width < 40 {
		return 40
	}
	return m.width - 2
}

// faintBlock renders a multi-line paragraph faint one line at a time. A
// lipgloss style applied to the whole block pads every line out to the widest
// one, which puts trailing spaces into the frame and makes a terminal-copied
// paragraph ragged.
func faintBlock(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = settingsFaintStyle.Render(line)
	}
	return strings.Join(lines, "\n")
}

// padRight pads to width in *display cells*. `fmt`'s `%-*s` pads by bytes,
// which drifts by two columns on any row whose title carries the one-rune,
// three-byte ellipsis [truncate] appends.
func padRight(s string, width int) string {
	if n := lipgloss.Width(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// truncate cuts a value to width, marking the cut. It counts runes, not bytes.
func truncate(s string, width int) string {
	if width < 4 {
		width = 4
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}
