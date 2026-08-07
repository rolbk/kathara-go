// This file is JSON_CLI_CONTRACT.md §7.3-§7.4: the tar-on-stdin intake of
// `lstart --from-archive -`.
//
// PORT_SPEC §5.4 asks for it so that the Python client package can deploy a
// scenario it built in memory, with no directory anywhere. The archive is
// materialised to a private temporary directory and `lstart` then proceeds
// exactly as if `-d <tmpdir>` had been given — which is why this file's whole
// job is to produce a directory and refuse everything that could escape it.

package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// gzipMagic is the two-byte prefix §7.4 says the format is detected by.
var gzipMagic = []byte{0x1f, 0x8b}

// extractScenarioArchive reads a scenario archive from r and writes it under
// dir.
//
// The constraints are §7.4's, and they are refusals rather than sanitisations:
// an entry that could write outside dir fails the whole deploy with code
// `Invocation` and nothing is deployed, instead of being silently renamed. The
// same reasoning as PROPOSED-DIVERGENCES.md's note on `retrieve_files`, applied
// to a path that takes its input from an untrusted stdin by design.
func extractScenarioArchive(r io.Reader, dir string) error {
	if r == nil {
		return kerrors.NewOS("Cannot read the network scenario archive from stdin.")
	}

	// stdin is consumed to EOF before deployment begins (§7.4), so the reader
	// is buffered once and the magic bytes are peeked rather than re-read.
	buffered := bufio.NewReader(r)
	magic, err := buffered.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return kerrors.NewOS("Cannot read the network scenario archive from stdin.")
	}

	var source io.Reader = buffered
	if len(magic) == 2 && magic[0] == gzipMagic[0] && magic[1] == gzipMagic[1] {
		zr, err := gzip.NewReader(buffered)
		if err != nil {
			return kerrors.NewOS("The network scenario archive is not a valid gzip stream.")
		}
		defer func() { _ = zr.Close() }()
		source = zr
	}

	reader := tar.NewReader(source)
	seen := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return kerrors.NewOS("The network scenario archive is not a valid tar stream.")
		}
		seen = true

		name, err := archiveEntryPath(header.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(name))

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeArchiveFile(target, reader, os.FileMode(header.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			// PAX metadata records, not scenario content.
			continue
		default:
			// Symlinks, hardlinks, devices and FIFOs are refused outright
			// (§7.4): a scenario is regular files and directories.
			return kerrors.New(kerrors.ErrInvocation,
				"The network scenario archive contains an unsupported entry `"+header.Name+"`.")
		}
	}

	if !seen {
		return kerrors.NewOS("The network scenario archive is empty.")
	}
	return nil
}

// archiveEntryPath normalises one member name and refuses the two shapes §7.4
// rejects: an absolute path and a `..` traversal.
//
// A leading `./` is normalised away; a single leading directory component is
// NOT stripped, because §7.3 pins the layout as "a normal Kathará scenario
// directory rooted at the archive root".
func archiveEntryPath(name string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(name, `\`, "/"))
	if clean == "." || clean == "" {
		return "", nil
	}
	if path.IsAbs(clean) || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", kerrors.New(kerrors.ErrInvocation,
			"The network scenario archive contains an unsafe path `"+name+"`.")
	}
	return clean, nil
}

// writeArchiveFile writes one member, preserving its mode (§7.4: "File modes
// are preserved; owners are ignored").
func writeArchiveFile(target string, r io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, r); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
