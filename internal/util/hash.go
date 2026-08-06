// This file covers the identity half of utils.py: `generate_urlsafe_hash` and
// `slug`, the two functions that turn a lab name, a path or a hostname into the
// token Docker and Kubernetes see.

package util

import (
	"crypto/md5" //nolint:gosec // utils.py:55 hashes with MD5; the digest names resources, it is not a security primitive.
	"encoding/base64"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// GenerateURLSafeHash is utils.generate_urlsafe_hash (utils.py:53).
//
// It is the lab hash (Lab.py:76 and :90), half of the user name
// ([GetCurrentUserName]) and therefore part of every container name, network
// name and label Kathará writes. PORT_SPEC §0.4 forbids changing it.
//
// The four steps, in Python's order:
//
//  1. `re.sub` of the pattern `[^\x00-\x7F]+` with an empty replacement
//     deletes every non-ASCII run. On a
//     valid UTF-8 string that is the same set of bytes as "drop every byte with
//     the high bit set", which is what this does — an ASCII code point is one
//     byte below 0x80, and every byte of a multi-byte sequence is at or above
//     it. NUL and the other C0 controls are inside the kept range and survive.
//  2. `hashlib.md5(...)` over those bytes. The `errors='ignore'` of the
//     `.encode` is dead by then, everything left is ASCII.
//  3. `base64.urlsafe_b64encode(digest)[:-2]` — a 16-byte digest always encodes
//     to 24 characters ending in "==", so the slice drops exactly the padding
//     and leaves 22 characters.
//  4. two `.replace` calls with empty replacements, one for `-` and one for
//     `_`, delete the two non-alphanumeric characters of the URL-safe
//     alphabet. That is why the result is *not* a fixed 22 characters: it is
//     22 minus however many appeared, typically 20 to 22.
func GenerateURLSafeHash(s string) string {
	asciiOnly := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] < 0x80 {
			asciiOnly = append(asciiOnly, s[i])
		}
	}

	digest := md5.Sum(asciiOnly) //nolint:gosec // see the import comment
	encoded := base64.URLEncoding.EncodeToString(digest[:])

	return hashAlphabetTrim.Replace(encoded[:len(encoded)-2])
}

// hashAlphabetTrim is the pair of deleting `.replace` calls that end
// generate_urlsafe_hash. strings.Replacer applies both in one pass; the two
// characters are distinct and neither is produced by removing the other, so a
// single pass and Python's two sequential passes agree.
var hashAlphabetTrim = strings.NewReplacer("-", "", "_", "")

// asciiSpace is the set of ASCII characters that Python's `\s` matches inside a
// `str` pattern and that `str.strip()` removes. It is *not* the six characters
// of `unicode.IsSpace` restricted to ASCII: Python also treats the four file /
// group / record / unit separators, 0x1C to 0x1F, as whitespace, and `slug`
// depends on it (a device name holding a 0x1E becomes a "-", not a deletion).
func asciiSpace(b byte) bool {
	switch b {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x1C, 0x1D, 0x1E, 0x1F:
		return true
	}
	return false
}

// asciiWord is Python's `\w` for a `str` pattern restricted to ASCII, which is
// all `slug` can see: its input has already been through
// `.encode("ascii", "ignore")`.
func asciiWord(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

// Slug is utils.slug (utils.py:230), the ASCII slugifier that finishes
// [GetCurrentUserName].
//
// Python's three statements, kept in order because the order is observable —
// the lowercasing happens *before* the run collapse, and `strip` removes
// whitespace but never the hyphens, so " - " slugs to "-" while "-a-" keeps
// both of its hyphens:
//
//  1. `unicodedata.normalize("NFKD", value).encode("ascii", "ignore")` —
//     compatibility-decompose, then delete everything still non-ASCII. "café"
//     becomes "cafe" because the combining acute is dropped; "日本語" becomes
//     "" because nothing decomposes into ASCII.
//  2. `re.sub(r"[^\w\s-]", "", value).strip().lower()` — keep word characters,
//     whitespace and hyphen; trim; fold case.
//  3. `re.sub(r"[-\s]+", "-", value)` — collapse every run of hyphens and
//     whitespace into one hyphen.
func Slug(value string) string {
	decomposed := norm.NFKD.String(value)

	// encode("ascii", "ignore"), then the [^\w\s-] filter, in one pass: the
	// two are both byte-wise deletions over what is by then a byte string.
	kept := make([]byte, 0, len(decomposed))
	for i := 0; i < len(decomposed); i++ {
		b := decomposed[i]
		if b >= 0x80 {
			continue
		}
		if asciiWord(b) || asciiSpace(b) || b == '-' {
			kept = append(kept, b)
		}
	}

	// .strip()
	start, end := 0, len(kept)
	for start < end && asciiSpace(kept[start]) {
		start++
	}
	for end > start && asciiSpace(kept[end-1]) {
		end--
	}
	kept = kept[start:end]

	// .lower(), then re.sub(r"[-\s]+", "-", ...). Only ASCII is left, so the
	// case fold is the plain one and cannot change any character's class.
	out := make([]byte, 0, len(kept))
	inRun := false
	for _, b := range kept {
		if b == '-' || asciiSpace(b) {
			if !inRun {
				out = append(out, '-')
				inRun = true
			}
			continue
		}
		inRun = false
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		out = append(out, b)
	}

	return string(out)
}
