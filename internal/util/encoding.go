// This file covers the encoding half of utils.py: utils.convert_win_2_linux
// and the binaryornot `is_binary` heuristic it gates on. Together they decide
// which lab files get their UTF-8 BOM stripped and their line endings
// collapsed on the way into a device, and which are shipped through byte for
// byte. A misclassification here silently corrupts a startup script, so the
// Python behaviour is reproduced exactly rather than approximated; see
// docs/port/SPIKES/encoding.md for the parity analysis and the open items.
//
// The oracle is binaryornot 0.6.0, the release `pip install kathara==3.8.3`
// resolves today (its dependency is `binaryornot>=0.4.4`). That release
// classifies by file extension, magic prefix and a trained decision tree over
// byte statistics. It is NOT the 1024-byte translate-table heuristic of
// binaryornot 0.4.4, which src/requirements.txt pins and which cannot be
// ported at all because its verdict depends on chardet's confidence score.
package util

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// binaryChunkSize is binaryornot.helpers.CHUNK_SIZE: only the first 512 bytes
// of a file are ever examined. (0.4.4 read 1024; see the spike doc.)
const binaryChunkSize = 512

// binaryFeatureCount is the width of the vector binaryDecisionTree consumes.
const binaryFeatureCount = 24

// utf8BOM is the byte sequence Python's "utf-8-sig" codec strips from the
// front of a file, and only from the front.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// byteRange is an inclusive byte interval, used by the generated codec tables.
type byteRange struct{ lo, hi byte }

func (r byteRange) contains(b byte) bool { return b >= r.lo && b <= r.hi }

func inRanges(rs []byteRange, b byte) bool {
	for _, r := range rs {
		if r.contains(b) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// convert_win_2_linux
// ---------------------------------------------------------------------------

// ConvertWin2Linux is utils.convert_win_2_linux(filename) — the read-mode half.
//
// A file the binary sniff calls text is decoded as UTF-8 with the BOM stripped
// and its line endings normalised to LF, then re-encoded; anything else (a
// binary file, or a text-looking file that is not valid UTF-8, e.g. latin-1 or
// UTF-16) is returned verbatim. Python distinguishes "no result" (write mode)
// from "empty result" via Optional[bytes]; in read mode a result is always
// produced, so this returns []byte and never nil without an error — an empty
// file yields an empty, non-nil slice (NILABILITY.tsv, convert_win_2_linux).
//
// Errors mirror Python's: the stat/read behind the binary sniff propagates,
// and so does a failure of the verbatim fallback read. A UTF-8 decode failure
// is not an error — it is the fallback trigger.
func ConvertWin2Linux(path string) ([]byte, error) {
	binary, err := IsBinary(path)
	if err != nil {
		return nil, err
	}
	if !binary {
		if normalized, ok, err := normalizeText(path); err == nil && ok {
			return normalized, nil
		}
		// Python's blanket `except Exception` makes every failure here —
		// decode error, permission error, a race that deleted the file —
		// fall through to the verbatim read, which then reports it.
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if raw == nil {
		raw = []byte{}
	}
	return raw, nil
}

// ConvertWin2LinuxInPlace is utils.convert_win_2_linux(filename, write=True) —
// the mutating half, used by Machine.pack_data on every file copied into the
// hostlab tar.
//
// Only a file the sniff calls text AND that decodes as UTF-8 is rewritten
// (BOM-less, LF-only, UTF-8). For every other file this is a silent no-op:
// Python returns a bare None and leaves the bytes alone.
//
// PARITY WART: Python performs the rewrite inside the same blanket
// `except Exception`, so a failed write — read-only file, full disk — is
// swallowed too and the caller packs the un-normalised file without ever
// learning. That is reproduced here (the error is deliberately dropped at the
// marked line). SPIKES/encoding.md OQ-E4 proposes diverging.
func ConvertWin2LinuxInPlace(path string) error {
	binary, err := IsBinary(path)
	if err != nil {
		return err
	}
	if binary {
		return nil
	}
	normalized, ok, err := normalizeText(path)
	if err != nil || !ok {
		return nil
	}
	// Deliberately discarded: see the PARITY WART note above. O_TRUNC without
	// O_CREATE-with-mode keeps the existing file's permission bits, as
	// Python's open(mode="w") does.
	_ = os.WriteFile(path, normalized, 0o666)
	return nil
}

// normalizeText is the body of Python's `try` block: open the file with the
// "utf-8-sig" codec in universal-newline text mode, read it, and re-encode as
// UTF-8. ok is false when that would have raised UnicodeDecodeError, i.e. when
// the file is not valid UTF-8 and Python would fall through to the raw read.
//
// Two Python details are load-bearing:
//
//   - "utf-8-sig" strips exactly one leading BOM. A BOM anywhere else, and a
//     second consecutive BOM, survive as U+FEFF.
//   - text mode defaults to newline=None, i.e. universal newlines, so the
//     decoder has already turned every CRLF *and every lone CR* into LF before
//     Python's own `.replace("\n\r", "\n").replace("\r\n", "\n")` runs. That
//     replace chain is therefore dead code — no CR can reach it — and lone CRs
//     are normalised despite root-utils.md line 475 claiming they survive.
//     TestReplaceChainIsDeadCode pins this.
//
// Because the decoded text is re-encoded straight back to UTF-8 and the only
// edit is an ASCII-range newline rewrite, the whole round trip is expressible
// on the bytes; no decode step is needed.
func normalizeText(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	body := bytes.TrimPrefix(raw, utf8BOM)
	if !utf8.Valid(body) {
		return nil, false, nil
	}
	return normalizeNewlines(body), true, nil
}

// normalizeNewlines is Python's universal-newline translation: CRLF and lone
// CR both become LF, an existing LF is untouched.
func normalizeNewlines(b []byte) []byte {
	if bytes.IndexByte(b, '\r') < 0 {
		out := make([]byte, len(b))
		copy(out, b)
		return out
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
// binaryornot
// ---------------------------------------------------------------------------

// IsBinary is binaryornot.check.is_binary(filename), extension check included
// (Kathara calls it with the default check_extensions=True).
//
// Order matters and is surprising: the extension is consulted *before* the
// file is opened, so an empty file called "x.bin" is binary while an empty
// file called "x.txt" is text.
func IsBinary(path string) (bool, error) {
	if HasBinaryExtension(path) {
		return true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Python's f.read(512) keeps reading until it has 512 bytes or hits EOF;
	// a bare f.Read may stop short of that on a pipe or a slow filesystem.
	chunk := make([]byte, binaryChunkSize)
	n, err := io.ReadFull(f, chunk)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return IsBinaryString(chunk[:n]), nil
}

// HasBinaryExtension is binaryornot.helpers.has_binary_extension.
//
// The extension is taken with pathlib's `Path.suffix` semantics, which are not
// filepath.Ext's: a dotfile such as ".bashrc" has no suffix in pathlib, and a
// trailing-dot name such as "dump." has none either.
func HasBinaryExtension(path string) bool {
	ext := strings.TrimLeft(strings.ToLower(pathlibSuffix(path)), ".")
	_, ok := binaryExtensions[ext]
	return ok
}

// pathlibSuffix reproduces pathlib.PurePath.suffix:
//
//	name = self.name; i = name.rfind('.')
//	return name[i:] if 0 < i < len(name) - 1 else ''
func pathlibSuffix(path string) string {
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	i := strings.LastIndexByte(name, '.')
	if i > 0 && i < len(name)-1 {
		return name[i:]
	}
	return ""
}

// IsBinaryString is binaryornot.helpers.is_binary_string.
//
// Three stages, in this order: empty is text, a known magic prefix is binary,
// otherwise the trained decision tree decides. Note that several of the magic
// prefixes are two bytes ("BM" for bmp, "MZ" for dos exe), so ordinary English
// prose starting with those letters is classified binary.
func IsBinaryString(chunk []byte) bool {
	if len(chunk) == 0 {
		return false
	}
	if hasKnownBinarySignature(chunk) {
		return true
	}
	features := computeBinaryFeatures(chunk)
	return binaryDecisionTree(&features)
}

func hasKnownBinarySignature(chunk []byte) bool {
	for _, sig := range binaryMagics {
		if len(chunk) >= len(sig) && bytes.Equal(chunk[:len(sig)], sig) {
			return true
		}
	}
	return false
}

// computeBinaryFeatures is binaryornot.helpers._compute_features. The index
// comments are that function's docstring; the tree in encoding_tree.go
// addresses these slots positionally, so the order is a hard contract.
//
// Callers must not pass an empty chunk: Python divides by len(chunk) here and
// relies on is_binary_string having already short-circuited.
func computeBinaryFeatures(chunk []byte) [binaryFeatureCount]float64 {
	var f [binaryFeatureCount]float64
	n := len(chunk)
	fn := float64(n)

	var nullCount, controlCount, printableCount, highCount int
	var hist [256]int
	for _, b := range chunk {
		hist[b]++
		if b == 0 {
			nullCount++
		}
		// _CONTROL_BYTES = set(range(0, 32)) - {9, 10, 13}. NUL, VT and FF are
		// in it, contrary to the docstring's "0x01-0x08, 0x0E-0x1F".
		if b < 32 && b != 9 && b != 10 && b != 13 {
			controlCount++
		}
		if b >= 0x20 && b <= 0x7E {
			printableCount++
		}
		if b >= 0x80 {
			highCount++
		}
	}

	f[0] = float64(nullCount) / fn      // null_ratio
	f[1] = float64(controlCount) / fn   // control_ratio
	f[2] = float64(printableCount) / fn // printable_ascii_ratio
	f[3] = float64(highCount) / fn      // high_byte_ratio

	if utf8.Valid(chunk) { // utf8_valid
		f[4] = 1.0
	}

	evenTotal := (n + 1) / 2
	oddTotal := n / 2
	evenNulls, oddNulls := 0, 0
	for i := 0; i < n; i += 2 {
		if chunk[i] == 0 {
			evenNulls++
		}
	}
	for i := 1; i < n; i += 2 {
		if chunk[i] == 0 {
			oddNulls++
		}
	}
	if evenTotal != 0 {
		f[5] = float64(evenNulls) / float64(evenTotal) // even_null_ratio
	}
	if oddTotal != 0 {
		f[6] = float64(oddNulls) / float64(oddTotal) // odd_null_ratio
	}

	// byte_entropy. The float64() conversions pin the rounding of each term:
	// the Go spec lets an implementation fuse a multiply-add into an FMA
	// (which arm64 does), and an unfused Python accumulation must be matched
	// bit for bit or a threshold comparison in the tree can flip.
	entropy := 0.0
	for _, count := range hist {
		if count > 0 {
			p := float64(count) / fn
			entropy -= float64(p * math.Log2(p))
		}
	}
	f[7] = entropy

	if hasPrefix(chunk, 0xFF, 0xFE, 0x00, 0x00) { // bom_utf32le
		f[8] = 1.0
	}
	if hasPrefix(chunk, 0x00, 0x00, 0xFE, 0xFF) { // bom_utf32be
		f[9] = 1.0
	}
	if hasPrefix(chunk, 0xFF, 0xFE) && !hasPrefix(chunk, 0xFF, 0xFE, 0x00, 0x00) { // bom_utf16le
		f[10] = 1.0
	}
	if hasPrefix(chunk, 0xFE, 0xFF) { // bom_utf16be
		f[11] = 1.0
	}
	if hasPrefix(chunk, 0xEF, 0xBB, 0xBF) { // bom_utf8
		f[12] = 1.0
	}

	if n >= 10 {
		if utf16Decodes(chunk, true) { // try_utf16le
			f[13] = 1.0
		}
		if utf16Decodes(chunk, false) { // try_utf16be
			f[14] = 1.0
		}
	}
	if n >= 16 {
		if utf32Decodes(chunk, true) { // try_utf32le
			f[15] = 1.0
		}
		if utf32Decodes(chunk, false) { // try_utf32be
			f[16] = 1.0
		}
	}

	maxRun, run := 0, 0
	for _, b := range chunk {
		if (b >= 0x20 && b <= 0x7E) || b == 9 || b == 10 || b == 13 {
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	f[17] = float64(maxRun) / fn // longest_printable_run

	if n >= 10 {
		if cjkDecodes(chunk, &gb2312Lead, gb2312Trail, gb2312Trail3) { // try_gb2312
			f[18] = 1.0
		}
		if cjkDecodes(chunk, &big5Lead, big5Trail, big5Trail3) { // try_big5
			f[19] = 1.0
		}
		if cjkDecodes(chunk, &shiftJISLead, shiftJISTrail, shiftJISTrail3) { // try_shift_jis
			f[20] = 1.0
		}
		if cjkDecodes(chunk, &eucJPLead, eucJPTrail, eucJPTrail3) { // try_euc_jp
			f[21] = 1.0
		}
		if cjkDecodes(chunk, &eucKRLead, eucKRTrail, eucKRTrail3) { // try_euc_kr
			f[22] = 1.0
		}
	}

	if hasKnownBinarySignature(chunk) { // has_magic_signature
		f[23] = 1.0
	}
	return f
}

func hasPrefix(chunk []byte, want ...byte) bool {
	if len(chunk) < len(want) {
		return false
	}
	for i, b := range want {
		if chunk[i] != b {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// "does CPython's codec X accept these bytes?" predicates
// ---------------------------------------------------------------------------

// utf16Decodes answers chunk.decode("utf-16-le" / "utf-16-be") without raising.
// CPython's strict UTF-16 decoder rejects a trailing odd byte, a high
// surrogate not followed by a low one, and a bare low surrogate.
func utf16Decodes(chunk []byte, little bool) bool {
	if len(chunk)%2 != 0 {
		return false
	}
	unit := func(i int) uint16 {
		if little {
			return uint16(chunk[i]) | uint16(chunk[i+1])<<8
		}
		return uint16(chunk[i])<<8 | uint16(chunk[i+1])
	}
	for i := 0; i < len(chunk); i += 2 {
		u := unit(i)
		switch {
		case u >= 0xD800 && u <= 0xDBFF:
			if i+2 >= len(chunk) {
				return false
			}
			lo := unit(i + 2)
			if lo < 0xDC00 || lo > 0xDFFF {
				return false
			}
			i += 2
		case u >= 0xDC00 && u <= 0xDFFF:
			return false
		}
	}
	return true
}

// utf32Decodes answers chunk.decode("utf-32-le" / "utf-32-be") without raising:
// a whole number of 4-byte units, each a scalar value (no surrogates, nothing
// above U+10FFFF).
func utf32Decodes(chunk []byte, little bool) bool {
	if len(chunk)%4 != 0 {
		return false
	}
	for i := 0; i < len(chunk); i += 4 {
		var v uint32
		if little {
			v = uint32(chunk[i]) | uint32(chunk[i+1])<<8 | uint32(chunk[i+2])<<16 | uint32(chunk[i+3])<<24
		} else {
			v = uint32(chunk[i])<<24 | uint32(chunk[i+1])<<16 | uint32(chunk[i+2])<<8 | uint32(chunk[i+3])
		}
		if v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF) {
			return false
		}
	}
	return true
}

// cjkDecodes answers chunk.decode(<one of gb2312/big5/shift_jis/euc-jp/euc-kr>)
// without raising, using the accept tables lifted from CPython's own codecs by
// tools/vectorcheck/gen_encoding_tables.py.
//
// CPython's multibyte decoders are single-pass and never backtrack: the lead
// byte commits to a sequence length, and the sequence then either maps to a
// character or raises. A truncated trailing sequence raises too, so running
// off the end is a reject.
func cjkDecodes(chunk []byte, lead *[256]uint8, trail map[byte][]byteRange, trail3 map[byte]map[byte][]byteRange) bool {
	for i := 0; i < len(chunk); {
		b := chunk[i]
		switch lead[b] {
		case 1:
			i++
		case 2:
			if i+1 >= len(chunk) || !inRanges(trail[b], chunk[i+1]) {
				return false
			}
			i += 2
		case 3:
			if i+2 >= len(chunk) {
				return false
			}
			second, ok := trail3[b][chunk[i+1]]
			if !ok || !inRanges(second, chunk[i+2]) {
				return false
			}
			i += 3
		default:
			return false
		}
	}
	return true
}
