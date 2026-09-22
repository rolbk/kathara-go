package cliout

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ErrPromptEOF is what `rich.prompt`'s `console.input` does at end of input:
// CPython's `input()` raises `EOFError`, nothing in Kathará catches it, and
// `src/kathara.py:102-108` prints it as `CRITICAL (EOFError) EOF when reading a
// line` and exits 1. The Layer A harness closes stdin on every run
// (NORMALIZATION.md §1), so this is the answer a non-interactive human-mode
// `kathara wipe` gets.
var ErrPromptEOF error = &eofError{}

type eofError struct{}

func (e *eofError) Error() string      { return "EOF when reading a line" }
func (e *eofError) ErrorCode() string  { return kerrors.CodeInternalError }
func (e *eofError) HumanLabel() string { return "EOFError" }

// Prompter is the confirmation half of the CLI's interactivity.
type Prompter struct {
	// Console renders the question and takes the lock while it does.
	Console *Console
	// In is where the answer is read from. Nil means os.Stdin was not
	// supplied and every question answers [ErrPromptEOF], which is what a
	// closed stdin does in Python too.
	In io.Reader

	reader *bufio.Reader
}

// Confirm is `Confirm.ask(prompt)`: render `<prompt> [y/n]: `, read a line,
// and re-ask on anything that is not `y` or `n` after stripping and
// lower-casing (`rich/prompt.py`, `Confirm.process_response`).
func (p *Prompter) Confirm(prompt string) (bool, error) {
	if p.In == nil {
		p.Console.printPromptSuffix(prompt)
		return false, ErrPromptEOF
	}
	if p.reader == nil {
		p.reader = bufio.NewReader(p.In)
	}
	for {
		p.Console.printPromptSuffix(prompt)
		line, err := p.reader.ReadString('\n')
		if line == "" && err != nil {
			if errors.Is(err, io.EOF) {
				return false, ErrPromptEOF
			}
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y":
			return true, nil
		case "n":
			return false, nil
		}
		p.Console.Print("Please enter Y or N")
		if err != nil {
			// The stream ended on the same read that produced an invalid
			// answer; the next `input()` would raise.
			return false, ErrPromptEOF
		}
	}
}

// printPromptSuffix writes the question with rich's `prompt_suffix` and no
// newline, which is what puts the caret after the colon.
func (c *Console) printPromptSuffix(prompt string) {
	if c.Format.Machine() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = fmt.Fprintf(c.Out, "%s [y/n]: ", prompt)
}

// TreeNode is one row of the volume tree: a label plus its children.
type TreeNode struct {
	Label    string
	Children []TreeNode
}

// treeGuides is `rich.tree.TREE_GUIDES[0]` — the guide set rich picks when the
// guide style is neither bold nor doubly underlined, which the default
// `tree.line` theme style is not.
var treeGuides = [4]string{"    ", "│   ", "├── ", "└── "}

// Tree renders a `rich.tree.Tree` the way `MountDevicesVolumes.run` builds one:
// a root label with one level of children, no styling.
func Tree(node TreeNode, width int) []string {
	if width <= 0 {
		width = DefaultWidth
	}
	var out []string
	out = append(out, renderTreeLabel(node.Label, "", "", width)...)
	for i, child := range node.Children {
		guide := treeGuides[2]
		cont := treeGuides[1]
		if i == len(node.Children)-1 {
			guide = treeGuides[3]
			cont = treeGuides[0]
		}
		out = append(out, renderTreeLabel(child.Label, guide, cont, width)...)
	}
	return out
}

// renderTreeLabel wraps one label into the width left by its guide prefix and
// prefixes the continuations with the vertical guide.
func renderTreeLabel(label, prefix, cont string, width int) []string {
	avail := width - CellLen(prefix)
	if avail < 1 {
		avail = 1
	}
	lines := Wrap(label, avail, JustifyDefault)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, strings.TrimRight(prefix+line, " "))
			continue
		}
		out = append(out, strings.TrimRight(cont+line, " "))
	}
	return out
}
