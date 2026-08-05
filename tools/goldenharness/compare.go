package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileDiff describes one differing snapshot file.
type FileDiff struct {
	Path   string // snapshot-relative
	Reason string // "only_in_want" | "only_in_got" | "content"
	Detail string
}

// DirDiff is the comparison of two snapshot directories.
type DirDiff struct {
	Scenario string
	Files    []FileDiff
}

// Empty reports whether the two directories are identical.
func (d DirDiff) Empty() bool { return len(d.Files) == 0 }

// CompareDirs compares two snapshot trees. want is the stored golden, got is
// the freshly recorded tree.
func CompareDirs(scenario, wantDir, gotDir string, maxDiffLines int) (DirDiff, error) {
	out := DirDiff{Scenario: scenario}

	wantFiles, err := listFiles(wantDir)
	if err != nil {
		return out, err
	}
	gotFiles, err := listFiles(gotDir)
	if err != nil {
		return out, err
	}

	all := map[string]bool{}
	for f := range wantFiles {
		all[f] = true
	}
	for f := range gotFiles {
		all[f] = true
	}
	names := make([]string, 0, len(all))
	for f := range all {
		names = append(names, f)
	}
	sort.Strings(names)

	for _, name := range names {
		_, inWant := wantFiles[name]
		_, inGot := gotFiles[name]
		switch {
		case inWant && !inGot:
			out.Files = append(out.Files, FileDiff{Path: name, Reason: "only_in_want"})
		case !inWant && inGot:
			out.Files = append(out.Files, FileDiff{Path: name, Reason: "only_in_got"})
		default:
			wb, err := os.ReadFile(filepath.Join(wantDir, name))
			if err != nil {
				return out, fmt.Errorf("read %s: %w", name, err)
			}
			gb, err := os.ReadFile(filepath.Join(gotDir, name))
			if err != nil {
				return out, fmt.Errorf("read %s: %w", name, err)
			}
			if string(wb) == string(gb) {
				continue
			}
			out.Files = append(out.Files, FileDiff{
				Path:   name,
				Reason: "content",
				Detail: lineDiff(string(wb), string(gb), maxDiffLines),
			})
		}
	}
	return out, nil
}

func listFiles(dir string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}
	return out, nil
}

// lineDiff produces a compact diff: the common prefix and suffix are trimmed
// and the differing middle is printed as -want/+got. Snapshot files are small
// and highly structured, so this is enough to point at the divergence.
func lineDiff(want, got string, maxLines int) string {
	w := strings.Split(strings.TrimRight(want, "\n"), "\n")
	g := strings.Split(strings.TrimRight(got, "\n"), "\n")

	pre := 0
	for pre < len(w) && pre < len(g) && w[pre] == g[pre] {
		pre++
	}
	suf := 0
	for suf < len(w)-pre && suf < len(g)-pre && w[len(w)-1-suf] == g[len(g)-1-suf] {
		suf++
	}

	wm := w[pre : len(w)-suf]
	gm := g[pre : len(g)-suf]

	var b strings.Builder
	fmt.Fprintf(&b, "@@ line %d @@\n", pre+1)
	written := 0
	for _, l := range wm {
		if written >= maxLines {
			fmt.Fprintf(&b, "  ... %d more want line(s)\n", len(wm)-written)
			break
		}
		fmt.Fprintf(&b, "- %s\n", l)
		written++
	}
	written = 0
	for _, l := range gm {
		if written >= maxLines {
			fmt.Fprintf(&b, "  ... %d more got line(s)\n", len(gm)-written)
			break
		}
		fmt.Fprintf(&b, "+ %s\n", l)
		written++
	}
	return b.String()
}

// PrintDiff renders a DirDiff to w-style output on stdout.
func PrintDiff(d DirDiff, out *strings.Builder) {
	fmt.Fprintf(out, "MISMATCH %s (%d file(s))\n", d.Scenario, len(d.Files))
	for _, f := range d.Files {
		switch f.Reason {
		case "only_in_want":
			fmt.Fprintf(out, "  - missing in recording: %s\n", f.Path)
		case "only_in_got":
			fmt.Fprintf(out, "  + unexpected in recording: %s\n", f.Path)
		default:
			fmt.Fprintf(out, "  ~ %s\n", f.Path)
			for _, line := range strings.Split(strings.TrimRight(f.Detail, "\n"), "\n") {
				fmt.Fprintf(out, "      %s\n", line)
			}
		}
	}
}
