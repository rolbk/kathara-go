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
// one regular-file member per entry, in the order the caller passed them.
//
// Header fields come straight from tarfile.TarInfo's defaults, which
// pack_file_for_tar never overrides (verified: mode 0644, uid 0, gid 0,
// mtime 0, typeflag '0', empty uname/gname). pack_files_for_tar emits regular
// files only — it never adds a directory member, so there is no separate
// directory mode to reproduce.
//
// The entry slice is the whole reason the signature is not a map: Python
// iterates `guest_to_host.items()` in dict insertion order (utils.py:452), and
// ORDERING.tsv row for utils.py:452 binds this port to "API takes ordered
// pairs; Python client must preserve caller order". Member order is observable
// — extraction is last-wins for two entries whose arcnames collide, which two
// keys differing only in separator style do.
//
// The compressed wrapper is the one thing that is *not* byte-reproducible.
// CPython builds it as `gzip.GzipFile(fileobj=NamedTemporaryFile(...))`, so
// its header carries `time.time()` in MTIME and the random temp-file basename
// in FNAME (measured: `tmpsuqwxwrx.tar`, different on every call). Two Python
// calls on identical input already differ, so there is no Python byte string to
// match; the Go writer emits neither field. Recorded in
// PROPOSED-DIVERGENCES.md. The tar payload underneath — the layer both Docker
// and Kubernetes decompress before extracting — is byte-identical.
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

// WriteTar emits the uncompressed archive, one member per entry in slice
// order. It is exported because the uncompressed stream is the layer that is
// byte-identical to Python's, and is therefore what golden vectors compare.
//
// There is deliberately no map-shaped entry point. Python's parameter is a
// dict and its iteration order is the caller's insertion order; a Go map has
// no order to preserve, and ranging over one here would put an unordered
// iteration on a path that reaches a container (PORT_SPEC §10, "Reject any
// `for ... range` over a map whose iteration order can reach a container").
func WriteTar(w io.Writer, entries []TarEntry) error {
	counting := &countingWriter{w: w}
	tw := tar.NewWriter(counting)
	for _, e := range entries {
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
