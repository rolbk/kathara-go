package docker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/KatharaFramework/kathara-go/model"
)

// packedMember is one archive entry, reduced to what the container sees.
type packedMember struct {
	Name    string
	IsDir   bool
	Mode    int64
	Content string
}

// unpack reads back what [packData] produced.
func unpack(t *testing.T, data []byte) []packedMember {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer func() { _ = zr.Close() }()

	var members []packedMember
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read member %q: %v", header.Name, err)
		}
		members = append(members, packedMember{
			Name:    header.Name,
			IsDir:   header.Typeflag == tar.TypeDir,
			Mode:    header.Mode,
			Content: string(body),
		})
	}
	return members
}

// newLabOnDisk builds a scenario rooted at a temp directory, which is what
// gives its devices a real filesystem to pack.
func newLabOnDisk(t *testing.T) *model.Lab {
	t.Helper()

	lab, err := model.NewLabWithPath("Default scenario", t.TempDir(), model.DefaultDefaults())
	if err != nil {
		t.Fatalf("NewLabWithPath: %v", err)
	}
	return lab
}

func writeLabFile(t *testing.T, lab *model.Lab, name, content string) {
	t.Helper()

	path, ok := lab.FSPath()
	if !ok {
		t.Fatal("scenario has no host path")
	}
	full := filepath.Join(path, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestPackDataIsNilWhenThereIsNothingToShip is `pack_data`'s `return None`: a
// device with no directory and no scenario files produces no archive at all,
// and `create` then skips the `put_archive` entirely.
func TestPackDataIsNilWhenThereIsNothingToShip(t *testing.T) {
	lab := newLabOnDisk(t)
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}

	data, err := packData(machine)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	if data != nil {
		t.Errorf("packData produced %d bytes for an empty device, want nil", len(data))
	}
}

// TestPackDataLayout is the archive's shape: the device's own files under
// `hostlab/{name}/` and the four scenario files under `hostlab/`, which is what
// the startup script reads.
func TestPackDataLayout(t *testing.T) {
	lab := newLabOnDisk(t)
	writeLabFile(t, lab, "pc1/etc/hosts", "127.0.0.1 pc1\n")
	writeLabFile(t, lab, "pc1/root/.bashrc", "alias l=ls\n")
	writeLabFile(t, lab, "pc1.startup", "ip a\n")
	writeLabFile(t, lab, "shared.shutdown", "echo bye\n")
	// Not one of the four; must NOT be picked up.
	writeLabFile(t, lab, "other.startup", "nope\n")

	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}

	data, err := packData(machine)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	if data == nil {
		t.Fatal("packData produced nothing")
	}

	members := unpack(t, data)
	names := make([]string, 0, len(members))
	for _, member := range members {
		names = append(names, member.Name)
	}

	for _, want := range []string{
		"hostlab/",
		"hostlab/pc1/",
		"hostlab/pc1/etc/",
		"hostlab/pc1/etc/hosts",
		"hostlab/pc1/root/",
		"hostlab/pc1/root/.bashrc",
		"hostlab/pc1.startup",
		"hostlab/shared.shutdown",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("missing member %q; got %q", want, names)
		}
	}
	if slices.Contains(names, "hostlab/other.startup") {
		t.Error("a scenario file outside the four fixed names was packed")
	}

	for _, member := range members {
		switch member.Name {
		case "hostlab/pc1/etc/hosts":
			if member.Content != "127.0.0.1 pc1\n" {
				t.Errorf("device file content = %q", member.Content)
			}
			if member.Mode != packFileMode {
				t.Errorf("file mode = %o, want %o", member.Mode, packFileMode)
			}
		case "hostlab/", "hostlab/pc1/":
			if !member.IsDir || member.Mode != packDirMode {
				t.Errorf("%q: dir=%v mode=%o, want a directory with mode %o", member.Name, member.IsDir, member.Mode, packDirMode)
			}
		}
	}
}

// TestPackDataNormalisesLineEndings is the `convert_win_2_linux(..., write=True)`
// pass every copied file goes through: a UTF-8 BOM is stripped and CRLF — and a
// lone CR — become LF.
func TestPackDataNormalisesLineEndings(t *testing.T) {
	lab := newLabOnDisk(t)
	writeLabFile(t, lab, "pc1/crlf.txt", "\xEF\xBB\xBFa\r\nb\rc\n")

	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}
	data, err := packData(machine)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}

	for _, member := range unpack(t, data) {
		if member.Name == "hostlab/pc1/crlf.txt" {
			if member.Content != "a\nb\nc\n" {
				t.Errorf("content = %q, want the BOM stripped and LF endings", member.Content)
			}
			return
		}
	}
	t.Fatal("the file was not packed")
}

// TestPackDataExcludesDSStore is `Walker(exclude=utils.EXCLUDED_FILES)`.
func TestPackDataExcludesDSStore(t *testing.T) {
	lab := newLabOnDisk(t)
	writeLabFile(t, lab, "pc1/.DS_Store", "junk")
	writeLabFile(t, lab, "pc1/real.txt", "kept")

	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}
	data, err := packData(machine)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}

	for _, member := range unpack(t, data) {
		if member.Name == "hostlab/pc1/.DS_Store" {
			t.Error(".DS_Store was packed")
		}
	}
}

// TestPackDataExclusionIsFilesOnly is pyfilesystem's split between `exclude`
// and `exclude_dirs`: Kathará passes only the first, which matches FILES, so a
// DIRECTORY named `.DS_Store` is walked into and everything under it is
// shipped. Oracle-verified against `copy_fs` with the same walker. Pruning the
// subtree would silently drop files the device declares.
func TestPackDataExclusionIsFilesOnly(t *testing.T) {
	lab := newLabOnDisk(t)
	writeLabFile(t, lab, "pc1/.DS_Store/inner.txt", "kept")
	writeLabFile(t, lab, "pc1/sub/.DS_Store", "junk")

	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}
	data, err := packData(machine)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}

	var kept, excludedDir bool
	for _, member := range unpack(t, data) {
		switch member.Name {
		case "hostlab/pc1/.DS_Store/inner.txt":
			kept = true
		case "hostlab/pc1/.DS_Store/":
			excludedDir = true
		case "hostlab/pc1/sub/.DS_Store":
			t.Error("an excluded FILE was packed")
		}
	}
	if !kept {
		t.Error("a file under a directory named .DS_Store was dropped; exclude matches files only")
	}
	if !excludedDir {
		t.Error("the .DS_Store directory member is missing; copy_fs creates it")
	}
}

// TestPackDataEmptinessIgnoresTheExclusion reproduces the asymmetry in
// `pack_data`: emptiness is decided by the `isempty` probe, which runs BEFORE
// and independently of the exclusion walker. A device folder holding only a
// `.DS_Store` is therefore NOT empty — an archive is produced, carrying the two
// directory members and no files.
func TestPackDataEmptinessIgnoresTheExclusion(t *testing.T) {
	lab := newLabOnDisk(t)
	writeLabFile(t, lab, "pc1/.DS_Store", "junk")

	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}
	data, err := packData(machine)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	if data == nil {
		t.Fatal("a folder holding only an excluded file was treated as empty")
	}

	members := unpack(t, data)
	for _, member := range members {
		if !member.IsDir {
			t.Errorf("unexpected file member %q", member.Name)
		}
	}
}

// TestPackDataIsDeterministic is what the fixed epoch buys: Python's archive
// carries `time.time()` in every header and a random temp-file name in the gzip
// header, so two of its runs already differ; the port's do not.
func TestPackDataIsDeterministic(t *testing.T) {
	lab := newLabOnDisk(t)
	writeLabFile(t, lab, "pc1/a.txt", "a")
	writeLabFile(t, lab, "pc1.startup", "ip a\n")

	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}

	first, err := packData(machine)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	second, err := packData(machine)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("two packs of the same device produced different bytes")
	}
}

// TestConvertWin2Linux covers the bytes-level `convert_win_2_linux` on its own,
// including the two ways a file is shipped VERBATIM: a binary extension (which
// is consulted before the content is looked at) and content that is not valid
// UTF-8, where Python's decode raises and its blanket except falls through to
// the raw read.
func TestConvertWin2Linux(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		want    string
	}{
		{"crlf becomes lf", "a.txt", "a\r\nb", "a\nb"},
		{"a lone cr becomes lf", "a.txt", "a\rb", "a\nb"},
		{"an existing lf is untouched", "a.txt", "a\nb", "a\nb"},
		{"the bom is stripped", "a.txt", "\xEF\xBB\xBFa", "a"},
		{"only one bom is stripped", "a.txt", "\xEF\xBB\xBF\xEF\xBB\xBFa", "\xEF\xBB\xBFa"},
		{"a binary extension is shipped verbatim", "a.png", "x\r\ny", "x\r\ny"},
		{"invalid utf-8 is shipped verbatim, bom included", "a.txt", "\xEF\xBB\xBF\xff\xfe\r\n", "\xEF\xBB\xBF\xff\xfe\r\n"},
		{"an empty file stays empty", "a.txt", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(convertWin2Linux(tt.file, []byte(tt.content))); got != tt.want {
				t.Errorf("convertWin2Linux(%q, %q) = %q, want %q", tt.file, tt.content, got, tt.want)
			}
		})
	}
}

// TestExtractTar is the `retrieve_files` half: the archive the daemon returns,
// written out under dst.
func TestExtractTar(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("header: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "dir/f.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5}); err != nil {
		t.Fatalf("header: %v", err)
	}
	if _, err := tw.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	dst := t.TempDir()
	if err := extractTar(&buf, dst); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "dir", "f.txt"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("extracted content = %q", got)
	}
}

// TestExtractTarRestoresModesAndTimes is what `extractall` does after it has
// written each member: chmod to the member's EXACT mode and utime to its mtime.
// Creating the file with the mode instead leaves it umask-masked — a 0o666
// member comes out 0o644 under the usual 022 — and never touching the mtime
// stamps every retrieved file with now.
//
// The directory pass runs LAST and in reverse name order, so a nested member
// cannot leave its parent at the safe 0o700 `makedir` created it with.
func TestExtractTarRestoresModesAndTimes(t *testing.T) {
	mtime := time.Unix(1_600_000_000, 0)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	members := []*tar.Header{
		{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o777, ModTime: mtime},
		{Name: "d/inner/", Typeflag: tar.TypeDir, Mode: 0o750, ModTime: mtime},
		{Name: "d/inner/wide.txt", Typeflag: tar.TypeReg, Mode: 0o666, ModTime: mtime, Size: 2},
		{Name: "d/exec.sh", Typeflag: tar.TypeReg, Mode: 0o755, ModTime: mtime, Size: 2},
	}
	for _, header := range members {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("header %q: %v", header.Name, err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte("hi")); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	dst := t.TempDir()
	if err := extractTar(&buf, dst); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	for _, tt := range []struct {
		path string
		mode os.FileMode
	}{
		{"d", 0o777},
		{filepath.Join("d", "inner"), 0o750},
		{filepath.Join("d", "inner", "wide.txt"), 0o666},
		{filepath.Join("d", "exec.sh"), 0o755},
	} {
		info, err := os.Lstat(filepath.Join(dst, tt.path))
		if err != nil {
			t.Fatalf("lstat %q: %v", tt.path, err)
		}
		if info.Mode().Perm() != tt.mode {
			t.Errorf("%s mode = %04o, want %04o", tt.path, info.Mode().Perm(), tt.mode)
		}
		if !info.ModTime().Equal(mtime) {
			t.Errorf("%s mtime = %v, want the member's %v", tt.path, info.ModTime(), mtime)
		}
	}
}

// TestLabFilesOrder pins the four fixed scenario files and their order
// (`model/Machine.py:407`).
func TestLabFilesOrder(t *testing.T) {
	want := []string{"pc1.startup", "pc1.shutdown", "shared.startup", "shared.shutdown"}
	if got := labFiles("pc1"); !reflect.DeepEqual(got, want) {
		t.Errorf("labFiles = %q, want %q", got, want)
	}
}
