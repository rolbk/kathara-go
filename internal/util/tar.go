// Package util is the port of Kathara's utils.py (PACKAGE_GRAPH.md §2 row 2).
//
// This file covers the tar-packing half: utils.pack_file_for_tar and
// utils.pack_files_for_tar, the payload format that DockerManager.copy_files
// and KubernetesManager.copy_files push into a running device.
package util

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"sort"
	"strings"
	"time"
)

// tarZeroTime is tarfile.TarInfo's default mtime of 0. It must be spelled as
// an explicit epoch: time.Time's zero value is year 1, whose Unix() is
// negative and would push archive/tar off the USTAR header Python emits.
var tarZeroTime = time.Unix(0, 0)

// tarRecordSize is Python tarfile's RECORDSIZE (20 * BLOCKSIZE). CPython pads
// the archive out to a multiple of it on close; Go's archive/tar stops after
// the two zero blocks. The padding is reproduced so a Go archive is byte-equal
// to the Python one, including the 10240-byte archive an empty input produces.
const tarRecordSize = 20 * 512

// TarEntry is one member of the archive: a guest path and its content.
type TarEntry struct {
	// Path is the destination path inside the device. Backslashes are
	// rewritten to forward slashes when the header is emitted
	// ("Tar files must have Linux-style paths", utils.py:443); a leading "/"
	// is preserved, exactly as Python preserves it.
	Path string

	// Content is the file body, already normalised. Callers that start from a
	// host path are responsible for the convert_win_2_linux pass (binary
	// sniff, UTF-8 BOM strip, CRLF/CR collapse) before getting here; that
	// lives in the binaryornot port, not in the packer.
	Content []byte
}

// TarName is the arcname transformation from pack_file_for_tar:
// backslash to forward slash, nothing else.
func TarName(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// PackFilesForTar is utils.pack_files_for_tar: a gzip-compressed tar carrying
// one regular-file member per entry.
//
// Header fields come straight from tarfile.TarInfo's defaults, which
// pack_file_for_tar never overrides (verified: mode 0644, uid 0, gid 0,
// mtime 0, typeflag '0', empty uname/gname). pack_files_for_tar emits regular
// files only — it never adds a directory member, so there is no separate
// directory mode to reproduce.
//
// Two deliberate divergences from Python, both for determinism:
//
//  1. Member order is the sorted arcname, not the caller's map iteration
//     order. Python iterates dict insertion order, so its output depends on
//     call-site ordering; tar has no ordering semantics, so sorting is inside
//     the observable envelope and makes archives comparable.
//  2. The gzip header's MTIME field is zeroed. Python's `w:gz` stamps
//     time.time() there, which makes two identical inputs produce different
//     bytes seconds apart (verified: byte-identical across two calls == False).
//     The tar payload underneath is already deterministic in Python; this
//     extends that to the compressed wrapper.
func PackFilesForTar(entries []TarEntry) ([]byte, error) {
	var raw bytes.Buffer
	if err := WriteTar(&raw, entries); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	// Zero every header field gzip.Writer would otherwise fill from ambient
	// state. ModTime's zero value is what suppresses the timestamp.
	zw.Header = gzip.Header{OS: 255} // 255 = unknown, what Python's gzip writes
	if _, err := zw.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// PackFilesForTarMap is the map-shaped entry point matching the Python
// signature pack_files_for_tar(guest_to_host: Dict[str, ...]).
func PackFilesForTarMap(files map[string][]byte) ([]byte, error) {
	return PackFilesForTar(entriesFromMap(files))
}

// WriteTar emits the uncompressed archive. It is exported because the
// uncompressed stream is the layer that is byte-identical to Python's, and is
// therefore what golden vectors compare.
func WriteTar(w io.Writer, entries []TarEntry) error {
	sorted := canonicalize(entries)

	counting := &countingWriter{w: w}
	tw := tar.NewWriter(counting)
	for _, e := range sorted {
		hdr := &tar.Header{
			Name:     TarName(e.Path),
			Size:     int64(len(e.Content)),
			Mode:     0o644, // tarfile.TarInfo default
			Uid:      0,
			Gid:      0,
			Uname:    "",
			Gname:    "",
			ModTime:  tarZeroTime, // mtime 0
			Typeflag: tar.TypeReg,
			Format:   tar.FormatPAX, // tarfile.DEFAULT_FORMAT == PAX_FORMAT
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write(e.Content); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return padToRecord(counting)
}

// canonicalize applies the arcname transformation and sorts.
//
// Ordering ruling: sort by the CANONICAL (backslash-translated) name, because
// that is the string that ends up in the header and therefore the only one a
// consumer can observe. Two inputs that differ only in separator style then
// produce the same order. The original path breaks ties so the result stays
// total even when two distinct keys canonicalise to the same member name
// (Python would emit both members too, last-one-wins on extraction).
func canonicalize(entries []TarEntry) []TarEntry {
	out := make([]TarEntry, len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool {
		ni, nj := TarName(out[i].Path), TarName(out[j].Path)
		if ni != nj {
			return ni < nj
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func entriesFromMap(files map[string][]byte) []TarEntry {
	out := make([]TarEntry, 0, len(files))
	for p, c := range files {
		out = append(out, TarEntry{Path: p, Content: c})
	}
	return out
}

// padToRecord writes zero bytes until the stream length is a multiple of
// tarRecordSize, reproducing CPython tarfile's close().
func padToRecord(c *countingWriter) error {
	rem := c.n % tarRecordSize
	if rem == 0 {
		return nil
	}
	_, err := c.Write(make([]byte, tarRecordSize-rem))
	return err
}

type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}
