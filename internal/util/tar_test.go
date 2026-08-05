package util

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"
)

// Header field offsets inside a 512-byte ustar block, used by the
// Python-parity test to carve out the two documented divergences.
const (
	offChksum   = 148
	lenChksum   = 8
	offDevMajor = 329
	lenDevBoth  = 16 // devmajor + devminor
)

func TestTarName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"posix path untouched", "/etc/frr/frr.conf", "/etc/frr/frr.conf"},
		{"windows separators translated", `sub\win\bom.txt`, "sub/win/bom.txt"},
		{"mixed separators", `a\b/c\d`, "a/b/c/d"},
		{"leading slash preserved", "/a.txt", "/a.txt"},
		{"relative preserved", "a.txt", "a.txt"},
		{"no other normalisation", "./a//b/../c", "./a//b/../c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TarName(tt.in); got != tt.want {
				t.Errorf("TarName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWriteTarHeaderFields(t *testing.T) {
	// tarfile.TarInfo defaults, verified against the oracle:
	// mode 0644, uid 0, gid 0, mtime 0, typeflag '0', empty uname/gname.
	// pack_files_for_tar emits regular files only — no directory members —
	// so there is no separate directory mode to reproduce.
	var buf bytes.Buffer
	entries := []TarEntry{
		{Path: `sub\win\b.txt`, Content: []byte("b")},
		{Path: "/a.txt", Content: []byte("aa")},
	}
	if err := WriteTar(&buf, entries); err != nil {
		t.Fatal(err)
	}

	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	var got []*tar.Header
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, h)
	}

	if len(got) != 2 {
		t.Fatalf("member count = %d, want 2", len(got))
	}
	// Sorted by canonical name: "/a.txt" < "sub/win/b.txt".
	wantNames := []string{"/a.txt", "sub/win/b.txt"}
	for i, h := range got {
		if h.Name != wantNames[i] {
			t.Errorf("member %d name = %q, want %q", i, h.Name, wantNames[i])
		}
		if h.Mode != 0o644 {
			t.Errorf("%s: mode = %#o, want 0644", h.Name, h.Mode)
		}
		if h.Uid != 0 || h.Gid != 0 {
			t.Errorf("%s: uid/gid = %d/%d, want 0/0", h.Name, h.Uid, h.Gid)
		}
		if h.Uname != "" || h.Gname != "" {
			t.Errorf("%s: uname/gname = %q/%q, want empty", h.Name, h.Uname, h.Gname)
		}
		if h.ModTime.Unix() != 0 {
			t.Errorf("%s: mtime = %d, want 0", h.Name, h.ModTime.Unix())
		}
		if h.Typeflag != tar.TypeReg {
			t.Errorf("%s: typeflag = %q, want regular", h.Name, h.Typeflag)
		}
	}
}

func TestWriteTarOrdering(t *testing.T) {
	// Ordering ruling: members come out in sorted CANONICAL-name order, not
	// in caller order. Python iterates dict insertion order, so its output is
	// call-site dependent; tar assigns no meaning to member order, so sorting
	// stays inside the observable envelope and makes archives comparable.
	tests := []struct {
		name    string
		entries []TarEntry
		want    []string
	}{
		{
			name: "reverse input sorts",
			entries: []TarEntry{
				{Path: "/z", Content: []byte("z")},
				{Path: "/m", Content: []byte("m")},
				{Path: "/a", Content: []byte("a")},
			},
			want: []string{"/a", "/m", "/z"},
		},
		{
			name: "sorted on the canonical name, not the raw one",
			entries: []TarEntry{
				{Path: `sub\b`, Content: []byte("b")},
				{Path: "sub/a", Content: []byte("a")},
			},
			want: []string{"sub/a", "sub/b"},
		},
		{
			name: "byte order, so uppercase precedes lowercase",
			entries: []TarEntry{
				{Path: "/b", Content: []byte("b")},
				{Path: "/B", Content: []byte("B")},
			},
			want: []string{"/B", "/b"},
		},
		{
			name: "colliding canonical names both survive",
			entries: []TarEntry{
				{Path: `a\b`, Content: []byte("backslash")},
				{Path: "a/b", Content: []byte("slash")},
			},
			want: []string{"a/b", "a/b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteTar(&buf, tt.entries); err != nil {
				t.Fatal(err)
			}
			got := memberNames(t, buf.Bytes())
			if len(got) != len(tt.want) {
				t.Fatalf("names = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("names = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestWriteTarDeterministic(t *testing.T) {
	// The point of the whole exercise: identical inputs, byte-identical
	// archives, including the gzip wrapper (Python's stamps time.time() in the
	// gzip MTIME field, so two Python calls seconds apart differ).
	entries := []TarEntry{
		{Path: "/etc/frr/frr.conf", Content: []byte("hostname r1\n")},
		{Path: "/hosthome/x", Content: bytes.Repeat([]byte("x"), 5000)},
	}
	shuffled := []TarEntry{entries[1], entries[0]}

	a, err := PackFilesForTar(entries)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PackFilesForTar(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("gzip output differs between two orderings of the same input")
	}

	c, err := PackFilesForTarMap(map[string][]byte{
		"/etc/frr/frr.conf": []byte("hostname r1\n"),
		"/hosthome/x":       bytes.Repeat([]byte("x"), 5000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, c) {
		t.Error("map entry point differs from slice entry point")
	}

	// gzip MTIME lives at bytes 4..7 of the header and must be zero.
	if !bytes.Equal(a[4:8], []byte{0, 0, 0, 0}) {
		t.Errorf("gzip MTIME = % x, want zeroed", a[4:8])
	}

	zr, err := gzip.NewReader(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := WriteTar(&want, entries); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want.Bytes()) {
		t.Error("gunzip(PackFilesForTar) != WriteTar")
	}
}

func TestWriteTarRecordPadding(t *testing.T) {
	// CPython tarfile pads the archive to a multiple of RECORDSIZE (10240);
	// Go's archive/tar stops after the two zero blocks. The padding is
	// reproduced, which is why the empty archive is 10240 bytes and not 1024.
	tests := []struct {
		name    string
		entries []TarEntry
		want    int
	}{
		{"empty input", nil, 10240},
		{"one small file", []TarEntry{{Path: "/a", Content: []byte("a")}}, 10240},
		{"content crossing the record boundary",
			[]TarEntry{{Path: "/big", Content: bytes.Repeat([]byte("A"), 9000)}}, 20480},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteTar(&buf, tt.entries); err != nil {
				t.Fatal(err)
			}
			if buf.Len() != tt.want {
				t.Errorf("archive length = %d, want %d", buf.Len(), tt.want)
			}
			if buf.Len()%tarRecordSize != 0 {
				t.Errorf("archive length %d is not a multiple of %d", buf.Len(), tarRecordSize)
			}
		})
	}
}

func TestWriteTarRoundTrip(t *testing.T) {
	// Every member must read back with the exact path and bytes, including
	// the cases where Go and CPython pick different PAX encodings.
	tests := []struct {
		name    string
		entries []TarEntry
	}{
		{"ascii paths", []TarEntry{
			{Path: "/aa/first.bin", Content: []byte{0, 1, 2, 0xff}},
			{Path: "/zz/last.txt", Content: []byte("hello\nworld\n")},
		}},
		{"empty content", []TarEntry{{Path: "/empty", Content: []byte{}}}},
		{"nil content", []TarEntry{{Path: "/nil", Content: nil}}},
		{"name longer than the 100-byte ustar field", []TarEntry{
			{Path: "/" + strings.Repeat("d", 60) + "/" + strings.Repeat("f", 60) + ".txt",
				Content: []byte("L")},
		}},
		{"non-ascii name", []TarEntry{{Path: "/tmp/caffè.txt", Content: []byte("N")}}},
		{"windows separators", []TarEntry{{Path: `dir\sub\f.txt`, Content: []byte("W")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteTar(&buf, tt.entries); err != nil {
				t.Fatal(err)
			}
			tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
			for _, want := range tt.entries {
				h, err := tr.Next()
				if err != nil {
					t.Fatalf("%s: %v", want.Path, err)
				}
				if h.Name != TarName(want.Path) {
					t.Errorf("name = %q, want %q", h.Name, TarName(want.Path))
				}
				body, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(body, want.Content) {
					t.Errorf("%s: content = %q, want %q", h.Name, body, want.Content)
				}
			}
			if _, err := tr.Next(); err != io.EOF {
				t.Errorf("expected EOF after the last member, got %v", err)
			}
		})
	}
}

// TestPythonParity compares against archives produced by the real
// utils.pack_files_for_tar (Kathara 3.8.3 on CPython 3.13.5), captured in
// testdata/. Two field windows per 512-byte block are excluded and asserted
// separately:
//
//   - devmajor/devminor (offset 329, 16 bytes). CPython 3.13 writes NULs there
//     for non-device members (tarfile._create_header's has_device_fields
//     branch, new in 3.13); 3.9-3.12 and Go's archive/tar write octal zeros.
//     POSIX leaves the field unused for regular files, and the golden would be
//     oracle-version dependent either way.
//   - the header checksum (offset 148, 8 bytes), which covers those 16 bytes
//     and therefore differs by exactly 14 * 0x30 = 672.
//
// Everything else — name, mode, uid, gid, size, mtime, typeflag, magic,
// version, uname, gname, prefix, the content blocks and the record padding —
// is byte-identical.
func TestPythonParity(t *testing.T) {
	tests := []struct {
		name    string
		golden  string
		entries []TarEntry
	}{
		{
			name:   "three ascii members",
			golden: "pack_basic.tar",
			entries: []TarEntry{
				{Path: "/aa/first.bin", Content: []byte{0, 1, 2, 0xff}},
				{Path: "/zz/last.txt", Content: []byte("hello\nworld\n")},
				{Path: `sub\win\bom.txt`, Content: []byte("BOM here\n")},
			},
		},
		{
			name:    "empty archive",
			golden:  "pack_empty.tar",
			entries: nil,
		},
		{
			name:   "sizes spanning a record boundary",
			golden: "pack_sizes.tar",
			entries: []TarEntry{
				{Path: "/big.txt", Content: bytes.Repeat([]byte("A"), 9000)},
				{Path: "/empty", Content: []byte{}},
				{Path: "/z.txt", Content: []byte("z")},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := os.ReadFile("testdata/" + tt.golden)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := WriteTar(&buf, tt.entries); err != nil {
				t.Fatal(err)
			}
			got := buf.Bytes()
			if len(got) != len(want) {
				t.Fatalf("length = %d, want %d", len(got), len(want))
			}

			gotMasked, wantMasked := maskHeaderDivergences(got), maskHeaderDivergences(want)
			if !bytes.Equal(gotMasked, wantMasked) {
				for i := range gotMasked {
					if gotMasked[i] != wantMasked[i] {
						t.Fatalf("first divergence at byte %d (block %d, offset %d): go=%q py=%q",
							i, i/512, i%512, got[i], want[i])
					}
				}
			}
		})
	}
}

// TestPythonParityDivergentFields pins the two excluded windows so the
// divergence stays a known, asserted fact rather than a silent mask.
func TestPythonParityDivergentFields(t *testing.T) {
	want, err := os.ReadFile("testdata/pack_basic.tar")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err = WriteTar(&buf, []TarEntry{
		{Path: "/aa/first.bin", Content: []byte{0, 1, 2, 0xff}},
		{Path: "/zz/last.txt", Content: []byte("hello\nworld\n")},
		{Path: `sub\win\bom.txt`, Content: []byte("BOM here\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.Bytes()

	// Header blocks of the three members are at 0, 1024 (1 content block),
	// and 2048.
	for _, off := range []int{0, 1024, 2048} {
		gd := got[off+offDevMajor : off+offDevMajor+lenDevBoth]
		wd := want[off+offDevMajor : off+offDevMajor+lenDevBoth]
		if string(gd) != "0000000\x000000000\x00" {
			t.Errorf("block at %d: Go devmajor/devminor = %q, want octal zeros", off, gd)
		}
		if !bytes.Equal(wd, make([]byte, lenDevBoth)) {
			t.Errorf("block at %d: CPython 3.13 devmajor/devminor = %q, want NULs", off, wd)
		}
		gc := parseOctalField(got[off+offChksum : off+offChksum+lenChksum])
		wc := parseOctalField(want[off+offChksum : off+offChksum+lenChksum])
		if gc-wc != 14*0x30 {
			t.Errorf("block at %d: checksum delta = %d, want %d (14 ASCII zeros)",
				off, gc-wc, 14*0x30)
		}
	}
}

// TestPythonPaxReadable covers the two cases where Go and CPython pick
// different PAX encodings for the same path: CPython emits a `././@PaxHeader`
// extended block, Go splits into the ustar prefix field (long name) or emits
// its own `PaxHeaders.0/` block (non-ASCII). The bytes differ; the archives
// must still denote the same member.
func TestPythonPaxReadable(t *testing.T) {
	tests := []struct {
		golden string
		want   string
	}{
		{"pack_longname.tar", "/" + strings.Repeat("d", 60) + "/" + strings.Repeat("f", 60) + ".txt"},
		{"pack_nonascii.tar", "/tmp/caffè.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			py, err := os.ReadFile("testdata/" + tt.golden)
			if err != nil {
				t.Fatal(err)
			}
			pyNames := memberNames(t, py)
			if len(pyNames) != 1 || pyNames[0] != tt.want {
				t.Fatalf("CPython archive members = %v, want [%q]", pyNames, tt.want)
			}

			var buf bytes.Buffer
			if err := WriteTar(&buf, []TarEntry{{Path: tt.want, Content: []byte("x")}}); err != nil {
				t.Fatal(err)
			}
			goNames := memberNames(t, buf.Bytes())
			if len(goNames) != 1 || goNames[0] != tt.want {
				t.Fatalf("Go archive members = %v, want [%q]", goNames, tt.want)
			}
		})
	}
}

func maskHeaderDivergences(b []byte) []byte {
	out := bytes.Clone(b)
	for off := 0; off+512 <= len(out); off += 512 {
		if !isHeaderBlock(out[off : off+512]) {
			continue
		}
		clear(out[off+offChksum : off+offChksum+lenChksum])
		clear(out[off+offDevMajor : off+offDevMajor+lenDevBoth])
	}
	return out
}

// isHeaderBlock recognises a ustar header by its magic, so content blocks that
// happen to sit at a 512-byte boundary are never masked.
func isHeaderBlock(blk []byte) bool {
	return bytes.Equal(blk[257:263], []byte("ustar\x00"))
}

func parseOctalField(f []byte) int {
	n := 0
	for _, c := range f {
		if c < '0' || c > '7' {
			break
		}
		n = n*8 + int(c-'0')
	}
	return n
}

func memberNames(t *testing.T, archive []byte) []string {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(archive))
	var out []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h.Name)
	}
}
