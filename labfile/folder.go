package labfile

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/model"
)

// ParseFolder is `FolderParser.parse` (`parser/netkit/FolderParser.py:12`):
// every subdirectory of the scenario directory is a device.
func ParseFolder(path string, defaults model.Defaults) (*model.Lab, error) {
	lab, err := model.NewLabFromPath(path, defaults)
	if err != nil {
		return nil, err
	}

	for _, name := range machineFolders(path) {
		if util.IsReservedMachineName(name) {
			continue
		}
		if _, err := lab.GetOrNewMachine(name, nil); err != nil {
			return nil, err
		}
	}

	return lab, nil
}

// machineFolders is `glob("%s/*/" % path)` reduced to the directory names, in
// sorted order.
func machineFolders(path string) []string {
	names := []string{}

	for _, dir := range globDirs(path + "/*") {
		// `_glob0` with the empty basename the trailing slash leaves behind:
		// the expansion survives only if it is a directory, tested with
		// `os.path.isdir`, which FOLLOWS symlinks — so a symlink to a directory
		// is a device under both its names, and a symlink to a file or to
		// nothing is not (oracle-verified).
		if !isDirFollowingLinks(dir) {
			continue
		}
		// `os.path.basename(machine_folder[:-1])`, where `machine_folder` is
		// `os.path.join(dir, "")`, i.e. dir plus the separator the slice then
		// removes. No expansion ends in a separator, so the pair is the last
		// component of dir.
		names = append(names, pyBasename(dir))
	}

	slices.Sort(names)
	return names
}

// ---------------------------------------------------------------------------
// `glob`
// ---------------------------------------------------------------------------

// globDirs is CPython's `glob._iglob(pattern, "", None, False, dironly=True)`:
// the directories a pattern expands to, each level filtered to directories
// because the caller is going to descend into it.
func globDirs(pattern string) []string {
	dirname, basename := pySplitPath(pattern)

	if dirname == "" {
		// A single-component pattern is matched against the working directory,
		// and the names come back relative to it.
		return globMatchNames(globListDirs(""), basename)
	}

	// `if dirname != pathname and has_magic(dirname)`. The inequality is
	// CPython's guard against the infinite recursion a Windows drive or UNC
	// prefix — which `split` returns as its own dirname — would otherwise cause.
	var dirs []string
	if dirname != pattern && hasMagic(dirname) {
		dirs = globDirs(dirname)
	} else {
		dirs = []string{dirname}
	}

	out := []string{}
	for _, dir := range dirs {
		if hasMagic(basename) {
			// `_glob1`: list and filter.
			for _, name := range globMatchNames(globListDirs(dir), basename) {
				out = append(out, pyJoinPath(dir, name))
			}
			continue
		}
		// `_glob0`: a literal component is kept if it merely EXISTS. The test is
		// `lexists`, which does not follow symlinks and does not ask for a
		// directory, so a file survives this level and is dropped by the next
		// one, whose listing of it fails and is swallowed.
		// The empty-basename arm of `_glob0` is unreachable from here: a pattern
		// ending in a separator would be needed, and `split` only produces one
		// as a dirname made entirely of separators, which has no magic and so is
		// never recursed into.
		if basename == "" {
			continue
		}
		joined := pyJoinPath(dir, basename)
		if _, err := os.Lstat(joined); err == nil {
			out = append(out, joined)
		}
	}
	return out
}

// globListDirs is `glob._listdir(dirname, None, dironly=True)`: the entries of
// a directory that are themselves directories, symlinks to one included.
func globListDirs(dir string) []string {
	target := dir
	if target == "" {
		target = "."
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		// `entry.is_dir()` follows symlinks and swallows its own OSError, which
		// is [os.Stat] and its error here. [os.DirEntry.IsDir] would not follow.
		if isDirFollowingLinks(pyJoinPath(dir, entry.Name())) {
			names = append(names, entry.Name())
		}
	}
	return names
}

// globMatchNames is the second half of `glob._glob1`: drop the hidden entries
// unless the pattern is itself hidden, then `fnmatch.filter`.
func globMatchNames(names []string, pattern string) []string {
	hiddenPattern := strings.HasPrefix(pattern, ".")
	match := newGlobMatcher(pattern)

	out := []string{}
	for _, name := range names {
		if !hiddenPattern && strings.HasPrefix(name, ".") {
			continue
		}
		if match(name) {
			out = append(out, name)
		}
	}
	return out
}

// newGlobMatcher is `fnmatch._compile_pattern` plus `fnmatch.filter`'s
// `os.path.normcase` on both sides of the comparison.
func newGlobMatcher(pattern string) func(string) bool {
	expr, ok := fnTranslate(pyNormcase(pattern))
	if !ok {
		return func(string) bool { return false }
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return func(string) bool { return false }
	}
	return func(name string) bool { return re.MatchString(pyNormcase(name)) }
}

// fnTranslate is `fnmatch.translate` (CPython 3.13, the interpreter the vector
// corpus is recorded against), emitting an RE2 source instead of a CPython one.
// The second result is false for a pattern that can never match.
func fnTranslate(pattern string) (string, bool) {
	pat := []rune(pattern)
	n := len(pat)

	// A piece is either one `*` or a fragment of regex source, which is how
	// CPython keeps the STAR sentinel apart from the text around it.
	type piece struct {
		star bool
		text string
	}
	var res []piece
	add := func(text string) { res = append(res, piece{text: text}) }

	for i := 0; i < n; {
		c := pat[i]
		i++

		switch {
		case c == '*':
			// Consecutive stars collapse into one.
			if len(res) == 0 || !res[len(res)-1].star {
				res = append(res, piece{star: true})
			}

		case c == '?':
			add(".")

		case c == '[':
			// Find the closing bracket. A `!` right after the `[` negates, and
			// a `]` right after either of them is a member rather than the end.
			j := i
			if j < n && pat[j] == '!' {
				j++
			}
			if j < n && pat[j] == ']' {
				j++
			}
			for j < n && pat[j] != ']' {
				j++
			}
			if j >= n {
				// An unclosed `[` is a literal one.
				add(`\[`)
				break
			}

			var stuff string
			if body := string(pat[i:j]); !strings.ContainsRune(body, '-') {
				stuff = strings.ReplaceAll(body, `\`, `\\`)
			} else {
				// The body is split at every `-` that can start a range, and a
				// range whose ends are inverted is folded away instead of being
				// handed to the regex engine, which would reject it.
				var chunks []string
				k := i + 1
				if pat[i] == '!' {
					k = i + 2
				}
				for {
					m := findRune(pat, '-', k, j)
					if m < 0 {
						break
					}
					chunks = append(chunks, string(pat[i:m]))
					i = m + 1
					k = m + 3
				}
				if chunk := string(pat[i:j]); chunk != "" {
					chunks = append(chunks, chunk)
				} else if len(chunks) > 0 {
					// CPython indexes `chunks[-1]` unguarded; the guard is here
					// because a panic is not an option, not because the empty
					// case is reachable — the first `-` of a body always leaves
					// a chunk behind.
					chunks[len(chunks)-1] += "-"
				}
				for k := len(chunks) - 1; k > 0; k-- {
					prev, cur := []rune(chunks[k-1]), []rune(chunks[k])
					if len(prev) == 0 || len(cur) == 0 {
						// The same unguarded `chunks[k][0]` as above, and the
						// same reason for guarding it: 300k differential
						// patterns produce no empty chunk here.
						continue
					}
					if prev[len(prev)-1] > cur[0] {
						chunks[k-1] = string(prev[:len(prev)-1]) + string(cur[1:])
						chunks = slices.Delete(chunks, k, k+1)
					}
				}
				for idx, chunk := range chunks {
					chunk = strings.ReplaceAll(chunk, `\`, `\\`)
					chunks[idx] = strings.ReplaceAll(chunk, "-", `\-`)
				}
				stuff = strings.Join(chunks, "-")
			}
			i = j + 1

			switch {
			case stuff == "":
				// An empty range: `(?!)`, which never matches.
				return "", false
			case stuff == "!":
				// A negated empty range: any character.
				add(".")
			default:
				if stuff[0] == '!' {
					stuff = "^" + stuff[1:]
				} else if stuff[0] == '^' || stuff[0] == '[' {
					stuff = `\` + stuff
				}
				add("[" + re2ClassBody(stuff) + "]")
			}

		default:
			add(regexp.QuoteMeta(string(c)))
		}
	}

	var expr strings.Builder
	expr.WriteString(`\A(?s:`)
	for _, p := range res {
		if p.star {
			expr.WriteString(".*")
		} else {
			expr.WriteString(p.text)
		}
	}
	expr.WriteString(`)\z`)
	return expr.String(), true
}

// re2ClassBody escapes the two characters `fnmatch.translate` leaves bare
// because CPython's `re` reads them as members: a `]` — legal as the first one
// — and a `[`, which RE2 may read as the start of a POSIX class. Sequences the
// translation already escaped are copied through untouched.
func re2ClassBody(stuff string) string {
	var out strings.Builder
	for i := 0; i < len(stuff); i++ {
		switch c := stuff[i]; {
		case c == '\\' && i+1 < len(stuff):
			out.WriteByte(c)
			i++
			out.WriteByte(stuff[i])
		case c == ']' || c == '[':
			out.WriteByte('\\')
			out.WriteByte(c)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// findRune is `str.find(sub, start, end)` for a single character: the index of
// the first r in pat[start:end], or -1.
func findRune(pat []rune, r rune, start, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(pat) {
		end = len(pat)
	}
	for i := start; i < end; i++ {
		if pat[i] == r {
			return i
		}
	}
	return -1
}

// hasMagic is `glob.magic_check.search`: the three characters that make a
// component a pattern rather than a name.
func hasMagic(s string) bool { return strings.ContainsAny(s, "*?[") }

// ---------------------------------------------------------------------------
// `os.path` and `posixpath`/`ntpath`
// ---------------------------------------------------------------------------

// pySplitPath is `os.path.split`: everything up to the last separator, with the
// trailing separators removed unless the head is nothing but separators, and
// everything after it.
func pySplitPath(p string) (head, tail string) {
	volume := filepath.VolumeName(p)
	rest := p[len(volume):]

	cut := 0
	for i := len(rest) - 1; i >= 0; i-- {
		if os.IsPathSeparator(rest[i]) {
			cut = i + 1
			break
		}
	}

	head, tail = rest[:cut], rest[cut:]
	if trimmed := strings.TrimRightFunc(head, func(r rune) bool {
		return r < 0x80 && os.IsPathSeparator(byte(r))
	}); trimmed != "" {
		head = trimmed
	}
	return volume + head, tail
}

// pyBasename is `os.path.basename`, i.e. the tail of [pySplitPath].
func pyBasename(p string) string {
	_, name := pySplitPath(p)
	return name
}

// pyJoinPath is `os.path.join(a, b)` for the two-argument case `glob` makes: an
// absolute b replaces a, and an a that is empty, that already ends with a
// separator, or that is a bare Windows drive does not get one added.
func pyJoinPath(a, b string) string {
	if b != "" && os.IsPathSeparator(b[0]) {
		return b
	}
	if a == "" || os.IsPathSeparator(a[len(a)-1]) ||
		(filepath.VolumeName(a) == a && strings.HasSuffix(a, ":")) {
		return a + b
	}
	return a + string(filepath.Separator) + b
}

// pyNormcase is `os.path.normcase`: the identity on POSIX, and on Windows a
// case fold plus `/` → `\`, which is what makes `fnmatch` case-insensitive
// there.
func pyNormcase(s string) string {
	if runtime.GOOS != "windows" {
		return s
	}
	return strings.ToLower(strings.ReplaceAll(s, "/", `\`))
}

// isDirFollowingLinks is `os.path.isdir`: a stat that follows symlinks, and
// false for every error it can fail with.
func isDirFollowingLinks(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
