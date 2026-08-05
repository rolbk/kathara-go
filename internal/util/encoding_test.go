package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// The whole suite is driven by testdata/encoding/expected.json, recorded from
// the real Python by tools/vectorcheck/encoding_probe.py. Python is the truth:
// a disagreement means the Go port is wrong, unless it is one of the
// impossible-parity items listed in docs/port/SPIKES/encoding.md.

type fixture struct {
	Name                string    `json:"name"`
	Size                int       `json:"size"`
	SHA256              string    `json:"sha256"`
	InputHex            *string   `json:"input_hex"`
	IsBinary            bool      `json:"is_binary"`
	IsBinaryContentOnly bool      `json:"is_binary_content_only"`
	HasBinaryExtension  *bool     `json:"has_binary_extension"`
	ChunkSize           *int      `json:"chunk_size"`
	ChunkLen            *int      `json:"chunk_len"`
	Features            []float64 `json:"features"`
	FeaturesHex         []string  `json:"features_hex"`
	IsBinaryV044        *bool     `json:"is_binary_v044"`
	ReadHex             *string   `json:"read_hex"`
	ReadSHA256          *string   `json:"read_sha256"`
	ReadSize            *int      `json:"read_size"`
	ReadIsNone          *bool     `json:"read_is_none"`
	ReadError           *string   `json:"read_error"`
	WriteReturnsNone    *bool     `json:"write_returns_none"`
	WriteResultHex      *string   `json:"write_result_hex"`
	WriteResultSHA256   *string   `json:"write_result_sha256"`
	WriteResultSize     *int      `json:"write_result_size"`
	WriteChanged        *bool     `json:"write_changed"`
	WriteError          *string   `json:"write_error"`
}

type expectations struct {
	BinaryOrNot string    `json:"binaryornot"`
	Chardet     string    `json:"chardet"`
	Python      string    `json:"python"`
	Fixtures    []fixture `json:"fixtures"`
}

const (
	encodingTestdata = "testdata/encoding"
	// oracleBinaryOrNot is the binaryornot release expected.json was recorded
	// against. Kathara's own dependency is the open-ended `binaryornot>=0.4.4`,
	// so a venv rebuild can silently move the oracle; the guard below turns
	// that into a loud test failure instead of a mystery parity regression.
	oracleBinaryOrNot = "0.6.0"
)

func loadExpectations(t *testing.T) expectations {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(encodingTestdata, "expected.json"))
	if err != nil {
		t.Fatalf("read expected.json: %v", err)
	}
	var exp expectations
	if err := json.Unmarshal(raw, &exp); err != nil {
		t.Fatalf("parse expected.json: %v", err)
	}
	if len(exp.Fixtures) == 0 {
		t.Fatal("expected.json has no fixtures")
	}
	if exp.BinaryOrNot != oracleBinaryOrNot {
		t.Fatalf("expected.json was recorded against binaryornot %s, this port targets %s; "+
			"see docs/port/SPIKES/encoding.md before re-recording",
			exp.BinaryOrNot, oracleBinaryOrNot)
	}
	return exp
}

func fixturePath(name string) string {
	return filepath.Join(encodingTestdata, "fixtures", name)
}

// TestIsBinaryParity is the headline claim: every fixture's binary/text verdict
// matches binaryornot's, extension shortcut included.
func TestIsBinaryParity(t *testing.T) {
	exp := loadExpectations(t)
	for _, f := range exp.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			got, err := IsBinary(fixturePath(f.Name))
			if err != nil {
				t.Fatalf("IsBinary: %v", err)
			}
			if got != f.IsBinary {
				t.Errorf("IsBinary = %v, Python = %v", got, f.IsBinary)
			}
			if f.HasBinaryExtension != nil {
				if gotExt := HasBinaryExtension(fixturePath(f.Name)); gotExt != *f.HasBinaryExtension {
					t.Errorf("HasBinaryExtension = %v, Python = %v", gotExt, *f.HasBinaryExtension)
				}
			}
		})
	}
}

// TestIsBinaryStringParity checks the content-only verdict — is_binary with
// check_extensions=False — so an extension-table hit cannot mask a wrong
// decision-tree result underneath it.
func TestIsBinaryStringParity(t *testing.T) {
	exp := loadExpectations(t)
	for _, f := range exp.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			raw, err := os.ReadFile(fixturePath(f.Name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			chunk := raw
			if len(chunk) > binaryChunkSize {
				chunk = chunk[:binaryChunkSize]
			}
			if f.ChunkLen != nil && len(chunk) != *f.ChunkLen {
				t.Fatalf("chunk length %d, Python read %d", len(chunk), *f.ChunkLen)
			}
			if got := IsBinaryString(chunk); got != f.IsBinaryContentOnly {
				t.Errorf("IsBinaryString = %v, Python = %v", got, f.IsBinaryContentOnly)
			}
		})
	}
}

// entropyFeature is index 7, byte_entropy — the one feature that provably
// cannot be reproduced bit for bit. CPython's math.log2 delegates to the
// platform libm, whose log2 is not correctly rounded (measured on this
// container's glibc: 257 of the 131 328 reachable count/len ratios round
// differently from the exact value), so the Python side is not even stable
// across hosts. Go's math.Log2 is Log(frac)*(1/Ln2), which disagrees with
// glibc on ~20% of those ratios by up to 2 ulp.
//
// Measured consequence over a 20 000-chunk fuzz corpus spanning eight byte
// distributions: entropy differed on 4.16% of chunks by at most 52 ulp
// (4.6e-14 absolute), every other feature was bit-identical, and the
// binary/text verdict differed on 0 chunks. The tree's nineteen entropy
// thresholds are six-decimal sklearn split points; the closest any sample got
// to one was 8.9e-6, a margin of 1.9e8 over the worst drift.
//
// So: exact equality for every feature except entropy, which gets a tolerance
// four orders of magnitude tighter than the closest observed threshold
// approach and five orders looser than the worst observed drift.
const (
	entropyFeature   = 7
	entropyTolerance = 1e-9
)

// TestFeatureVectorParity compares all 24 features against Python. It is the
// early-warning system for the decision tree: if a threshold comparison ever
// flips, this says which feature drifted rather than leaving a bare
// binary/text mismatch to debug.
func TestFeatureVectorParity(t *testing.T) {
	exp := loadExpectations(t)
	names := []string{
		"null_ratio", "control_ratio", "printable_ascii_ratio", "high_byte_ratio",
		"utf8_valid", "even_null_ratio", "odd_null_ratio", "byte_entropy",
		"bom_utf32le", "bom_utf32be", "bom_utf16le", "bom_utf16be", "bom_utf8",
		"try_utf16le", "try_utf16be", "try_utf32le", "try_utf32be",
		"longest_printable_run", "try_gb2312", "try_big5", "try_shift_jis",
		"try_euc_jp", "try_euc_kr", "has_magic_signature",
	}
	for _, f := range exp.Fixtures {
		if f.Features == nil {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			raw, err := os.ReadFile(fixturePath(f.Name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			chunk := raw
			if len(chunk) > binaryChunkSize {
				chunk = chunk[:binaryChunkSize]
			}
			got := computeBinaryFeatures(chunk)
			if len(f.Features) != binaryFeatureCount {
				t.Fatalf("expected.json has %d features, want %d", len(f.Features), binaryFeatureCount)
			}
			for i := range got {
				want := f.Features[i]
				if len(f.FeaturesHex) == binaryFeatureCount {
					// float.hex() is exact; the JSON number is too, but this
					// removes any doubt about the decoder.
					if parsed, err := parseFloatHex(f.FeaturesHex[i]); err == nil {
						want = parsed
					}
				}
				if i == entropyFeature {
					if math.Abs(got[i]-want) > entropyTolerance {
						t.Errorf("feature %d (%s) = %v, Python = %v, delta %.3e exceeds the "+
							"documented libm-log2 tolerance %.0e",
							i, names[i], got[i], want, math.Abs(got[i]-want), entropyTolerance)
					}
					continue
				}
				if got[i] != want {
					t.Errorf("feature %d (%s) = %v (%s), Python = %v (%s), ulp diff %d",
						i, names[i], got[i], strconv.FormatFloat(got[i], 'x', -1, 64),
						want, strconv.FormatFloat(want, 'x', -1, 64), ulpDiff(got[i], want))
				}
			}
		})
	}
}

// parseFloatHex reads CPython's float.hex() output ("0x1.5p+3"), which is the
// C99 hex-float form Go's strconv accepts verbatim.
func parseFloatHex(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

func ulpDiff(a, b float64) int64 {
	ai := int64(math.Float64bits(a))
	bi := int64(math.Float64bits(b))
	if ai < bi {
		return bi - ai
	}
	return ai - bi
}

// TestConvertWin2LinuxReadParity pins the read-mode bytes: BOM stripping,
// newline collapsing, and the verbatim fallback for anything that is not valid
// UTF-8.
func TestConvertWin2LinuxReadParity(t *testing.T) {
	exp := loadExpectations(t)
	for _, f := range exp.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			got, err := ConvertWin2Linux(fixturePath(f.Name))
			if f.ReadError != nil {
				if err == nil {
					t.Fatalf("ConvertWin2Linux succeeded, Python raised %s", *f.ReadError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ConvertWin2Linux: %v", err)
			}
			if got == nil {
				t.Fatal("ConvertWin2Linux returned nil; read mode always yields bytes")
			}
			if f.ReadSize == nil || f.ReadSHA256 == nil {
				t.Fatalf("expected.json has no read digest for %s", f.Name)
			}
			if len(got) != *f.ReadSize {
				t.Errorf("read %d bytes, Python read %d", len(got), *f.ReadSize)
			}
			if digest := fmt.Sprintf("%x", sha256.Sum256(got)); digest != *f.ReadSHA256 {
				t.Errorf("read sha256 = %s, Python = %s", digest, *f.ReadSHA256)
			}
			// Oversized outputs carry only the digest; the rest are compared
			// byte for byte so a failure names the offending offset.
			if f.ReadHex != nil {
				if gotHex := hex.EncodeToString(got); gotHex != *f.ReadHex {
					t.Errorf("read bytes = %s\nPython      = %s", abbrev(gotHex), abbrev(*f.ReadHex))
				}
			}
		})
	}
}

// TestConvertWin2LinuxWriteParity pins the mutating mode, including the two
// silent no-ops: a binary file and a text-looking file that will not decode
// are both left byte-identical on disk.
func TestConvertWin2LinuxWriteParity(t *testing.T) {
	exp := loadExpectations(t)
	for _, f := range exp.Fixtures {
		t.Run(f.Name, func(t *testing.T) {
			raw, err := os.ReadFile(fixturePath(f.Name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			work := filepath.Join(t.TempDir(), f.Name)
			if err := os.WriteFile(work, raw, 0o644); err != nil {
				t.Fatalf("stage fixture: %v", err)
			}

			err = ConvertWin2LinuxInPlace(work)
			if f.WriteError != nil {
				if err == nil {
					t.Fatalf("ConvertWin2LinuxInPlace succeeded, Python raised %s", *f.WriteError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ConvertWin2LinuxInPlace: %v", err)
			}
			after, err := os.ReadFile(work)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if f.WriteResultSize != nil && len(after) != *f.WriteResultSize {
				t.Errorf("size after write = %d, Python = %d", len(after), *f.WriteResultSize)
			}
			if f.WriteResultHex != nil {
				if gotHex := hex.EncodeToString(after); gotHex != *f.WriteResultHex {
					t.Errorf("bytes after write = %s\nPython            = %s",
						abbrev(gotHex), abbrev(*f.WriteResultHex))
				}
			}
			if f.WriteChanged != nil {
				changed := string(after) != string(raw)
				if changed != *f.WriteChanged {
					t.Errorf("write_changed = %v, Python = %v", changed, *f.WriteChanged)
				}
			}
		})
	}
}

// TestReplaceChainIsDeadCode pins the finding that Python's
// `.replace("\n\r", "\n").replace("\r\n", "\n")` inside convert_win_2_linux can
// never fire: universal-newline text mode has already removed every CR by the
// time it runs. If this ever fails, the newline model in normalizeText is
// wrong and root-utils.md line 475 was right after all.
func TestReplaceChainIsDeadCode(t *testing.T) {
	exp := loadExpectations(t)
	decoded := 0
	for _, f := range exp.Fixtures {
		if f.ReadHex == nil || f.IsBinary {
			continue // binary files are passed through verbatim, CRs and all
		}
		raw, err := os.ReadFile(fixturePath(f.Name))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		if !utf8.Valid(bytes.TrimPrefix(raw, utf8BOM)) {
			continue // Python's raw fallback, CRs legitimately survive
		}
		decoded++
		out, err := hex.DecodeString(*f.ReadHex)
		if err != nil {
			t.Fatalf("%s: bad read_hex: %v", f.Name, err)
		}
		if bytes.ContainsRune(out, '\r') {
			t.Errorf("%s: Python's own output still contains CR, so the replace chain is live", f.Name)
		}
		// The literal Python replace chain, applied to Python's own output.
		// It must be a no-op; if it is not, normalizeText's model is wrong.
		chained := strings.NewReplacer("\n\r", "\n").Replace(string(out))
		chained = strings.ReplaceAll(chained, "\r\n", "\n")
		if chained != string(out) {
			t.Errorf("%s: replace chain changed the output, so it is not dead code", f.Name)
		}
	}
	if decoded < 10 {
		t.Fatalf("only %d fixtures exercised the decode path; the corpus lost coverage", decoded)
	}
}

// TestPathlibSuffix pins the pathlib.Path.suffix semantics has_binary_extension
// relies on, which filepath.Ext does not share.
func TestPathlibSuffix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"foo.png", ".png"},
		{"/a/b/foo.PNG", ".PNG"},
		{"foo.tar.gz", ".gz"},
		{".bashrc", ""},    // filepath.Ext says ".bashrc"
		{"/x/.bashrc", ""}, //
		{"dump.", ""},
		{"noext", ""},
		{"", ""},
		{"/", ""},
		{"a/b/", ""},
		{"..", ""},
		{"x..png", ".png"},
	}
	for _, c := range cases {
		if got := pathlibSuffix(c.in); got != c.want {
			t.Errorf("pathlibSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The consequences that matter to the port.
	if !HasBinaryExtension("machine1/etc/config.gz") {
		t.Error("a .gz lab file must be treated as binary")
	}
	if HasBinaryExtension("machine1/.bashrc") {
		t.Error(".bashrc must not be treated as binary")
	}
}

// TestIsBinaryMissingFile pins that the sniff reports the OS error rather than
// guessing, matching Python's FileNotFoundError escaping is_binary.
func TestIsBinaryMissingFile(t *testing.T) {
	if _, err := IsBinary(filepath.Join(t.TempDir(), "nope.txt")); err == nil {
		t.Fatal("IsBinary on a missing file must fail")
	}
	// ...unless the extension short-circuits first, exactly as in Python.
	got, err := IsBinary(filepath.Join(t.TempDir(), "nope.png"))
	if err != nil {
		t.Fatalf("extension shortcut must not touch the filesystem: %v", err)
	}
	if !got {
		t.Fatal("a missing .png is still reported binary by the extension table")
	}
}

// TestEmptyFileIsNotNil pins the NILABILITY.tsv row: an empty text file yields
// an empty, non-nil slice, which is distinct from Python's None.
func TestEmptyFileIsNotNil(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ConvertWin2Linux(p)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("empty text file must yield a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("got %q, want empty", got)
	}
}

func abbrev(s string) string {
	if len(s) <= 160 {
		return s
	}
	return s[:80] + "..." + s[len(s)-40:] + " (len " + strconv.Itoa(len(s)) + ")"
}
