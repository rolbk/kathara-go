// This file is the half of `argparse.ArgumentParser` that `pflag` has no
// equivalent for and whose ordering differs in `cobra`: which options
// are `required=True`, which mutually-exclusive groups exist and whether they
// are themselves required, which positionals the parser declares, and the
// metavars all four render with.
// It exists because `cobra.Command.ValidateFlagGroups` checks only the group
// annotations — `MarkFlagRequired` is answered by a *different* method — and
// because the order the three checks fire in is observable. argparse's is:
//  1. a mutually-exclusive conflict, raised from inside the parse loop the
//     moment the second member of a group is consumed;
//  2. the `required=True` sweep, `the following arguments are required: …`;
//  3. the required mutually-exclusive groups,
//     `one of the arguments --add --rm is required`;
//  4. `parse_args`'s leftover check, `unrecognized arguments: …`.
// Oracle-verified on all four: `lconfig --add A --rm B` (no `-n`) reports the
// conflict and not the missing name, `lconfig -n pc1` reports the group, and
// `wipe extra` reports the leftover.
// An argparse *action* is not a pflag flag. `-w, -l, --watch, --live` is one
// action with four spellings; pflag needs two flags to express it, and a
// mutually-exclusive group that named only one of them would let the other slip
// through. [parser.aliasOf] is what puts the two back together.

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// nargsKind is the argparse `nargs` of a positional.
type nargsKind int

const (
	// nargsOne is the default: exactly one token, and a missing one is a
	// "required" failure.
	nargsOne nargsKind = iota
	// nargsOneOrMore is `nargs='+'`.
	nargsOneOrMore
	// nargsZeroOrMore is `nargs='*'`.
	nargsZeroOrMore
	// nargsRemainder is `nargs=argparse.REMAINDER`, which never fails.
	nargsRemainder
)

// positional is one positional `add_argument`.
type positional struct {
	metavar string
	kind    nargsKind
	help    string
}

// parser is one sub-command's `argparse.ArgumentParser`: cobra's flag set plus
// the metadata above.
type parser struct {
	*cobra.Command

	// metavars is the `metavar=` of each option that has one. An option with
	// no entry takes no value.
	metavars map[string]string
	// optNames overrides the option-string list of an action whose argparse
	// spelling order differs from pflag's (`--no-hosthome, -H`), or that has
	// more than the two spellings a pflag flag can carry.
	optNames map[string][]string
	// aliasFor maps a pflag flag to the flag whose argparse action it repeats,
	// so that `--dry-mode` and `--print` count as one and `--live` and
	// `--watch` share a mutually-exclusive group.
	aliasFor map[string]string
	// listKind records the `nargs` of the options declared with one, for the
	// usage line.
	listKind map[string]nargsKind

	// required is the `required=True` optionals, in declaration order.
	required []string
	// exclusive and oneRequired are `add_mutually_exclusive_group(required=…)`,
	// each holding pflag flag names in declaration order.
	exclusive   [][]string
	oneRequired [][]string
	// positionals is the positional list, in declaration order.
	positionals []positional
}

// meta records a `metavar=`.
func (p *parser) meta(name, metavar string) {
	p.metavars[name] = metavar
}

// names records an action's argparse option strings when they are not
// `-x, --long`.
func (p *parser) names(name string, optNames ...string) {
	p.optNames[name] = optNames
}

// alias records that name is a second spelling of the action canonical backs.
func (p *parser) alias(name, canonical string) {
	p.aliasFor[name] = canonical
}

// require is `required=True` on an optional.
func (p *parser) require(names ...string) {
	p.required = append(p.required, names...)
}

// exclusiveGroup is `add_mutually_exclusive_group(required=False)`.
func (p *parser) exclusiveGroup(names ...string) {
	p.exclusive = append(p.exclusive, names)
}

// requiredGroup is `add_mutually_exclusive_group(required=True)`, which is both
// exclusive and one-of.
func (p *parser) requiredGroup(names ...string) {
	p.exclusive = append(p.exclusive, names)
	p.oneRequired = append(p.oneRequired, names)
}

// pos declares a positional.
func (p *parser) pos(metavar string, kind nargsKind, help string) {
	p.positionals = append(p.positionals, positional{metavar: metavar, kind: kind, help: help})
}

// canonical follows [parser.aliasFor] to the flag that owns the action.
func (p *parser) canonical(name string) string {
	for {
		next, ok := p.aliasFor[name]
		if !ok {
			return name
		}
		name = next
	}
}

// actionChanged is argparse's "this action was seen", which is true when ANY of
// its spellings was given.
func (p *parser) actionChanged(name string) bool {
	if p.Flags().Changed(name) {
		return true
	}
	changed := false
	p.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed && p.canonical(f.Name) == name {
			changed = true
		}
	})
	return changed
}

// actionName is argparse's `_get_action_name`: the option strings joined with
// `/`, which is how a required or conflicting option is named in an error.
func (p *parser) actionName(name string) string {
	return strings.Join(p.optionStrings(name), "/")
}

// optionStrings is the action's spellings, in argparse's declaration order.
func (p *parser) optionStrings(name string) []string {
	if custom, ok := p.optNames[name]; ok {
		return custom
	}
	f := p.Flags().Lookup(name)
	if f == nil {
		return []string{"--" + name}
	}
	if f.Shorthand != "" {
		return []string{"-" + f.Shorthand, "--" + f.Name}
	}
	return []string{"--" + f.Name}
}

// validate runs the four checks in argparse's order. head is the option half of
// argv, which the mutually-exclusive message needs in order to name the option
// that arrived *second*, as argparse does.
func (p *parser) validate(head []string) error {
	if err := p.checkExclusive(head); err != nil {
		return err
	}
	if err := p.checkRequired(); err != nil {
		return err
	}
	if err := p.checkOneRequired(); err != nil {
		return err
	}
	return p.checkLeftovers()
}

// checkExclusive is argparse's `take_action` conflict check. For each group it
// walks the members in the order the command line mentioned them and reports
// the first that finds an already-seen sibling; the sibling named is the
// earliest-*declared* one, which is the order `action_conflicts` holds.
func (p *parser) checkExclusive(head []string) error {
	order := p.optionOrder(head)

	bestAt := -1
	var bestErr error
	for _, group := range p.exclusive {
		mentioned := make([]string, 0, len(group))
		for _, name := range group {
			if p.actionChanged(name) {
				mentioned = append(mentioned, name)
			}
		}
		if len(mentioned) < 2 {
			continue
		}
		sort.SliceStable(mentioned, func(i, j int) bool {
			return order[mentioned[i]] < order[mentioned[j]]
		})

		seen := map[string]bool{}
		conflicted := false
		for _, name := range mentioned {
			for _, sibling := range group {
				if sibling == name || !seen[sibling] {
					continue
				}
				// argparse iterates an action's conflicts in group declaration
				// order and raises on the first one already consumed, so the
				// option named second is the earliest-*declared* sibling.
				if at := order[name]; bestAt < 0 || at < bestAt {
					bestAt = at
					bestErr = fmt.Errorf("argument %s: not allowed with argument %s",
						p.actionName(name), p.actionName(sibling))
				}
				conflicted = true
				break
			}
			if conflicted {
				break
			}
			seen[name] = true
		}
	}
	return bestErr
}

// checkRequired is argparse's `required_actions` sweep: the `required=True`
// optionals first, in declaration order, then the positionals that could not be
// filled.
func (p *parser) checkRequired() error {
	var missing []string
	for _, name := range p.required {
		if !p.actionChanged(name) {
			missing = append(missing, p.actionName(name))
		}
	}
	missingPos, _ := p.matchPositionals()
	missing = append(missing, missingPos...)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("the following arguments are required: %s", strings.Join(missing, ", "))
}

// checkOneRequired is the `required=True` half of a mutually-exclusive group.
func (p *parser) checkOneRequired() error {
	for _, group := range p.oneRequired {
		satisfied := false
		for _, name := range group {
			if p.actionChanged(name) {
				satisfied = true
				break
			}
		}
		if satisfied {
			continue
		}
		names := make([]string, 0, len(group))
		for _, name := range group {
			names = append(names, p.actionName(name))
		}
		return fmt.Errorf("one of the arguments %s is required", strings.Join(names, " "))
	}
	return nil
}

// checkLeftovers is `parse_args`'s own check: whatever `parse_known_args` did
// not consume is an error, which is what makes `kathara wipe extra` exit 2
// instead of wiping.
func (p *parser) checkLeftovers() error {
	_, extra := p.matchPositionals()
	if len(extra) == 0 {
		return nil
	}
	return fmt.Errorf("unrecognized arguments: %s", strings.Join(extra, " "))
}

// matchPositionals assigns the leftover tokens to the declared positionals,
// reporting the ones that could not be filled and the tokens nothing claimed.
func (p *parser) matchPositionals() (missing, extra []string) {
	args := p.Flags().Args()
	i := 0
	for _, spec := range p.positionals {
		switch spec.kind {
		case nargsOne:
			if i < len(args) {
				i++
			} else {
				missing = append(missing, spec.metavar)
			}
		case nargsOneOrMore:
			if i < len(args) {
				i = len(args)
			} else {
				missing = append(missing, spec.metavar)
			}
		case nargsZeroOrMore, nargsRemainder:
			i = len(args)
		}
	}
	if i < len(args) {
		extra = args[i:]
	}
	return missing, extra
}

// optionOrder maps each action to the index of the token that first mentioned
// it, which is the only thing pflag throws away that an argparse error message
// needs.
func (p *parser) optionOrder(args []string) map[string]int {
	order := map[string]int{}
	record := func(name string, i int) {
		name = p.canonical(name)
		if _, seen := order[name]; !seen {
			order[name] = i
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !isOptionToken(arg) {
			continue
		}

		if strings.HasPrefix(arg, "--") {
			name := strings.TrimPrefix(arg, "--")
			attached := false
			if idx := strings.IndexByte(name, '='); idx >= 0 {
				name, attached = name[:idx], true
			}
			f := p.Flags().Lookup(name)
			if f == nil {
				continue
			}
			record(name, i)
			if !attached && f.NoOptDefVal == "" {
				i++
			}
			continue
		}

		body := arg[1:]
		for j := 0; j < len(body); j++ {
			f := p.Flags().ShorthandLookup(string(body[j]))
			if f == nil {
				break
			}
			record(f.Name, i)
			if f.NoOptDefVal == "" {
				// A value-taking shorthand ends the cluster: the rest of the
				// token is its value, or the next token is.
				if j == len(body)-1 {
					i++
				}
				break
			}
		}
	}
	return order
}

// helpMaxPosition is `HelpFormatter._max_help_position`.
const helpMaxPosition = 24

// helpWidth turns a console width into argparse's `HelpFormatter._width`, which
// is `shutil.get_terminal_size().columns - 2`.
func helpWidth(consoleWidth int) int {
	if consoleWidth < 13 {
		consoleWidth = 13
	}
	return consoleWidth - 2
}

// usage renders one sub-command's help exactly as `argparse.HelpFormatter`
// does: the `usage:` block, the description, the `positional arguments:` and
// `options:` sections, and the epilog — each separated by one blank line, each
// wrapped to the console width, with the help column at
// `min(longest invocation + 4, 24)`.
func (p *parser) usageAt(width int) string {
	w := helpWidth(width)
	var b strings.Builder
	b.WriteString(p.formatUsage(w))
	b.WriteString("\n")
	b.WriteString(fillText(p.Short, w))
	b.WriteString("\n\n")

	position := p.helpPosition()
	if len(p.positionals) > 0 {
		b.WriteString("\npositional arguments:\n")
		for _, spec := range p.positionals {
			b.WriteString(formatAction(spec.metavar, spec.help, position, w))
		}
		b.WriteString("\n")
	}

	b.WriteString("\noptions:\n")
	p.eachAction(func(name string) {
		f := p.Flags().Lookup(name)
		if f == nil || f.Hidden {
			return
		}
		b.WriteString(formatAction(p.invocation(name), f.Usage, position, w))
	})
	b.WriteString("\n")

	b.WriteString(fillText(wikiDescription, w))
	b.WriteString("\n\n")

	// `format_help`'s two final passes: collapse runs of blank lines, then
	// trim the block to exactly one trailing newline.
	out := collapseBlankLines(b.String())
	return strings.Trim(out, "\n") + "\n"
}

// usage renders at the default 80 columns, which is what `shutil` reports for a
// pipe and therefore what every non-terminal invocation sees.
func (p *parser) usage() string { return p.usageAt(80) }

// usageBlock is `ArgumentParser.format_usage()`: the `usage:` line alone.
func (p *parser) usageBlock() string { return p.formatUsage(helpWidth(80)) }

// helpPosition is `min(self._action_max_length + 2, self._max_help_position)`,
// where `_action_max_length` is the longest invocation plus the section indent.
func (p *parser) helpPosition() int {
	longest := 0
	measure := func(s string) {
		if len(s) > longest {
			longest = len(s)
		}
	}
	for _, spec := range p.positionals {
		measure(spec.metavar)
	}
	p.eachAction(func(name string) {
		f := p.Flags().Lookup(name)
		if f == nil || f.Hidden {
			return
		}
		measure(p.invocation(name))
	})
	if position := longest + 2 + 2; position < helpMaxPosition {
		return position
	}
	return helpMaxPosition
}

// formatAction is `HelpFormatter._format_action`: the invocation indented two
// columns, the help text folded into what is left of the width, and the
// invocation moved to a line of its own when it would reach the help column.
func formatAction(invocation, help string, position, width int) string {
	const indent = 2
	actionWidth := position - indent - 2
	hw := width - position
	if hw < 11 {
		hw = 11
	}

	lines := wrapText(help, hw)
	if len(lines) == 0 {
		// `if not action.help`: the header stands alone, unpadded.
		return fmt.Sprintf("%*s%s\n", indent, "", invocation)
	}

	var b strings.Builder
	indentFirst := 0
	if len(invocation) <= actionWidth {
		b.WriteString(fmt.Sprintf("%*s%-*s  ", indent, "", actionWidth, invocation))
	} else {
		b.WriteString(fmt.Sprintf("%*s%s\n", indent, "", invocation))
		indentFirst = position
	}

	b.WriteString(fmt.Sprintf("%*s%s\n", indentFirst, "", lines[0]))
	for _, line := range lines[1:] {
		b.WriteString(fmt.Sprintf("%*s%s\n", position, "", line))
	}
	return b.String()
}

// formatUsage is `HelpFormatter._format_usage`, including the wrap that hangs
// the continuation lines under the program name.
func (p *parser) formatUsage(width int) string {
	const prefix = "usage: "
	prog := p.Use
	optParts, posParts := p.usageParts()
	parts := append(append([]string{}, optParts...), posParts...)
	usage := strings.TrimSpace(prog + " " + strings.Join(parts, " "))

	if len(prefix)+len(usage) <= width {
		return prefix + usage + "\n"
	}

	// getLines is argparse's local helper: greedy packing, every line but the
	// first carrying the hanging indent.
	getLines := func(parts []string, indent string, hasPrefix bool) []string {
		var lines []string
		var line []string
		lineLen := len(indent) - 1
		if hasPrefix {
			lineLen = len(prefix) - 1
		}
		for _, part := range parts {
			if lineLen+1+len(part) > width && len(line) > 0 {
				lines = append(lines, indent+strings.Join(line, " "))
				line = nil
				lineLen = len(indent) - 1
			}
			line = append(line, part)
			lineLen += 1 + len(part)
		}
		if len(line) > 0 {
			lines = append(lines, indent+strings.Join(line, " "))
		}
		if hasPrefix && len(lines) > 0 {
			lines[0] = lines[0][len(indent):]
		}
		return lines
	}

	var lines []string
	if float64(len(prefix)+len(prog)) <= 0.75*float64(width) {
		indent := strings.Repeat(" ", len(prefix)+len(prog)+1)
		switch {
		case len(optParts) > 0:
			lines = getLines(append([]string{prog}, optParts...), indent, true)
			lines = append(lines, getLines(posParts, indent, false)...)
		case len(posParts) > 0:
			lines = getLines(append([]string{prog}, posParts...), indent, true)
		default:
			lines = []string{prog}
		}
	} else {
		indent := strings.Repeat(" ", len(prefix))
		lines = getLines(parts, indent, false)
		if len(lines) > 1 {
			lines = getLines(optParts, indent, false)
			lines = append(lines, getLines(posParts, indent, false)...)
		}
		lines = append([]string{prog}, lines...)
	}
	return prefix + strings.Join(lines, "\n") + "\n"
}

// usageParts is `HelpFormatter._get_actions_usage_parts`: one bracketed part
// per action. A mutually-exclusive group stays as one part *per member*, with
// the bracket on the first and last and a trailing `|` on all but the last —
// which is what lets the usage wrap split a group across two lines, as
// `kathara vconfig`'s does.
func (p *parser) usageParts() (optParts, posParts []string) {
	groupOf := map[string]int{}
	for i, group := range p.exclusive {
		for _, name := range group {
			groupOf[name] = i
		}
	}
	requiredGroup := map[int]bool{}
	for i, group := range p.exclusive {
		for _, one := range p.oneRequired {
			if sameGroup(group, one) {
				requiredGroup[i] = true
			}
		}
	}
	required := map[string]bool{}
	for _, name := range p.required {
		required[name] = true
	}

	emitted := map[int]bool{}
	p.eachAction(func(name string) {
		idx, grouped := groupOf[name]
		if !grouped {
			part := p.usageInvocation(name)
			if !required[name] {
				part = "[" + part + "]"
			}
			optParts = append(optParts, part)
			return
		}
		if emitted[idx] {
			return
		}
		emitted[idx] = true
		members := make([]string, 0, len(p.exclusive[idx]))
		for _, member := range p.exclusive[idx] {
			members = append(members, p.usageInvocation(member))
		}
		open, closing := "[", "]"
		if requiredGroup[idx] {
			open, closing = "(", ")"
			if len(members) == 1 {
				open, closing = "", ""
			}
		}
		members[0] = open + members[0]
		last := len(members) - 1
		members[last] += closing
		for i := 0; i < last; i++ {
			members[i] += " |"
		}
		optParts = append(optParts, members...)
	})

	for _, spec := range p.positionals {
		posParts = append(posParts, positionalArgs(spec))
	}
	return optParts, posParts
}

// usageInvocation is the usage line's view of an action: its FIRST spelling
// plus its argument shape.
func (p *parser) usageInvocation(name string) string {
	first := p.optionStrings(name)[0]
	if args := p.optionArgs(name); args != "" {
		return first + " " + args
	}
	return first
}

// invocation is `HelpFormatter._format_action_invocation`: every spelling,
// comma-separated, with the argument shape once at the end.
func (p *parser) invocation(name string) string {
	joined := strings.Join(p.optionStrings(name), ", ")
	if args := p.optionArgs(name); args != "" {
		return joined + " " + args
	}
	return joined
}

// optionArgs is `_format_args` for an optional. An option with no metavar is
// `action='store_true'`/`'store_const'`, i.e. `nargs == 0`, and shows nothing.
func (p *parser) optionArgs(name string) string {
	metavar, ok := p.metavars[name]
	if !ok {
		return ""
	}
	switch p.listKind[name] {
	case nargsOneOrMore:
		return metavar + " [" + metavar + " ...]"
	case nargsZeroOrMore:
		return "[" + metavar + " ...]"
	}
	return metavar
}

// positionalArgs is `_format_args` for a positional.
func positionalArgs(spec positional) string {
	switch spec.kind {
	case nargsOneOrMore:
		return spec.metavar + " [" + spec.metavar + " ...]"
	case nargsZeroOrMore:
		return "[" + spec.metavar + " ...]"
	case nargsRemainder:
		return "..."
	}
	return spec.metavar
}

// eachAction walks the flag set in declaration order — which is what
// `SortFlags = false` buys — calling fn once per argparse action, i.e. skipping
// the extra pflag flags that only exist to spell an alias.
func (p *parser) eachAction(fn func(name string)) {
	p.Flags().VisitAll(func(f *pflag.Flag) {
		if _, isAlias := p.aliasFor[f.Name]; isAlias {
			return
		}
		fn(f.Name)
	})
}

// sameGroup reports whether two group member lists are the same group.
func sameGroup(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fillText is `HelpFormatter._fill_text` at indent zero: collapse the
// whitespace, then fold.
func fillText(text string, width int) string {
	return strings.Join(wrapText(text, width), "\n")
}

// wrapText is `textwrap.wrap` with argparse's settings: whitespace collapsed
// first, then greedy packing, then a hard split for a word that cannot fit at
// all.
func wrapText(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	line := ""
	flush := func() {
		if line != "" {
			lines = append(lines, line)
			line = ""
		}
	}
	for _, word := range words {
		for len(word) > width {
			flush()
			lines = append(lines, word[:width])
			word = word[width:]
		}
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= width:
			line += " " + word
		default:
			flush()
			line = word
		}
	}
	flush()
	return lines
}

// collapseBlankLines is `format_help`'s `_long_break_matcher.sub('\n\n', help)`.
func collapseBlankLines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runLen := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			runLen++
			if runLen <= 2 {
				b.WriteByte('\n')
			}
			continue
		}
		runLen = 0
		b.WriteByte(s[i])
	}
	return b.String()
}
