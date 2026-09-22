//  1. `nargs='+'` / `nargs='*'` on an *option*. Python's `--exclude a b c` and
//     `-o k=v k2=v2` consume the whole following run of non-option tokens
//     (oracle-probed: `--exclude a b pc1` swallows `pc1`, leaving the
//     positional list empty). pflag takes one value per occurrence, so
//     [expandGreedy] rewrites the run into repetitions before pflag sees it.
//  2. `nargs=argparse.REMAINDER` on `vstart`'s trailing `ARG ...`. Everything
//     from the first positional onwards is captured verbatim, options
//     included, and the leading `--` is *kept* — which is why
//     `VstartCommand.run` strips it by hand (`VstartCommand.py:228`).
//     [splitRemainder] finds that cut.
// What is deliberately NOT reproduced is argparse's prefix matching, i.e.

package main

import (
	"strconv"
	"strings"
)

// greedyKind is how many values an option-with-nargs accepts.
type greedyKind int

const (
	// greedyOneOrMore is `nargs='+'`: at least one value, error otherwise.
	greedyOneOrMore greedyKind = iota
	// greedyZeroOrMore is `nargs='*'`: the option may appear bare, and then
	// the parsed value is the empty list rather than absent.
	greedyZeroOrMore
)

// greedySpec names one option that consumes a run of values, by its long name
// and its optional shorthand.
type greedySpec struct {
	long  string
	short string
	kind  greedyKind
}

// emptyListSentinel is what a bare `nargs='*'` option sets. pflag's
// `NoOptDefVal` needs a non-empty string, and the empty *value* is a legitimate
// item (`-o =v` yields the pair `("", "v")`), so the sentinel has to be a
// string no shell can produce.
const emptyListSentinel = "\x00kathara-empty-list\x00"

// isOptionToken is argparse's `_parse_optional` reduced to the question
// [expandGreedy] asks: does this token end the run of values?
func isOptionToken(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	if strings.ContainsRune(s, ' ') {
		return false
	}
	if isNegativeNumber(s) {
		return false
	}
	return true
}

// isNegativeNumber is argparse's `_negative_number_matcher`, `^-\d+$|^-\d*\.\d+$`.
func isNegativeNumber(s string) bool {
	if len(s) < 2 || s[0] != '-' {
		return false
	}
	body := s[1:]
	if _, err := strconv.Atoi(body); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(body, 64); err == nil {
		return strings.Contains(body, ".")
	}
	return false
}

// expandGreedy rewrites the option runs of specs into one `--opt=value` per
// value, so that pflag's one-value-per-occurrence parsing produces the list
// argparse's `nargs` would.
func expandGreedy(args []string, specs []greedySpec) []string {
	if len(specs) == 0 {
		return args
	}
	byLong := make(map[string]greedySpec, len(specs))
	byShort := make(map[string]greedySpec, len(specs))
	for _, s := range specs {
		byLong["--"+s.long] = s
		if s.short != "" {
			byShort["-"+s.short] = s
		}
	}

	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}

		spec, ok := byLong[arg]
		if !ok {
			spec, ok = byShort[arg]
		}
		if !ok {
			// `--opt=value` and `-ovalue` already carry exactly one value,
			// which is what argparse gives them too.
			out = append(out, arg)
			continue
		}

		values := make([]string, 0, 2)
		for i+1 < len(args) && args[i+1] != "--" && !isOptionToken(args[i+1]) {
			i++
			values = append(values, args[i])
		}
		if len(values) == 0 {
			if spec.kind == greedyZeroOrMore {
				out = append(out, "--"+spec.long+"="+emptyListSentinel)
				continue
			}
			// `nargs='+'` with nothing after it: let pflag report the missing
			// argument, which is argparse's "expected at least one argument"
			// class and the same exit 2.
			out = append(out, arg)
			continue
		}
		for _, v := range values {
			out = append(out, "--"+spec.long+"="+v)
		}
	}
	return out
}

// valueTaking is the set of options that consume the following token, which
// [splitRemainder] needs in order to tell a flag's value from the first
// positional.
type valueTaking struct {
	long  map[string]bool
	short map[string]bool
}

// splitRemainder implements `nargs=argparse.REMAINDER` for `vstart`: everything
// from the first positional token — or from a bare `--` — onwards is the
// remainder, verbatim and including any option-looking tokens.
func splitRemainder(args []string, taking valueTaking, greedy []greedySpec) (head, remainder []string) {
	greedyLong := map[string]bool{}
	greedyShort := map[string]bool{}
	for _, g := range greedy {
		greedyLong[g.long] = true
		if g.short != "" {
			greedyShort[g.short] = true
		}
	}

	// skipRun advances past the whole run of values a greedy option consumes.
	skipRun := func(i int) int {
		for i+1 < len(args) && args[i+1] != "--" && !isOptionToken(args[i+1]) {
			i++
		}
		return i
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || !isOptionToken(arg) {
			return args[:i], args[i:]
		}

		if strings.HasPrefix(arg, "--") {
			name := strings.TrimPrefix(arg, "--")
			if idx := strings.IndexByte(name, '='); idx >= 0 {
				continue
			}
			switch {
			case greedyLong[name]:
				i = skipRun(i)
			case taking.long[name]:
				i++
			}
			continue
		}

		// A shorthand cluster. Every letter up to the first value-taking one is
		// a valueless option; that one ends the cluster, taking either the rest
		// of the token or — when it is the last letter — the token after it.
		// `vstart -Hn pc1 ls` is `-H` plus `-n pc1`, so the remainder starts at
		// `ls` and not at `pc1`.
		body := arg[1:]
		for j := 0; j < len(body); j++ {
			short := body[j : j+1]
			if short == "=" {
				break
			}
			last := j == len(body)-1
			if greedyShort[short] {
				if last {
					i = skipRun(i)
				}
				break
			}
			if taking.short[short] {
				if last {
					i++
				}
				break
			}
		}
	}
	return args, nil
}
