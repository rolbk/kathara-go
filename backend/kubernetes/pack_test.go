package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
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
//
// The BYTES are not compared against Python's — its archive carries
// `time.time()` mtimes and a gzip header that differ run to run (SYNTHESIS
// §1.4, "goldens compare extracted content") — so what is checked is what comes
// out of the untar.
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
