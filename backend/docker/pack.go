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
// (see startup.go), so the name must stay in sync with the startup script.
const hostlabDir = "hostlab"

// labFiles are the four scenario-level files `pack_data` copies in, in the
// order it lists them (`model/Machine.py:407`). `{name}` is the device's.
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
var packEpoch = time.Unix(0, 0)

// packData is `Machine.pack_data`: a gzipped tar of the device's directory
// under `hostlab/{name}/` plus the four scenario files under `hostlab/`, with
// every text file's BOM stripped and its line endings normalised.
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
