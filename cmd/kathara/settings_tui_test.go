package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KatharaFramework/kathara-go/settings"
)

// A bubbletea model is a pure function of its messages, so the whole screen is
// driven here as key events with no terminal, no PTY and no goroutine. These
// are the tests PORT_SPEC §3.2 item 3 could not have had against consolemenu,
// which read the keyboard through `curses` from inside its own event loop.

// keyMsg builds one key event from the name [tea.Key.String] would print.
func keyMsg(name string) tea.KeyMsg {
	switch name {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// isQuit reports whether Update asked bubbletea to leave. The command is
// compared by identity rather than invoked, because a text field can return a
// cursor-blink command whose call would block on a timer.
func isQuit(cmd tea.Cmd) bool {
	return cmd != nil && reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Quit).Pointer()
}

// press feeds a sequence of key names and reports whether the last of them
// quit.
func press(t *testing.T, m *settingsModel, names ...string) bool {
	t.Helper()
	var cmd tea.Cmd
	for _, name := range names {
		_, cmd = m.Update(keyMsg(name))
	}
	return isQuit(cmd)
}

// saveRecorder counts the saves and can be told to fail.
type saveRecorder struct {
	calls int
	last  settings.Settings
	err   error
}

func (r *saveRecorder) save(s *settings.Settings) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	r.last = *s
	return nil
}

// newTestForm builds a form over fresh defaults.
func newTestForm(t *testing.T) (*settingsModel, *settings.Settings, *saveRecorder) {
	t.Helper()
	cfg := settings.Defaults()
	rec := &saveRecorder{}
	return newSettingsModel(cfg, managerChoices(), rec.save), cfg, rec
}

// focus moves the cursor onto the named key and fails if the form has no such
// row.
func focus(t *testing.T, m *settingsModel, key string) {
	t.Helper()
	for i, field := range m.fields {
		if field.key == key {
			m.cursor = i
			return
		}
	}
	t.Fatalf("the form has no row for %q", key)
}

// chooseByLabel selects the submenu row with the given label.
func chooseByLabel(t *testing.T, m *settingsModel, label string) {
	t.Helper()
	for i, option := range m.options(m.fields[m.cursor]) {
		if option.label == label {
			m.choice = i
			return
		}
	}
	t.Fatalf("no submenu item labelled %q", label)
}

// managerLabel is the `manager_type` submenu's text for one backend, which
// depends on the build: a `nok8s` binary lists `kubernetes` under its bare name
// because it carries no registry entry to take a formatted name from.
func managerLabel(t *testing.T, name string) string {
	t.Helper()
	for _, choice := range managerChoices() {
		if choice.value == name {
			return choice.label
		}
	}
	t.Fatalf("the manager menu does not offer %q", name)
	return ""
}

// TestSettingsFormCoversEveryMenuKey is the §3.2 item 3 requirement, checked
// key by key: the form edits exactly the keys the three Python handlers built
// items for.
//
// `last_checked` is deliberately absent from both lists. It is not a setting a
// user sets — it is the update-check bookkeeping stamp — and
// `CommonOptionsHandler` built no item for it. `kathara config set last_checked`
// still writes it, which is the same asymmetry Python had between its file and
// its screen.
func TestSettingsFormCoversEveryMenuKey(t *testing.T) {
	common := []string{
		"manager_type", "image", "open_terminals", "device_shell", "terminal",
		"net_prefix", "device_prefix", "debug_level", "print_startup_log",
		"enable_ipv6", "volume_mount_policy",
	}

	cases := []struct {
		manager string
		want    []string
	}{
		{
			manager: "docker",
			want: append(append([]string(nil), common...),
				"network_plugin", "hosthome_mount", "shared_mount",
				"image_update_policy", "shared_cds", "remote_url", "cert_path"),
		},
		{
			manager: "kubernetes",
			want: append(append([]string(nil), common...),
				"api_server_url", "api_token", "host_shared",
				"image_pull_policy", "docker_config_json"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.manager, func(t *testing.T) {
			cfg := settings.Defaults()
			if err := cfg.Set("manager_type", tc.manager); err != nil {
				t.Fatal(err)
			}
			m := newSettingsModel(cfg, managerChoices(), func(*settings.Settings) error { return nil })

			got := make([]string, 0, len(m.fields))
			for _, field := range m.fields {
				got = append(got, field.key)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("fields\n got: %v\nwant: %v", got, tc.want)
			}

			// Every row must name a key of the live schema, which is what makes
			// "the form and `config list` cannot disagree" checkable.
			for _, field := range m.fields {
				if _, err := cfg.Get(field.key); err != nil {
					t.Errorf("row %q is not a key of the %s schema: %v", field.key, tc.manager, err)
				}
				if field.title == "" {
					t.Errorf("row %q has no title", field.key)
				}
			}
		})
	}
}

// TestSettingsFormChoicesAreAllAccepted is §3.2 item 4 as an invariant: not one
// answer the form offers can be rejected by `settings`, because both go through
// the same [settings.Settings.SetString].
//
// `terminal` is excluded because its validator touches the filesystem —
// `/usr/bin/xterm` is a legal answer only where xterm is installed, and that is
// Python's behaviour too (`TerminalValidator`).
func TestSettingsFormChoicesAreAllAccepted(t *testing.T) {
	for _, manager := range []string{"docker", "kubernetes"} {
		cfg := settings.Defaults()
		if err := cfg.Set("manager_type", manager); err != nil {
			t.Fatal(err)
		}
		m := newSettingsModel(cfg, managerChoices(), func(*settings.Settings) error { return nil })

		for _, field := range m.fields {
			if field.key == "terminal" || field.isPath() {
				continue
			}
			for _, choice := range field.choices {
				t.Run(manager+"/"+field.key+"="+choice.label, func(t *testing.T) {
					fresh := settings.Defaults()
					if err := fresh.Set("manager_type", manager); err != nil {
						t.Fatal(err)
					}
					if err := fresh.SetString(field.key, choice.value); err != nil {
						t.Errorf("the form offers an answer settings rejects: %v", err)
					}
					for _, assign := range choice.extra {
						if err := fresh.SetString(assign.key, assign.value); err != nil {
							t.Errorf("the item's second assignment is rejected: %v", err)
						}
					}
				})
			}
		}
	}
}

// TestSettingsFormImagePullPolicyLabels is the one ported submenu whose text is
// not the value it writes: Python's middle row read "If Not Present" and stored
// `IfNotPresent` (`KubernetesOptionsHandler.py:142-149`). The vocabulary itself
// still comes from `settings`, so a value with no label is offered under its
// own name rather than dropped.
func TestSettingsFormImagePullPolicyLabels(t *testing.T) {
	cfg := settings.Defaults()
	if err := cfg.Set("manager_type", "kubernetes"); err != nil {
		t.Fatal(err)
	}
	m := newSettingsModel(cfg, managerChoices(), func(*settings.Settings) error { return nil })
	focus(t, m, "image_pull_policy")

	want := [][2]string{{"Always", "Always"}, {"If Not Present", "IfNotPresent"}, {"Never", "Never"}}
	got := m.fields[m.cursor].choices
	if len(got) != len(want) {
		t.Fatalf("choices = %v, want %d rows", got, len(want))
	}
	for i, w := range want {
		if got[i].label != w[0] || got[i].value != w[1] {
			t.Errorf("choice %d = %q → %q, want %q → %q", i, got[i].label, got[i].value, w[0], w[1])
		}
	}

	// The label is what the user picks; the value is what reaches the file.
	press(t, m, "enter")
	chooseByLabel(t, m, "If Not Present")
	press(t, m, "enter")
	if cfg.ImagePullPolicy == nil || *cfg.ImagePullPolicy != "IfNotPresent" {
		t.Errorf("image_pull_policy = %v, want IfNotPresent", cfg.ImagePullPolicy)
	}
}

// TestSettingsFormNavigation walks the list and checks that it clamps rather
// than wraps, which is what consolemenu's cursor did.
func TestSettingsFormNavigation(t *testing.T) {
	m, _, _ := newTestForm(t)

	press(t, m, "down", "down", "j")
	if m.cursor != 3 {
		t.Errorf("cursor = %d, want 3", m.cursor)
	}
	press(t, m, "up", "k")
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
	press(t, m, "up", "up", "up")
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (clamped at the top)", m.cursor)
	}

	for range len(m.fields) + 5 {
		press(t, m, "down")
	}
	if m.cursor != len(m.fields)-1 {
		t.Errorf("cursor = %d, want %d (clamped at the bottom)", m.cursor, len(m.fields)-1)
	}
}

// TestSettingsFormHandlesACoalescedBurst covers what a held-down `j` actually
// produces: bubbletea reads the autorepeat in one go and delivers a single
// KeyRunes message carrying every rune. Treated whole it matches no key at all,
// and the cursor never moves.
func TestSettingsFormHandlesACoalescedBurst(t *testing.T) {
	m, _, rec := newTestForm(t)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jjjj")})
	if m.cursor != 4 {
		t.Errorf("cursor = %d, want 4", m.cursor)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("kk")})
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}

	// A burst that reaches a quit stops there rather than replaying the rest.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("qjjj")})
	if !isQuit(cmd) {
		t.Errorf("a burst containing q did not quit")
	}
	if rec.calls != 0 {
		t.Errorf("the burst saved")
	}

	// In a text field the same message is a paste and must arrive whole.
	m2, cfg, _ := newTestForm(t)
	focus(t, m2, "image")
	press(t, m2, "enter", "ctrl+u")
	m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("kathara/frr")})
	press(t, m2, "enter")
	if cfg.Image != "kathara/frr" {
		t.Errorf("image = %q, want the pasted value", cfg.Image)
	}
}

// TestSettingsFormIgnoresAZeroWindowSize is what a pty with no window size
// reports; taking it literally collapses every value to an ellipsis.
func TestSettingsFormIgnoresAZeroWindowSize(t *testing.T) {
	m, _, _ := newTestForm(t)
	m.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	if m.width != cliWidthFallback {
		t.Errorf("width = %d, want the %d fallback", m.width, cliWidthFallback)
	}
	if !strings.Contains(m.View(), "kathara/base") {
		t.Errorf("the list truncated the values away:\n%s", m.View())
	}

	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.width != 120 {
		t.Errorf("width = %d, want 120", m.width)
	}
}

// TestSettingsFormNarrowTerminalKeepsTheValues clips the item names rather than
// the values, which are the part a user is there to read.
func TestSettingsFormNarrowTerminalKeepsTheValues(t *testing.T) {
	m, _, _ := newTestForm(t)
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})

	view := m.View()
	if !strings.Contains(view, "kathara/base") {
		t.Errorf("the image value was truncated away:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if len([]rune(line)) > 60 {
			t.Errorf("a row is %d columns wide at 50: %q", len([]rune(line)), line)
		}
	}
}

// TestSettingsFormEditsAChoice is the ordinary path: open an item, pick an
// answer, land back on the list with the value changed and unsaved.
func TestSettingsFormEditsAChoice(t *testing.T) {
	m, cfg, rec := newTestForm(t)
	focus(t, m, "open_terminals")

	press(t, m, "enter")
	if m.mode != settingsChoosing {
		t.Fatalf("mode = %v, want the submenu", m.mode)
	}
	chooseByLabel(t, m, "No")
	press(t, m, "enter")

	if m.mode != settingsBrowse {
		t.Errorf("mode = %v, want the list", m.mode)
	}
	if cfg.OpenTerminals {
		t.Errorf("open_terminals is still true")
	}
	if !m.dirty {
		t.Errorf("the edit did not mark the form dirty")
	}
	if rec.calls != 0 {
		t.Errorf("an edit saved on its own (%d calls)", rec.calls)
	}
}

// TestSettingsFormEscapeLeavesTheValueAlone is the submenu's cancel.
func TestSettingsFormEscapeLeavesTheValueAlone(t *testing.T) {
	m, cfg, _ := newTestForm(t)
	focus(t, m, "debug_level")

	press(t, m, "enter", "down", "down", "esc")
	if m.mode != settingsBrowse {
		t.Errorf("mode = %v, want the list", m.mode)
	}
	if cfg.DebugLevel != "INFO" {
		t.Errorf("debug_level = %s, want the untouched default", cfg.DebugLevel)
	}
	if m.dirty {
		t.Errorf("a cancelled submenu marked the form dirty")
	}
}

// TestSettingsFormFreeTextEdit types an answer into an item that has no menu at
// all, which is `update_value`'s prompt.
func TestSettingsFormFreeTextEdit(t *testing.T) {
	m, cfg, _ := newTestForm(t)
	focus(t, m, "image")

	press(t, m, "enter")
	if m.mode != settingsEditing {
		t.Fatalf("mode = %v, want the prompt", m.mode)
	}
	// The prompt opens on the current value, so an unedited Enter is a no-op.
	if got := m.input.Value(); got != "kathara/base" {
		t.Errorf("prefill = %q, want the current value", got)
	}

	press(t, m, "ctrl+u")
	press(t, m, "k", "a", "t", "h", "a", "r", "a", "/", "f", "r", "r")
	press(t, m, "enter")

	if m.mode != settingsBrowse {
		t.Errorf("mode = %v, want the list", m.mode)
	}
	if cfg.Image != "kathara/frr" {
		t.Errorf("image = %q", cfg.Image)
	}
}

// TestSettingsFormValidateReject is the third requirement: a rejected answer
// keeps the prompt open with the reason on screen and the value unchanged —
// consolemenu's re-prompt loop, minus the loop.
//
// The message is `settings`' own, not one this file writes.
func TestSettingsFormValidateReject(t *testing.T) {
	cases := []struct{ key, answer, wantMsg string }{
		{key: "net_prefix", answer: "Kathara1",
			wantMsg: "Networks Prefix must only contain lowercase letters and underscore."},
		{key: "device_prefix", answer: "dev-1",
			wantMsg: "Device Prefix must only contain lowercase letters and underscore."},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			m, cfg, _ := newTestForm(t)
			before, err := cfg.Get(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			focus(t, m, tc.key)

			press(t, m, "enter")
			m.input.SetValue(tc.answer)
			press(t, m, "enter")

			if m.mode != settingsEditing {
				t.Errorf("mode = %v, want the prompt to stay open", m.mode)
			}
			if !m.failed || !strings.Contains(m.status, tc.wantMsg) {
				t.Errorf("status = %q (failed=%t), want %q", m.status, m.failed, tc.wantMsg)
			}
			if after, _ := cfg.Get(tc.key); after != before {
				t.Errorf("%s changed to %v despite the rejection", tc.key, after)
			}
			if m.dirty {
				t.Errorf("a rejected answer marked the form dirty")
			}
			if !strings.Contains(m.View(), tc.wantMsg) {
				t.Errorf("the rejection is not on screen:\n%s", m.View())
			}
		})
	}
}

// TestSettingsFormRejectedChoiceKeepsTheSubmenuOpen is the same rule on the
// menu side, using the one menu answer whose validator can fail: `terminal`.
func TestSettingsFormRejectedChoiceKeepsTheSubmenuOpen(t *testing.T) {
	m, cfg, _ := newTestForm(t)
	focus(t, m, "terminal")

	press(t, m, "enter")
	// Replace the offered emulators with one that certainly is not installed,
	// so the test does not depend on the host's /usr/bin.
	m.fields[m.cursor].choices = []settingsChoice{{label: "nope", value: filepath.Join(t.TempDir(), "nope")}}
	m.choice = 0
	press(t, m, "enter")

	if m.mode != settingsChoosing {
		t.Errorf("mode = %v, want the submenu to stay open", m.mode)
	}
	if !m.failed {
		t.Errorf("status = %q, want a rejection", m.status)
	}
	if cfg.Terminal == "nope" {
		t.Errorf("the rejected terminal was stored")
	}
}

// TestSettingsFormSave is the explicit save: `s` writes and clears the dirty
// flag, and says what consolemenu said.
func TestSettingsFormSave(t *testing.T) {
	m, cfg, rec := newTestForm(t)
	focus(t, m, "enable_ipv6")
	press(t, m, "enter")
	chooseByLabel(t, m, "Yes")
	press(t, m, "enter")

	if quit := press(t, m, "s"); quit {
		t.Errorf("`s` quit the form")
	}
	if rec.calls != 1 {
		t.Fatalf("save calls = %d, want 1", rec.calls)
	}
	if !rec.last.EnableIPv6 {
		t.Errorf("the saved settings do not carry the edit")
	}
	if m.dirty {
		t.Errorf("the form is still dirty after a save")
	}
	if m.status != "Saved successfully!" {
		t.Errorf("status = %q", m.status)
	}
	if !cfg.EnableIPv6 {
		t.Errorf("the model's settings lost the edit")
	}
}

// TestSettingsFormQuitWithoutSave is the fourth requirement. An unsaved form
// asks before leaving, and `n` discards.
func TestSettingsFormQuitWithoutSave(t *testing.T) {
	m, _, rec := newTestForm(t)
	focus(t, m, "enable_ipv6")
	press(t, m, "enter")
	chooseByLabel(t, m, "Yes")
	press(t, m, "enter")

	if quit := press(t, m, "q"); quit {
		t.Fatalf("a dirty form quit without asking")
	}
	if m.mode != settingsConfirmQuit {
		t.Fatalf("mode = %v, want the confirmation", m.mode)
	}
	if !strings.Contains(m.View(), "unsaved changes") {
		t.Errorf("the confirmation does not say what is at stake:\n%s", m.View())
	}

	// Escape goes back to the list rather than leaving.
	if quit := press(t, m, "esc"); quit {
		t.Fatalf("esc quit the confirmation")
	}
	if m.mode != settingsBrowse {
		t.Errorf("mode = %v, want the list", m.mode)
	}

	if quit := press(t, m, "q", "n"); !quit {
		t.Fatalf("`n` did not quit")
	}
	if rec.calls != 0 {
		t.Errorf("quitting without saving wrote the file (%d calls)", rec.calls)
	}
}

// TestSettingsFormConfirmSaveThenQuit is the other arm of the same question.
func TestSettingsFormConfirmSaveThenQuit(t *testing.T) {
	m, _, rec := newTestForm(t)
	focus(t, m, "enable_ipv6")
	press(t, m, "enter")
	chooseByLabel(t, m, "Yes")
	press(t, m, "enter")

	if quit := press(t, m, "q", "y"); !quit {
		t.Fatalf("`y` did not quit")
	}
	if rec.calls != 1 {
		t.Errorf("save calls = %d, want 1", rec.calls)
	}
	if !rec.last.EnableIPv6 {
		t.Errorf("the save did not carry the edit")
	}
}

// TestSettingsFormCleanQuitDoesNotAsk keeps the question out of the way when
// there is nothing to lose.
func TestSettingsFormCleanQuitDoesNotAsk(t *testing.T) {
	for _, name := range []string{"q", "esc"} {
		m, _, rec := newTestForm(t)
		if quit := press(t, m, name); !quit {
			t.Errorf("%q did not quit a clean form", name)
		}
		if rec.calls != 0 {
			t.Errorf("%q saved a clean form", name)
		}
	}
}

// TestSettingsFormCtrlCNeverSaves is the one key that leaves from anywhere,
// including from inside a text field, and never writes.
func TestSettingsFormCtrlCNeverSaves(t *testing.T) {
	for _, prefix := range [][]string{nil, {"enter"}, {"enter", "esc", "q"}} {
		m, _, rec := newTestForm(t)
		focus(t, m, "image")
		press(t, m, prefix...)

		if quit := press(t, m, "ctrl+c"); !quit {
			t.Errorf("ctrl+c after %v did not quit", prefix)
		}
		if rec.calls != 0 {
			t.Errorf("ctrl+c after %v saved", prefix)
		}
	}
}

// TestSettingsFormSaveFailureKeepsTheForm: a file that cannot be written must
// not cost the user the edits, and the failure has to reach the command's exit
// code rather than being a status line nobody sees.
func TestSettingsFormSaveFailureKeepsTheForm(t *testing.T) {
	m, _, rec := newTestForm(t)
	rec.err = errors.New("read-only file system")
	focus(t, m, "enable_ipv6")
	press(t, m, "enter")
	chooseByLabel(t, m, "Yes")
	press(t, m, "enter")

	if quit := press(t, m, "s"); quit {
		t.Errorf("a failed save quit the form")
	}
	if !m.failed || !strings.Contains(m.status, "read-only file system") {
		t.Errorf("status = %q", m.status)
	}
	if !m.dirty {
		t.Errorf("a failed save cleared the dirty flag")
	}
	if m.err == nil {
		t.Errorf("the failure was not recorded for the caller")
	}

	// The same failure from the quit confirmation returns to the list rather
	// than throwing the edits away.
	if quit := press(t, m, "q", "y"); quit {
		t.Errorf("a failed save from the confirmation quit anyway")
	}
	if m.mode != settingsBrowse {
		t.Errorf("mode = %v, want the list", m.mode)
	}
}

// TestSettingsFormManagerSwitchRebuildsTheList is `update_manager_value`
// (`CommonOptionsHandler.py:425-435`): changing the manager builds a new menu,
// because the addon's keys are different ones.
func TestSettingsFormManagerSwitchRebuildsTheList(t *testing.T) {
	m, cfg, _ := newTestForm(t)
	focus(t, m, "hosthome_mount")
	press(t, m, "enter")
	chooseByLabel(t, m, "Yes")
	press(t, m, "enter")
	if !cfg.HosthomeMount {
		t.Fatal("setup failed")
	}

	focus(t, m, "manager_type")
	press(t, m, "enter")
	chooseByLabel(t, m, managerLabel(t, "kubernetes"))
	press(t, m, "enter")

	keys := map[string]bool{}
	for _, field := range m.fields {
		keys[field.key] = true
	}
	if keys["hosthome_mount"] {
		t.Errorf("the docker rows survived the switch")
	}
	if !keys["docker_config_json"] {
		t.Errorf("the kubernetes rows were not added: %v", keys)
	}

	// And back, which must show the addon defaults rather than the values the
	// docker session left behind.
	focus(t, m, "manager_type")
	press(t, m, "enter")
	chooseByLabel(t, m, managerLabel(t, "docker"))
	press(t, m, "enter")
	if cfg.HosthomeMount {
		t.Errorf("hosthome_mount survived the round trip; the addon was not reset")
	}
}

// TestSettingsFormResetToNull is the "Reset value to Empty String" item, which
// stores `None` and not `""`.
func TestSettingsFormResetToNull(t *testing.T) {
	m, cfg, _ := newTestForm(t)
	if err := cfg.SetString("cert_path", "/etc/docker/certs"); err != nil {
		t.Fatal(err)
	}

	focus(t, m, "cert_path")
	press(t, m, "enter")
	chooseByLabel(t, m, "Reset value to Empty String")
	press(t, m, "enter")

	if cfg.CertPath != nil {
		t.Errorf("cert_path = %q, want null", *cfg.CertPath)
	}
}

// TestSettingsFormRemoteURLResetClearsBoth is the one menu item that wrote two
// keys: "Reset remote Docker connection to default"
// (`DockerOptionsHandler.py:249-256`).
func TestSettingsFormRemoteURLResetClearsBoth(t *testing.T) {
	m, cfg, _ := newTestForm(t)
	if err := cfg.SetString("remote_url", "http://10.0.0.1:2375"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetString("cert_path", "/etc/docker/certs"); err != nil {
		t.Fatal(err)
	}

	focus(t, m, "remote_url")
	press(t, m, "enter")
	chooseByLabel(t, m, "Reset remote Docker connection to default")
	press(t, m, "enter")

	if cfg.RemoteURL != nil || cfg.CertPath != nil {
		t.Errorf("remote_url = %v, cert_path = %v, want both null", cfg.RemoteURL, cfg.CertPath)
	}
}

// TestSettingsFormFreeTextEscapeFromASubmenu is the "Choose another …" row the
// handlers appended next to their suggestions.
func TestSettingsFormFreeTextEscapeFromASubmenu(t *testing.T) {
	m, cfg, _ := newTestForm(t)
	focus(t, m, "device_shell")

	press(t, m, "enter")
	chooseByLabel(t, m, "Choose another value…")
	press(t, m, "enter")

	if m.mode != settingsEditing {
		t.Fatalf("mode = %v, want the prompt", m.mode)
	}
	m.input.SetValue("/usr/local/bin/nu")
	press(t, m, "enter")

	if cfg.DeviceShell != "/usr/local/bin/nu" {
		t.Errorf("device_shell = %q", cfg.DeviceShell)
	}
}

// TestSettingsFormDockerConfigJSONTakesAPath is the OQ-12c behaviour on the TUI
// side: the answer is a path, the stored value is the base64 of what it holds,
// and the prompt is prefilled with `DEFAULT_DOCKER_CONFIG_JSON_PATH`.
func TestSettingsFormDockerConfigJSONTakesAPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"auths":   {}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := settings.Defaults()
	if err := cfg.Set("manager_type", "kubernetes"); err != nil {
		t.Fatal(err)
	}
	m := newSettingsModel(cfg, managerChoices(), func(*settings.Settings) error { return nil })

	focus(t, m, "docker_config_json")
	press(t, m, "enter")
	chooseByLabel(t, m, "Choose another value…")
	press(t, m, "enter")
	if got := m.input.Value(); got != settings.DefaultDockerConfigJSONPath {
		t.Errorf("prefill = %q, want %q", got, settings.DefaultDockerConfigJSONPath)
	}

	m.input.SetValue(path)
	press(t, m, "enter")

	if cfg.DockerConfigJSON == nil {
		t.Fatal("docker_config_json is still null")
	}
	if *cfg.DockerConfigJSON != "eyJhdXRocyI6IHt9fQ==" {
		t.Errorf("stored = %q, want the base64 of `{\"auths\": {}}`", *cfg.DockerConfigJSON)
	}
}

// TestSettingsFormDockerConfigJSONRejectsABadPath keeps the prompt open on the
// two failure classes the path can produce.
func TestSettingsFormDockerConfigJSONRejectsABadPath(t *testing.T) {
	cfg := settings.Defaults()
	if err := cfg.Set("manager_type", "kubernetes"); err != nil {
		t.Fatal(err)
	}
	m := newSettingsModel(cfg, managerChoices(), func(*settings.Settings) error { return nil })

	focus(t, m, "docker_config_json")
	press(t, m, "enter")
	chooseByLabel(t, m, "Choose another value…")
	press(t, m, "enter")
	m.input.SetValue(filepath.Join(t.TempDir(), "absent.json"))
	press(t, m, "enter")

	if m.mode != settingsEditing {
		t.Errorf("mode = %v, want the prompt to stay open", m.mode)
	}
	if !m.failed {
		t.Errorf("status = %q, want a rejection", m.status)
	}
	if cfg.DockerConfigJSON != nil {
		t.Errorf("a bad path was stored: %q", *cfg.DockerConfigJSON)
	}
}

// TestSettingsFormViewRendersEveryMode is a smoke test over the four pages: no
// panic, and each one names what it is.
func TestSettingsFormViewRendersEveryMode(t *testing.T) {
	m, _, _ := newTestForm(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	if !strings.Contains(m.View(), "Kathara Settings") {
		t.Errorf("the list has no title:\n%s", m.View())
	}
	// Every row shows its current value, which is `current_string`'s subtitle.
	if !strings.Contains(m.View(), "kathara/base") {
		t.Errorf("the list does not show the current image:\n%s", m.View())
	}

	focus(t, m, "debug_level")
	press(t, m, "enter")
	view := m.View()
	for _, want := range []string{"Choose logging level to be used", "Current: INFO", "CRITICAL", "EXCEPTION"} {
		if !strings.Contains(view, want) {
			t.Errorf("the submenu is missing %q:\n%s", want, view)
		}
	}

	press(t, m, "esc")
	focus(t, m, "image")
	press(t, m, "enter")
	if !strings.Contains(m.View(), "Write the name of a Docker image available on Docker Hub:") {
		t.Errorf("the prompt is missing its message:\n%s", m.View())
	}

	press(t, m, "esc")
	m.dirty = true
	press(t, m, "q")
	if !strings.Contains(m.View(), "Save them before quitting?") {
		t.Errorf("the confirmation is missing its question:\n%s", m.View())
	}
}

// TestSettingsFormTruncatesALongValue keeps the list readable when
// `docker_config_json` holds a kilobyte of base64.
func TestSettingsFormTruncatesALongValue(t *testing.T) {
	cfg := settings.Defaults()
	if err := cfg.Set("manager_type", "kubernetes"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetString("docker_config_json", strings.Repeat("A", 4000)); err != nil {
		t.Fatal(err)
	}
	m := newSettingsModel(cfg, managerChoices(), func(*settings.Settings) error { return nil })
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for _, line := range strings.Split(m.View(), "\n") {
		if len([]rune(line)) > 100 {
			t.Fatalf("a list row is %d columns wide: %q", len([]rune(line)), line)
		}
	}
}

// TestSettingsWithoutATerminalRefuses is the honest degradation the curses menu
// never had: with no TTY the command names the scriptable interface instead of
// driving a terminal that is not there.
func TestSettingsWithoutATerminalRefuses(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["settings"]

	if code := runCommand(t.Context(), a.app, spec, nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(a.stdoutString(), "kathara config get|set|list|reset") {
		t.Errorf("stdout = %q", a.stdoutString())
	}
}

// TestSettingsCommandTakesNoFlags is CLI_SURFACE.md M-5: `kathara settings -h`
// does not print help, because the command never parses argv at all.
func TestSettingsCommandTakesNoFlags(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["settings"]
	if !spec.NoParser {
		t.Fatal("the settings command grew a parser")
	}
	if code := runCommand(t.Context(), a.app, spec, []string{"-h"}); code != 1 {
		t.Fatalf("exit = %d, want the no-TTY refusal rather than help", code)
	}
	if strings.Contains(a.stdoutString(), "usage:") {
		t.Errorf("`settings -h` printed help:\n%s", a.stdoutString())
	}
}

// TestManagerChoicesCarryTheWholeVocabulary keeps the `manager_type` menu on
// `AVAILABLE_MANAGERS` rather than on what this build happens to link, which is
// what makes the file's vocabulary and the screen's agree in a `nok8s` build.
func TestManagerChoicesCarryTheWholeVocabulary(t *testing.T) {
	choices := managerChoices()
	names := settings.AvailableManagers()
	if len(choices) != len(names) {
		t.Fatalf("choices = %v, want one per %v", choices, names)
	}
	for i, choice := range choices {
		if choice.value != names[i] {
			t.Errorf("choice %d = %q, want %q", i, choice.value, names[i])
		}
		if choice.label == "" {
			t.Errorf("choice %d has no label", i)
		}
	}
}
