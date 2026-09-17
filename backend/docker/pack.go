// This file is `Machine.pack_data` (`model/Machine.py:381`) and the extraction
// half of `retrieve_files`.
//
// # Why it lives here
//
// PACKAGE_GRAPH.md §1.1 row 5 and §2.2 put `pack_data` in `model/pack.go`, and
// that file does not exist: the symbol is a tracked gap (PROPOSED-DIVERGENCES.md,
// "`model.PackData` is registered but not implemented"). The gap is real —
// `model` cannot write it without two widenings of `internal/util`, a
// bytes-level `convert_win_2_linux` and a `WriteTar` that emits directory
// members — and this stage may not edit either package. Its only 1.0 consumers
// are the two backends' `copy_files`, so the Docker one carries its own copy
// and PROPOSED-DIVERGENCES.md asks for the symbol to be scheduled or reassigned;
// when it lands, this file becomes a call.

package docker

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/vfs"
)

// hostlabDir is the archive's single top-level directory. The startup script
// reads `/hostlab/{machine}` and the four fixed files out of it
// (see startup.go), so the name is part of the container contract.
const hostlabDir = "hostlab"

// labFiles are the four scenario-level files `pack_data` copies in, in the
// order it lists them (`model/Machine.py:407`). `{name}` is the device's.
//
// They are looked up on the SCENARIO filesystem, not the device's, which is why
// two devices in one scenario each ship their own copy of `shared.startup`.
func labFiles(machineName string) []string {
	return []string{
		machineName + ".startup",
		machineName + ".shutdown",
		"shared.startup",
		"shared.shutdown",
	}
}

// Header defaults, from what `fs.tarfs.WriteTarFS` produces (oracle-probed on
// 3.8.3: files 0644, directories 0755, uid/gid 0, uname/gname "root").
const (
	packFileMode = 0o644
	packDirMode  = 0o755
)

// packEpoch is the mtime every member carries.
//
// Python's is `time.time()` — `WriteTarFS` stages the tree on a real
// filesystem and tars it on close, so each member gets the moment it was
// written, and two runs of the same command already differ. There is therefore
// no Python byte string to match (SYNTHESIS §1.4: "Archive bytes are
// nondeterministic (walk order, mtimes, gzip header) — goldens compare
// extracted content"), and a fixed epoch is chosen so that the port's own
// output is reproducible. It is the same choice `util.PackFilesForTar` makes
// for the same reason.
var packEpoch = time.Unix(0, 0)

// packData is `Machine.pack_data`: a gzipped tar of the device's directory
// under `hostlab/{name}/` plus the four scenario files under `hostlab/`, with
// every text file's BOM stripped and its line endings normalised.
//
// It answers nil — Python's None — when there is nothing to ship, and
// `DockerMachine.create` then skips the `put_archive` entirely.
//
// # What counts as "nothing"
//
// `is_empty` starts true and is cleared by either half. The device half clears
// it on `self.fs and not self.fs.isempty(”)`, which is tested BEFORE the
// exclusion walker runs — so a device directory holding only a `.DS_Store` is
// NOT empty, and the archive comes out carrying the two directory members and
// no files. That is reproduced: the emptiness question and the exclusion
// question are asked separately, as they are in Python.
//
// # Member order
//
// Python's is the OS's walk order over a staging directory, which OQ-15(c)
// flags as deliberately unspecified and which no ruling pins. The order here is
// declared instead: `hostlab/`, `hostlab/{name}/`, the device's files in sorted
// path order, then the four scenario files in their fixed order. Extraction is
// what the goldens compare, and no two members can collide — the device's live
// under a subdirectory of the scenario files' — so the order is unobservable
// past the untar.
//
// # Normalisation
//
// `convert_win_2_linux(..., write=True)` runs on every copied file: a file the
// binary sniff calls text AND that decodes as UTF-8 loses its BOM and gets LF
// line endings; everything else is shipped byte for byte. Python applies it to
// the staging COPY, so the user's own files are never rewritten — and neither
// are they here, since the normalisation happens on the bytes on their way into
// the archive.
func packData(machine *model.Machine) ([]byte, error) {
	isEmpty := true

	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)

	if err := writeDir(tw, hostlabDir); err != nil {
		return nil, err
	}

	machineDir := path.Join(hostlabDir, machine.Name)
	if err := writeDir(tw, machineDir); err != nil {
		return nil, err
	}

	if machine.FS != nil {
		empty, err := vfs.IsEmpty(machine.FS, "")
		if err != nil {
			return nil, err
		}
		if !empty {
			if err := writeDeviceFiles(tw, machine, machineDir); err != nil {
				return nil, err
			}
			isEmpty = false
		}
	}

	for _, name := range labFiles(machine.Name) {
		if !vfs.Exists(machine.Lab.FS, name) {
			continue
		}
		content, err := vfs.ReadFile(machine.Lab.FS, name)
		if err != nil {
			return nil, err
		}
		if err := writeFile(tw, path.Join(hostlabDir, name), convertWin2Linux(name, content)); err != nil {
			return nil, err
		}
		isEmpty = false
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if isEmpty {
		return nil, nil
	}

	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	// Python's gzip header carries `time.time()` and the random temp-file
	// basename; both are dropped here, as `util.PackFilesForTar` drops them.
	zw.Header = gzip.Header{OS: 255}
	if _, err := zw.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// writeDeviceFiles is the `copy_fs(self.fs, machine_tar_dir, walker=Walker(
// exclude=EXCLUDED_FILES))` half (`model/Machine.py:396-403`).
//
// `copy_fs` copies the directory structure as well as the files, so an empty
// subdirectory of the device folder becomes an empty directory in the archive
// and therefore in the container. The exclusion is by BASE NAME
// (`.DS_Store`), which is what pyfilesystem's `Walker(exclude=…)` matches on.
//
// `exclude` matches FILES ONLY — `exclude_dirs` is a separate parameter and
// Kathará does not pass it — so a DIRECTORY named `.DS_Store` is walked into
// and its contents are copied. Oracle-verified: `copy_fs` with
// `Walker(exclude=['.DS_Store'])` produces `/.DS_Store` and
// `/.DS_Store/inner.txt` while dropping `/sub/.DS_Store`. Pruning the subtree
// would silently lose files the device ships.
//
// The walk is [vfs.WalkFollow], not [vfs.Walk]: pyfilesystem types each entry
// with `os.DirEntry.is_dir()`, which FOLLOWS symlinks, so a device folder
// holding `linkdir -> real/` ships the target's contents at `linkdir/`. With
// the non-following walk the link landed on the file path instead and the
// whole deploy died on `path should be a file`, leaving the container
// Created.
func writeDeviceFiles(tw *tar.Writer, machine *model.Machine, machineDir string) error {
	excluded := util.ExcludedFiles()

	var dirs, files []string
	err := vfs.WalkFollow(machine.FS, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == "." || name == "" {
			return nil
		}
		if !entry.IsDir() && slices.Contains(excluded, path.Base(name)) {
			return nil
		}
		if entry.IsDir() {
			dirs = append(dirs, name)
		} else {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		return err
	}

	slices.Sort(dirs)
	slices.Sort(files)

	for _, name := range dirs {
		if err := writeDir(tw, path.Join(machineDir, name)); err != nil {
			return err
		}
	}
	for _, name := range files {
		content, err := vfs.ReadFile(machine.FS, name)
		if err != nil {
			return err
		}
		if err := writeFile(tw, path.Join(machineDir, name), convertWin2Linux(name, content)); err != nil {
			return err
		}
	}
	return nil
}

func writeDir(tw *tar.Writer, name string) error {
	return tw.WriteHeader(&tar.Header{
		Name:     name + "/",
		Typeflag: tar.TypeDir,
		Mode:     packDirMode,
		Uname:    "root",
		Gname:    "root",
		ModTime:  packEpoch,
		Format:   tar.FormatPAX, // tarfile.DEFAULT_FORMAT
	})
}

func writeFile(tw *tar.Writer, name string, content []byte) error {
	err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     int64(len(content)),
		Mode:     packFileMode,
		Uname:    "root",
		Gname:    "root",
		ModTime:  packEpoch,
		Format:   tar.FormatPAX,
	})
	if err != nil {
		return err
	}
	_, err = tw.Write(content)
	return err
}

// utf8BOM is what the `utf-8-sig` codec strips: exactly one, and only at the
// front.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// convertWin2Linux is `utils.convert_win_2_linux(path, write=True)` over bytes
// rather than over a host path.
//
// `internal/util` exposes the same transformation only over an `os` path
// ([util.ConvertWin2Linux]), because Python's `WriteTarFS` stages every file on
// a real filesystem before archiving it and the mutating variant therefore
// always had a path to open. A `vfs.FS` device folder can be in memory, so the
// bytes form is needed and this stage cannot add it to `internal/util`;
// PROPOSED-DIVERGENCES.md asks for the widening.
//
// The three steps are that function's, in its order:
//
//  1. the binaryornot sniff — the EXTENSION first (so an empty `x.bin` is
//     binary and an empty `x.txt` is text), then the first 512 bytes;
//  2. strip one leading UTF-8 BOM;
//  3. if what remains is valid UTF-8, normalise CRLF and lone CR to LF;
//     otherwise ship the ORIGINAL bytes, BOM included, because Python's
//     decode raised and its blanket `except` fell through to the raw read.
func convertWin2Linux(name string, content []byte) []byte {
	if util.HasBinaryExtension(name) {
		return content
	}
	head := content
	if len(head) > 512 {
		head = head[:512]
	}
	if util.IsBinaryString(head) {
		return content
	}

	body := bytes.TrimPrefix(content, utf8BOM)
	if !utf8.Valid(body) {
		return content
	}
	return normalizeNewlines(body)
}

// normalizeNewlines is Python's universal-newline translation: CRLF and a lone
// CR both become LF, an existing LF is untouched.
func normalizeNewlines(b []byte) []byte {
	if bytes.IndexByte(b, '\r') < 0 {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '\r' {
			out = append(out, '\n')
			if i+1 < len(b) && b[i+1] == '\n' {
				i++
			}
			continue
		}
		out = append(out, b[i])
	}
	return out
}

// ---------------------------------------------------------------------------
// Extraction
// ---------------------------------------------------------------------------

// extractTar is `tarfile.extractall(path=dst)` with no `filter=`, i.e. the
// fully-trusted extraction `retrieve_files` performs.
//
// # What is reproduced
//
//   - `../` members write outside dst: `filepath.Join` cleans but does not
//     clamp, so `Join(dst, "../x")` lands beside dst exactly as
//     `os.path.join` does. That is deliberate (see
//     [machineService.retrieveFiles]) and PROPOSED-DIVERGENCES.md carries the
//     hardening request.
//   - Member ATTRIBUTES: `extractall` restores the exact mode, the mtime, and —
//     when running as root, which `retrieve_files` routinely is — the ownership.
//     A file is created with the umask-masked default and then `chmod`ed to the
//     member's mode, which is why the mode is not passed to the open.
//   - Directory attributes are applied in a SECOND pass, reverse-sorted by
//     name, because a later member can overwrite a parent's mode; and their
//     order there is chown → utime → chmod, not the files' chown → chmod →
//     utime.
//   - An attribute failure is an `ExtractError`, and `errorlevel` defaults to 1,
//     so it is logged and SWALLOWED — while it does skip the rest of that
//     member's attributes. A failure to create the member itself is an OSError
//     and propagates.
//
// # What is not (DIVERGENCES.md 69)
//
// An ABSOLUTE member name: `os.path.join(dst, "/abs/x")` discards dst and
// writes to `/abs/x`, while `filepath.Join` roots it under dst. Docker's
// `get_archive` only ever emits relative names, so the case is unreachable from
// `retrieve_files`, and reproducing it would widen an already-flagged hole.
//
// Only the three member types Docker's `get_archive` emits are handled —
// regular files, directories and symlinks. Python's `extractall` also creates
// devices and fifos; Docker never puts one in the archive it builds from a
// container path, and creating them would need privileges the CLI has dropped.
func extractTar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)

	// `directories`: the dir members, whose attributes wait for the second
	// pass (`_extract_one(..., set_attrs=not tarinfo.isdir())`).
	var directories []*tar.Header

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dst, filepath.FromSlash(header.Name))

		switch header.Typeflag {
		case tar.TypeDir:
			// `makedir` creates ONE level with a safe 0o700 and tolerates an
			// existing directory; the upper levels are `os.makedirs` with the
			// default permissions.
			if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
				return err
			}
			if err := os.Mkdir(target, 0o700); err != nil {
				info, statErr := os.Stat(target)
				if !os.IsExist(err) || statErr != nil || !info.IsDir() {
					return err
				}
			}
			directories = append(directories, header)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
				return err
			}
			if err := writeExtracted(target, tr); err != nil {
				return err
			}
			setMemberAttrs(target, header, false)
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
				return err
			}
			// `tarfile` removes an existing name before linking; without the
			// removal a re-extraction fails with EEXIST.
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
			setMemberAttrs(target, header, true)
		}
	}

	// `directories.sort(key=lambda a: a.name, reverse=True)`, then chown,
	// utime, chmod — that order, and each step skipped once one has failed.
	slices.SortStableFunc(directories, func(a, b *tar.Header) int {
		return strings.Compare(b.Name, a.Name)
	})
	for _, header := range directories {
		target := filepath.Join(dst, filepath.FromSlash(header.Name))
		if !chownMember(target, header, false) {
			continue
		}
		if err := os.Chtimes(target, header.ModTime, header.ModTime); err != nil {
			continue
		}
		_ = os.Chmod(target, header.FileInfo().Mode())
	}
	return nil
}

// setMemberAttrs is the `if set_attrs:` tail of `_extract_member`: chown
// always, then chmod and utime for everything that is not a symlink (there is
// no portable `lchmod`, and Python does not try).
//
// Every failure is an `ExtractError` the default `errorlevel` swallows, so
// nothing is returned; the raise does abort the remaining steps for that
// member, which is what the early returns are.
func setMemberAttrs(target string, header *tar.Header, symlink bool) {
	if !chownMember(target, header, symlink) || symlink {
		return
	}
	if err := os.Chmod(target, header.FileInfo().Mode()); err != nil {
		return
	}
	_ = os.Chtimes(target, header.ModTime, header.ModTime)
}

// chownMember is `TarFile.chown`: a no-op unless the euid is 0, and then the
// member's `gname`/`uname` resolved against the host's group and passwd
// databases, falling back to the numeric ids the archive carries.
//
// The result reports whether the caller may continue — false is Python's
// `raise ExtractError("could not change owner")`, which skips the chmod and
// utime that would have followed.
func chownMember(target string, header *tar.Header, symlink bool) bool {
	if os.Geteuid() != 0 {
		return true
	}

	uid, gid := header.Uid, header.Gid
	if header.Gname != "" {
		if group, err := user.LookupGroup(header.Gname); err == nil {
			if id, err := strconv.Atoi(group.Gid); err == nil {
				gid = id
			}
		}
	}
	if header.Uname != "" {
		if owner, err := user.Lookup(header.Uname); err == nil {
			if id, err := strconv.Atoi(owner.Uid); err == nil {
				uid = id
			}
		}
	}

	if symlink {
		return os.Lchown(target, uid, gid) == nil
	}
	return os.Chown(target, uid, gid) == nil
}

// writeExtracted is `TarFile.makefile`: a plain `open(targetpath, "wb")`, so
// the creation mode is the umask default and the member's real mode arrives
// with the chmod in [setMemberAttrs].
func writeExtracted(target string, r io.Reader) (err error) {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(f, r)
	return err
}
