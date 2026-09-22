// All of them are drawn only in `human` mode.

package cliout

import (
	"fmt"
	"strings"
	"sync"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/model"
)

// barGlyph is `rich.progress.BarColumn`'s complete block.
const barGlyph = "━"

// spinnerFrames is `rich`'s "dots" spinner, the braille cycle that
// NORMALIZATION.md §6.6 drops: which frame is on screen is a function of
// elapsed time. `SpinnerColumn` renders a frame whenever the task is *not*
// finished and its blank finished-text once `completed >= total`
// (`rich/progress.py`, `SpinnerColumn.render`), which is what makes "carries a
// braille glyph" mean "this render caught the bar mid-flight".
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// ProgressBar is `cli/ui/event/HandleProgressBar.py`: one bar per message,
// subscribed to a started/item/ended triple.
type ProgressBar struct {
	// Message is the bar's description, e.g. "Deploying devices".
	Message string
	// Console is where the bar is drawn.
	Console *Console

	mu      sync.Mutex
	active  bool
	total   int
	done    int
	frame   int
	painted bool
}

// NewProgressBar is `HandleProgressBar(message)`.
func NewProgressBar(message string, console *Console) *ProgressBar {
	return &ProgressBar{Message: message, Console: console}
}

// Init is `HandleProgressBar.init(items)`: build the bar with `total=len(items)`.
func (b *ProgressBar) Init(total int) {
	if b.Console == nil || b.Console.Format.Machine() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active = true
	b.total = total
	b.done = 0
	b.frame = 0
	b.painted = false
	b.paintLocked(false)
}

// Advance is `HandleProgressBar.update(item)`: `advance=1`, ignoring the item —
// the Python body reads nothing off it (`HandleProgressBar.py:37-47`).
func (b *ProgressBar) Advance() {
	if b.Console == nil || b.Console.Format.Machine() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active {
		return
	}
	b.done++
	b.frame++
	b.paintLocked(false)
}

// Finish is `HandleProgressBar.finish()` and, through the subscription hook,
// `HandleProgressBar.unregister()`. It is idempotent for the same reason
// Python's is: the body is guarded by `if self.progress_bar`.
func (b *ProgressBar) Finish() error {
	if b.Console == nil || b.Console.Format.Machine() {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active {
		return nil
	}
	b.paintLocked(true)
	b.active = false
	return nil
}

// paintLocked draws the bar. On a terminal it redraws in place with a carriage
// return, which is what `rich.live.Live` does; on a file or a dumb terminal
// rich suppresses every intermediate refresh and prints the final state once at
// stop (`rich/live.py`, the `not self._started and not self.transient` arm), so
// this does too.
func (b *ProgressBar) paintLocked(final bool) {
	c := b.Console
	if !c.TTY && !final {
		return
	}

	width := c.Width
	desc := fmt.Sprintf("[%s]", b.Message)
	counts := fmt.Sprintf("%d/%d", b.done, b.total)
	// `SpinnerColumn` goes blank on a *finished* task, not on the last render:
	// rich's `task.finished` is `completed >= total`, so a display torn down
	// while items are still outstanding — a failure part-way through a deploy —
	// prints a braille frame even at stop. Blanking the cell on `final` alone
	// made this port emit a spinner-free row exactly where Python emits a
	// time-derived one, i.e. a row the Layer A normalizer keeps against a
	// golden that drops it.
	spinner := string(spinnerFrames[b.frame%len(spinnerFrames)])
	if b.done >= b.total {
		spinner = " "
	}

	barWidth := width - CellLen(desc) - CellLen(counts) - 4
	if barWidth < 1 {
		barWidth = 1
	}
	// A *partial* bar is spelled differently from rich's, which paints the
	// whole width in `━` and distinguishes done from not-done by style alone
	// (`bar.complete` against `bar.back`) plus a `╸` half-tick. This port emits
	// no styling, so a full-width run of `━` would read as "finished" at every
	// point; the remainder is left blank instead. Nothing observable rests on
	// it: every partial render carries a spinner frame and is dropped, and the
	// completed bar — the one a golden compares — is a full run either way.
	filled := barWidth
	if b.total > 0 && b.done < b.total {
		filled = b.done * barWidth / b.total
	}
	if filled < 1 {
		filled = 1
	}
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat(barGlyph, filled) + strings.Repeat(" ", barWidth-filled)

	line := fmt.Sprintf("%s %s %s %s", desc, spinner, bar, counts)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.TTY {
		_, _ = fmt.Fprintf(c.Out, "\r%s", line)
		if final {
			_, _ = fmt.Fprintln(c.Out)
		}
		b.painted = true
		return
	}
	_, _ = fmt.Fprintln(c.Out, line)
	b.painted = true
}

// ImagePullBar is `cli/ui/event/HandleDockerImagePull.py`: one task per Docker
// layer, keyed by the layer id.
type ImagePullBar struct {
	// Console is where the bar is drawn.
	Console *Console

	mu     sync.Mutex
	active bool
	order  []string
	layers map[string]pullLayer
}

type pullLayer struct {
	complete bool
	current  int64
	total    int64
	hasTotal bool
}

// NewImagePullBar is `HandleDockerImagePull()`.
func NewImagePullBar(console *Console) *ImagePullBar {
	return &ImagePullBar{Console: console}
}

// Init is `HandleDockerImagePull.init()`.
func (b *ImagePullBar) Init() {
	if b.Console == nil || b.Console.Format.Machine() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active = true
	b.order = nil
	b.layers = make(map[string]pullLayer)
}

// Update is `HandleDockerImagePull.update(progress)`.
func (b *ImagePullBar) Update(p event.PullProgress) error {
	if b.Console == nil || b.Console.Format.Machine() {
		return nil
	}
	if p.Status == nil {
		// `progress['status']` on a line that carries no status: CPython
		// raises KeyError and nothing on the path catches it.
		return &model.PyRuntimeError{Class: "KeyError", Msg: "'status'"}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active {
		return nil
	}

	var complete bool
	switch *p.Status {
	case "Download complete":
		complete = true
	case "Downloading":
		complete = false
	default:
		return nil
	}
	if p.ID == nil {
		return &model.PyRuntimeError{Class: "KeyError", Msg: "'id'"}
	}
	id := *p.ID

	layer, seen := b.layers[id]
	if !seen {
		b.order = append(b.order, id)
	}
	layer.complete = complete
	if complete {
		layer.total, layer.hasTotal, layer.current = 100, true, 100
	} else {
		if p.Detail == nil {
			return &model.PyRuntimeError{Class: "KeyError", Msg: "'progressDetail'"}
		}
		if !seen {
			if p.Detail.Total != nil {
				layer.total, layer.hasTotal = *p.Detail.Total, true
			}
		}
		if p.Detail.Current != nil {
			layer.current = *p.Detail.Current
		}
	}
	b.layers[id] = layer
	b.paintLocked()
	return nil
}

// Finish is `HandleDockerImagePull.finish()`: every task is forced to its own
// total and relabelled "Download Complete" before the display stops.
func (b *ImagePullBar) Finish() error {
	if b.Console == nil || b.Console.Format.Machine() {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active {
		return nil
	}
	for id, layer := range b.layers {
		layer.complete = true
		layer.current = layer.total
		b.layers[id] = layer
	}
	b.paintLocked()
	b.active = false
	b.order = nil
	b.layers = nil
	return nil
}

// paintLocked draws one line per layer. It only draws on a terminal: a
// per-layer redraw of a whole block has no meaningful non-TTY rendering, and
// the Layer A normalizer drops the lines anyway.
func (b *ImagePullBar) paintLocked() {
	c := b.Console
	if !c.TTY {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range b.order {
		layer := b.layers[id]
		label := fmt.Sprintf("[Downloading %s]", id)
		pct := "  ?%"
		if layer.complete {
			label = fmt.Sprintf("[Download Complete %s]", id)
			pct = "100%"
		} else if layer.hasTotal && layer.total > 0 {
			pct = fmt.Sprintf("%3d%%", layer.current*100/layer.total)
		}
		_, _ = fmt.Fprintf(c.Out, "\r%s %s %s\n", label, barGlyph, pct)
	}
}

// ImageUpdatePolicy answers `UpdateDockerImage.run` for one image whose remote
// digest moved.
type ImageUpdatePolicy struct {
	// Policy is `Setting.image_update_policy`.
	Policy string
	// Console renders the question, or the notice that replaced it.
	Console *Console
	// Prompter reads the answer in human mode.
	Prompter *Prompter
}

// Run is `UpdateDockerImage.run(docker_image, image_name)`.
func (u *ImageUpdatePolicy) Run(e event.DockerImageUpdateFound) error {
	switch u.Policy {
	case PolicyAlways:
		return e.Image.Pull(e.ImageName)
	case PolicyPrompt:
		if u.Console.Format.Machine() {
			u.Console.Log(LevelInfo,
				"a new version of image `%s` is available; not pulling it (--format %s never prompts)",
				e.ImageName, u.Console.Format)
			return nil
		}
		yes, err := u.Prompter.Confirm(fmt.Sprintf(
			"A new version of image `%s` has been found on Docker Hub. Do you want to pull it?",
			e.ImageName))
		if err != nil {
			return err
		}
		if yes {
			return e.Image.Pull(e.ImageName)
		}
	}
	return nil
}

// VolumeMountPolicy is `cli/ui/event/MountDevicesVolumes.py`: print the tree of
// host-to-guest mounts, then, under `Prompt`, ask whether to go ahead.
type VolumeMountPolicy struct {
	// Policy is `Setting.volume_mount_policy`.
	Policy string
	// Console prints the tree and the question.
	Console *Console
	// Prompter reads the answer in human mode.
	Prompter *Prompter
}

// Run is `MountDevicesVolumes.run(lab, machines_with_volumes)`. Declining sets
// the scenario option `_mount_volumes` to false, which is how the backend is
// told to deploy without the mounts.
func (v *VolumeMountPolicy) Run(e event.MachinesWithVolumes) error {
	if !v.Console.Format.Machine() {
		v.Console.Print("The following devices have volumes configured:")
		for _, machine := range e.Machines {
			node := TreeNode{Label: fmt.Sprintf("* Device `%s`", machine.Name)}
			volumes, err := machine.GetVolumes()
			if err != nil {
				return err
			}
			for _, entry := range volumes.Entries() {
				guest := entry.Value.GuestPath
				if guest == "" {
					guest = "<missing guest_path>"
				}
				node.Children = append(node.Children, TreeNode{
					Label: fmt.Sprintf("Host Path: %s -> Device Path: %s", entry.Key, guest),
				})
			}
			v.Console.PrintLines(Tree(node, v.Console.Width))
		}
	}

	if v.Policy != PolicyPrompt {
		return nil
	}
	if v.Console.Format.Machine() {
		v.Console.Log(LevelInfo, "mounting the declared volumes (--format %s never prompts)", v.Console.Format)
		return nil
	}

	yes, err := v.Prompter.Confirm("Continue with volume mounting?")
	if err != nil {
		return err
	}
	if !yes && e.Lab != nil {
		e.Lab.AddOption("_mount_volumes", model.Bool(false))
	}
	return nil
}

// PrintWaitMessage is `HandleMachineTerminal.print_wait_msg`: the one-line
// notice that `connect` prints while it waits for `/tmp/EOS`, with no newline
// so that the ENTER the user presses lands on the same row.
func (c *Console) PrintWaitMessage() {
	if c.Format.Machine() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = fmt.Fprint(c.Out, "Waiting startup commands execution. Press [ENTER] to override...")
}

func (c *Console) ClearScreen() {
	if c.Format.Machine() || !c.TTY {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = fmt.Fprint(c.Out, "\033[2J\033[0;0H")
}

// PolicyPrompt is the `image_update_policy` / `volume_mount_policy` value that
// makes the two handlers ask a question (`setting/Setting.py`'s menus).
const PolicyPrompt = "Prompt"

// PolicyAlways is the `image_update_policy` value that pulls without asking.
const PolicyAlways = "Always"
