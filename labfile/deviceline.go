package labfile

// The lab.conf device-line pattern, `LabParser.py:44-47`:
//
//	^(?P<key>[a-z0-9_]{1,30})\[(?P<arg>\w+)\]=([\"\']?)(?P<value>[^\"\']+)(\3)(\s+\#.*)?$
//
// applied to `line.strip()`. Group 3 is an optional opening quote and `(\3)` is
// a BACKREFERENCE to it, which RE2 cannot express — PACKAGE_GRAPH.md §6 and
// PORT_SPEC §6 both single this out as the one place the port hand-rolls a
// regex. What follows is that pattern as a matcher, backtracking included.
//
// Backtracking is not a formality here. Two of its consequences are what the
// vector corpus pins:
//
//   - The value class is greedy and contains `#` and spaces, so on an UNQUOTED
//     value the optional comment group can never fire and the comment is
//     swallowed into the value: `pc1[image]=kathara/frr # note` stores the whole
//     tail, and the same swallow on an interface line surfaces as ``Collision
//     domain `A # comment` contains non-alphanumeric characters.``
//     (README SURPRISE 8, DIVERGENCES.md 4.)
//   - When an unquoted value is followed by a quote later in the line, the
//     engine gives value characters back one at a time looking for a ` #` it can
//     hand to the comment group. `pc1[image]=abc #x"y` therefore matches with
//     value `abc` — a match a non-backtracking rewrite would miss.
//
// Quote handling is: the opening quote is optional, the closing quote must be
// the SAME character, and the value between them can contain neither. So
// `pc1[0]='A"` and `pc1[0]='A` fail, and `pc1[image]="ka"tha"ra"` fails rather
// than losing its inner quotes (README SURPRISE 4).

// matchDeviceLine runs that pattern against an already-stripped line.
//
// The line is handled as runes because two of the quantifiers count characters:
// the device name is `{1,30}` and `\w` is Unicode.
func matchDeviceLine(s string) (deviceLine, bool) {
	r := []rune(s)
	n := len(r)

	// `^(?P<key>[a-z0-9_]{1,30})\[`
	//
	// The class cannot match `[`, so greedy-then-backtrack degenerates to a
	// single answer: the maximal run from position 0 must be 1 to 30 characters
	// AND be followed immediately by `[`. A 31-character name is a syntax
	// error, not a "bad device name" one, because backtracking to 30 leaves
	// another name character where the `[` has to be.
	i := 0
	for i < n && isDeviceNameRune(r[i]) {
		i++
	}
	if i == 0 || i > 30 || i >= n || r[i] != '[' {
		return deviceLine{}, false
	}
	key := string(r[:i])
	i++

	// `(?P<arg>\w+)\]=` — same reasoning: `]` is not a word character.
	j := i
	for j < n && isWordRune(r[j]) {
		j++
	}
	if j == i || j >= n || r[j] != ']' {
		return deviceLine{}, false
	}
	arg := string(r[i:j])
	j++
	if j >= n || r[j] != '=' {
		return deviceLine{}, false
	}
	p := j + 1

	// `([\"\']?)` — greedy, so the quote is tried before the empty
	// alternative. quotes holds the alternatives in that preference order,
	// with 0 standing for "no quote".
	quotes := make([]rune, 0, 2)
	if p < n && isQuote(r[p]) {
		quotes = append(quotes, r[p])
	}
	quotes = append(quotes, 0)

	for _, quote := range quotes {
		start := p
		if quote != 0 {
			start++
		}

		// `(?P<value>[^\"\']+)` — greedy up to the next quote or the end.
		limit := start
		for limit < n && !isQuote(r[limit]) {
			limit++
		}

		// Give the value back one character at a time, longest first, and try
		// `(\3)(\s+\#.*)?$` at each length. The value needs at least one
		// character, which is why `pc1[image]=` and `pc1[0]=''` are syntax
		// errors while `LAB_DESCRIPTION=` is fine (README SURPRISE 25).
		for end := limit; end > start; end-- {
			rest := end
			if quote != 0 {
				// The backreference. Every character of the value run is a
				// non-quote, so this can only ever succeed at `limit` — but
				// spelling it as a test keeps the loop the pattern's shape.
				if end >= n || r[end] != quote {
					continue
				}
				rest++
			}
			if matchCommentTail(r[rest:]) {
				return deviceLine{key: key, arg: arg, value: string(r[start:end])}, true
			}
		}
	}

	return deviceLine{}, false
}

// matchCommentTail is `(\s+\#.*)?$` against the remainder of the line.
//
// The optional group is greedy, so a trailing comment is preferred over an
// empty match. Inside it, `\s+` is greedy and `#` is not whitespace, so the
// only length that can work is the full whitespace run: the tail matches when
// it starts with at least one space and its first non-space character is `#`.
// A comment glued to the closing quote (`'A'# c`) has no whitespace and fails,
// which is why that spelling is a syntax error.
func matchCommentTail(rest []rune) bool {
	spaces := 0
	for spaces < len(rest) && pySpace(rest[spaces]) {
		spaces++
	}
	if spaces > 0 && spaces < len(rest) && rest[spaces] == '#' {
		// `.*` stops at a newline and `$` matches only at the end of the string
		// or immediately before a final one. A stripped line has neither, so
		// this is a formality that keeps the two engines' languages equal.
		body := rest[spaces+1:]
		newline := -1
		for k, c := range body {
			if c == '\n' {
				newline = k
				break
			}
		}
		if newline < 0 || newline == len(body)-1 {
			return true
		}
	}
	// The empty alternative, which then requires `$` right here.
	return len(rest) == 0
}
