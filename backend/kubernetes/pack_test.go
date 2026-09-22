package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"testing"

	"github.com/KatharaFramework/kathara-go/model"
)

// TestPackDataEmptyDevice is `pack_data`'s None branch: a device with no
// directory and no scenario files ships nothing, which is what makes
// `KubernetesConfigMap.deploy_for_machine` answer None.
func TestPackDataEmptyDevice(t *testing.T) {
	s := testSettings()
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab)

	data, err := packData(device)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	if data != nil {
		t.Errorf("archive = %d bytes, want nil", len(data))
	}
}

// TestPackDataRoundTrip is the archive's content: the device's own files under
// `hostlab/<name>/` and the four scenario files under `hostlab/`.
func TestPackDataRoundTrip(t *testing.T) {
	s := testSettings()
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab)

	if err := device.CreateFileFromString("hello\n", "/etc/motd"); err != nil {
		t.Fatalf("CreateFileFromString: %v", err)
	}
	if err := lab.CreateFileFromString("echo shared\n", "shared.startup"); err != nil {
		t.Fatalf("CreateFileFromString: %v", err)
	}
	if err := lab.CreateFileFromString("echo device\n", "test_device.startup"); err != nil {
		t.Fatalf("CreateFileFromString: %v", err)
	}

	data, err := packData(device)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	if data == nil {
		t.Fatal("archive = nil, want content")
	}

	members := untarNames(t, data)
	for _, want := range []string{
		"hostlab/",
		"hostlab/test_device/",
		"hostlab/test_device/etc/motd",
		"hostlab/shared.startup",
		"hostlab/test_device.startup",
	} {
		if !slices.Contains(members, want) {
			t.Errorf("member %q missing from %v", want, members)
		}
	}
	// `<name>.shutdown` and `shared.shutdown` were not created, so they are not
	// in the archive: `pack_data` copies only the files that exist.
	if slices.Contains(members, "hostlab/test_device.shutdown") {
		t.Error("a scenario file that does not exist was archived")
	}
}

// TestPackDataNormalisesText is `convert_win_2_linux`: a text file loses its
// BOM and its CRLFs, and a binary one is shipped byte for byte.
func TestPackDataNormalisesText(t *testing.T) {
	s := testSettings()
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab)

	if err := device.CreateFileFromString("\ufeffline1\r\nline2\r\n", "/text.txt"); err != nil {
		t.Fatalf("CreateFileFromString: %v", err)
	}
	binary := string([]byte{0x00, 0x0d, 0x0a, 0xff})
	if err := device.CreateFileFromString(binary, "/blob.bin"); err != nil {
		t.Fatalf("CreateFileFromString: %v", err)
	}

	data, err := packData(device)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	contents := untarContents(t, data)

	if got := contents["hostlab/test_device/text.txt"]; got != "line1\nline2\n" {
		t.Errorf("text.txt = %q, want the normalised form", got)
	}
	if got := contents["hostlab/test_device/blob.bin"]; got != binary {
		t.Errorf("blob.bin = %q, want the original bytes", got)
	}
}

// TestExtractTar is the `retrieve_files` half: a fully trusted `extractall`.
func TestExtractTar(t *testing.T) {
	dst := t.TempDir()
	archive := tarWithFile(t, "etc/motd", "hello\n")

	if err := extractTar(bytes.NewReader(archive), dst); err != nil {
		t.Fatalf("extractTar: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dst, "etc", "motd"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(content) != "hello\n" {
		t.Errorf("content = %q", content)
	}
}

func untarNames(t *testing.T, data []byte) []string {
	t.Helper()
	names := make([]string, 0, 8)
	for name := range untarContents(t, data) {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func untarContents(t *testing.T, data []byte) map[string]string {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer func() { _ = zr.Close() }()

	contents := map[string]string{}
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
			t.Fatalf("read member: %v", err)
		}
		contents[header.Name] = string(body)
	}
	return contents
}

// TestPackDataFollowsSymlinkedDirectory is the Kubernetes half of the same
// `copy_fs` walk: pyfilesystem types every entry with `os.DirEntry.is_dir()`,
// which follows symlinks, so a device folder holding `linkdir -> real/` ships
// the target's contents at `linkdir/`. Oracle-verified: `copy_fs` writes
// `/linkdir/f.txt` next to `/real/f.txt`.
func TestPackDataFollowsSymlinkedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {

		t.Skip("symlinks are not creatable unprivileged on Windows")
	}
	root := t.TempDir()
	writeHostFile(t, filepath.Join(root, "test_device", "real", "f.txt"), "inner\n")
	writeHostFile(t, filepath.Join(root, "test_device", "plain.txt"), "plain\n")
	hostSymlink(t, "real", filepath.Join(root, "test_device", "linkdir"))
	hostSymlink(t, "plain.txt", filepath.Join(root, "test_device", "linkfile"))
	device := newDeviceOnDisk(t, root)

	data, err := packData(device)
	if err != nil {
		t.Fatalf("packData: %v", err)
	}
	if data == nil {
		t.Fatal("archive = nil, want content")
	}

	names := untarNames(t, data)
	if !slices.Contains(names, "hostlab/test_device/linkdir/") {
		t.Errorf("hostlab/test_device/linkdir/ missing from %v", names)
	}
	contents := untarContents(t, data)
	for name, want := range map[string]string{
		"hostlab/test_device/linkdir/f.txt": "inner\n",
		"hostlab/test_device/real/f.txt":    "inner\n",
		"hostlab/test_device/linkfile":      "plain\n",
		"hostlab/test_device/plain.txt":     "plain\n",
	} {
		if got, ok := contents[name]; !ok || got != want {
			t.Errorf("member %q = %q, %v; want %q", name, got, ok, want)
		}
	}
}

// TestPackDataSymlinkLoopFailsWithELOOP is that walk meeting a cycle: neither
// side detects one, so the path grows until the kernel refuses it. Python
// surfaces it as `fs.errors.OperationFailed, [Errno 40] Too many levels of
// symbolic links` (verified live); here it is ELOOP. Either way the walk
// terminates with an error instead of recursing forever.
func TestPackDataSymlinkLoopFailsWithELOOP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are not creatable unprivileged on Windows")
	}
	root := t.TempDir()
	writeHostFile(t, filepath.Join(root, "test_device", "keep.txt"), "keep\n")
	hostSymlink(t, ".", filepath.Join(root, "test_device", "self"))
	device := newDeviceOnDisk(t, root)

	if _, err := packData(device); !errors.Is(err, syscall.ELOOP) {
		t.Errorf("packData over a symlink loop = %v, want ELOOP", err)
	}
}

// newDeviceOnDisk is the `default_device` fixture in a scenario backed by the
// already-seeded host directory root, which is what gives it symlinks to trip
// over. The device is built last: its filesystem is bound when it is created.
func newDeviceOnDisk(t *testing.T, root string) *model.Machine {
	t.Helper()

	lab, err := model.NewLabWithPath("default_scenario", root, testDefaults(testSettings()))
	if err != nil {
		t.Fatalf("NewLabWithPath: %v", err)
	}
	device, err := lab.NewMachine("test_device", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	return device
}

func writeHostFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func hostSymlink(t *testing.T, target, link string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}
