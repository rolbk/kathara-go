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

var gzipMagic = []byte{0x1f, 0x8b}

// extractScenarioArchive reads a scenario archive from r and writes it under
// dir.
func extractScenarioArchive(r io.Reader, dir string) error {
	if r == nil {
		return kerrors.NewOS("Cannot read the network scenario archive from stdin.")
	}

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
			return kerrors.New(kerrors.ErrInvocation,
				"The network scenario archive contains an unsupported entry `"+header.Name+"`.")
		}
	}

	if !seen {
		return kerrors.NewOS("The network scenario archive is empty.")
	}
	return nil
}

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
