package labfile

import (
	"strconv"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ParseError is a failure that names the scenario file and the line it was
// found on — the shape ERROR_CODES.md §0.3 freezes for this package.
//
// Python raises two different classes from those sites and the difference is
// observable in the JSON envelope, so [ParseError.Code] carries it: every
// message of `LabParser` and `DepParser` is a `SyntaxError` except the reserved
// device name of `LabParser.py:55`, which is a `ValueError`.
//
// The struct is what makes `file` and `line` available as JSON fields; the
// rendered text is [ParseError.Msg] and the constructors below are the only
// places the templates live.
type ParseError struct {
	// File is the configuration file the failure was found in — "lab.conf",
	// the `--config` name that replaced it, or "lab.dep". It is empty for the
	// sites that carry no file.
	File string
	// Line is the 1-based line number, or 0 when the failure is not tied to
	// one.
	Line int
	// Msg is the fully rendered Python message, byte-for-byte.
	Msg string
	// Code is the JSON code: [kerrors.CodeSyntax] (the default, and what the
	// zero value means) or [kerrors.CodeValue].
	Code string
}

// Error returns the rendered Python message.
func (e *ParseError) Error() string { return e.Msg }

// ErrorCode implements [kerrors.Coder]. It is spelled this way, and not `Code`,
// because the field of that name is part of the frozen struct.
func (e *ParseError) ErrorCode() string {
	if e.Code == kerrors.CodeValue {
		return kerrors.CodeValue
	}
	return kerrors.CodeSyntax
}

// Unwrap exposes the class sentinel, so `errors.Is(err, kerrors.ErrSyntax)`
// works on a ParseError exactly as it does on the errors kerrors builds.
func (e *ParseError) Unwrap() error {
	if e.Code == kerrors.CodeValue {
		return kerrors.ErrValue
	}
	return kerrors.ErrSyntax
}

// newLabSyntax is `In {conf_name} - Line {line_number}: {inner}`, the template
// of `LabParser.py:65,71,83`. inner is already punctuated by its caller,
// because the three call sites punctuate differently.
func newLabSyntax(confName string, line int, inner string) *ParseError {
	return &ParseError{
		File: confName,
		Line: line,
		Msg:  "In " + confName + " - Line " + strconv.Itoa(line) + ": " + inner,
		Code: kerrors.CodeSyntax,
	}
}

// newLabReservedName is `LabParser.py:55`. It is the one parse failure Python
// raises as a ValueError rather than a SyntaxError (ERROR_CODES.md §2 Value).
func newLabReservedName(confName string, line int, key string) *ParseError {
	return &ParseError{
		File: confName,
		Line: line,
		Msg: "In " + confName + " - Line " + strconv.Itoa(line) + ": `" + key +
			"` is a reserved name, you can not use it for a device.",
		Code: kerrors.CodeValue,
	}
}

// newDepSyntax is `In lab.dep - Line {line_number}.` (`DepParser.py:67`). It
// quotes no line: lab.dep's failure says only where it happened, unlike
// lab.conf's, which interpolates the raw line.
func newDepSyntax(line int) *ParseError {
	return &ParseError{
		File: DepName,
		Line: line,
		Msg:  "In " + DepName + " - Line " + strconv.Itoa(line) + ".",
		Code: kerrors.CodeSyntax,
	}
}

// tooManyValues is CPython's own text for `(a, b) = seq` with a longer
// sequence, and notEnoughValues is its counterpart for a shorter one, `got`
// being the length CPython reports.
//
// Both strings are CPython runtime detail that Go can only hard-code
// (DIVERGENCES.md 8, README SURPRISE 27), and both are user-visible: a `LAB_*`
// value containing a second `=` escapes uncaught with the first
// (`LabParser.py:85`), and `OptionParser` wraps either one in its own sentence.
const (
	tooManyValues        = "too many values to unpack (expected 2)"
	notEnoughValuesUntil = "not enough values to unpack (expected 2, got "
)

// errTooManyValues is the bare, uncaught ValueError of `LabParser.py:85` — no
// file, no line number and no mention of lab.conf (DIVERGENCES.md 5).
var errTooManyValues = kerrors.NewValue(tooManyValues)

// notEnoughValues renders CPython's short-sequence message for a sequence of
// length got.
func notEnoughValues(got int) string {
	return notEnoughValuesUntil + strconv.Itoa(got) + ")"
}
