// This file is the third rendering of ERROR_CODES.md §0.1 — the two the CLI
// owns. Human mode is `src/kathara.py:102-108`'s
// `logging.critical(f"({type(e).__name__}) {str(e)}")`; json/jsonl mode is
// JSON_CLI_CONTRACT.md §5's `{"error":{…}}`, with the structured fields of §5.4
// recovered from the `kerrors` types by `errors.As`.

package cliout

import (
	"errors"
	"fmt"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/labfile"
	"github.com/KatharaFramework/kathara-go/model"
)

// humanLabeler is an error that names the Python class the human line should
// print, for the classes ERROR_CODES.md buckets into `InternalError` but that
// Python still renders under their own name — [ErrPromptEOF]'s `EOFError`, and
// [model.PyRuntimeError]'s `TypeError`/`AttributeError`/`KeyError`/
// `CreateFailed`.
//
// Without it the `err-nonexistent-dir` golden's
// `CRITICAL (CreateFailed) root path '…' does not exist` would render as
// `CRITICAL (InternalError) …`, because the code is all the registry can say
// about an error it does not carry.
type humanLabeler interface{ HumanLabel() string }

// HumanLabelOf is the `{label}` of `CRITICAL ({label}) {message}`.
//
// It prefers an error's own claim over the registry's, then falls back to
// `kerrors.HumanLabel(kerrors.Code(err))`, which is the Python class name for
// every mapped class.
func HumanLabelOf(err error) string {
	var labeler humanLabeler
	if errors.As(err, &labeler) {
		return labeler.HumanLabel()
	}
	var py *model.PyRuntimeError
	if errors.As(err, &py) && py.Class != "" {
		return py.Class
	}
	return kerrors.HumanLabel(kerrors.Code(err))
}

// EmitError renders err and reports the exit code, which is always 1 (§5.5).
//
// Human mode prints the CRITICAL line to **stdout**, because that is where
// Python's `RichHandler` puts it (JSON_CLI_CONTRACT.md A1) and where the Layer
// A goldens record it. With `debug_level == "EXCEPTION"` Python prints the same
// line plus a traceback; the port appends the wrapped-error chain, which is the
// Go analogue of the frames.
func (c *Console) EmitError(err error) int {
	if err == nil {
		return 0
	}
	switch c.Format {
	case FormatJSON:
		obj := newObj().Raw("error", encodeErrorObject(err))
		if batch := kerrors.Joined(err); len(batch) > 1 {
			items := make([][]byte, 0, len(batch))
			for _, e := range batch {
				items = append(items, encodeErrorObject(e))
			}
			obj.RawArray("errors", items)
		}
		c.mu.Lock()
		_, _ = c.Out.Write(obj.Bytes())
		_, _ = fmt.Fprintln(c.Out)
		c.mu.Unlock()
	case FormatJSONL:
		obj := newObj().Str("type", "error").Raw("error", encodeErrorObject(err))
		c.mu.Lock()
		_, _ = c.Out.Write(obj.Bytes())
		_, _ = fmt.Fprintln(c.Out)
		c.mu.Unlock()
	default:
		c.Log(LevelCritical, "(%s) %s", HumanLabelOf(err), err.Error())
		if c.Traceback {
			for cause := errors.Unwrap(err); cause != nil; cause = errors.Unwrap(cause) {
				c.Log(LevelCritical, "  caused by: %s", cause.Error())
			}
		}
	}
	return 1
}

// encodeErrorObject builds the inner `error` object of §5.1: `code`, then
// `message`, then the per-code structured fields of §5.4 in the order that
// table lists them.
func encodeErrorObject(err error) []byte {
	o := newObj()
	o.Str("code", kerrors.Code(err))
	o.Str("message", err.Error())
	addErrorFields(o, err)
	return o.Bytes()
}

// addErrorFields is §5.4's table. Each arm is one row; a field is emitted only
// when the raise site knew it, which is what `errors.As` reports.
func addErrorFields(o *jobj, err error) {
	// The data-bearing classes come first: each of them fully determines its
	// own field set, so a match here is exhaustive for that error.
	var binary *kerrors.BinaryError
	if errors.As(err, &binary) {
		o.Str("binary", binary.Binary)
		o.Str("machine", binary.Machine)
		return
	}
	var imageArch *kerrors.ImageArchError
	if errors.As(err, &imageArch) {
		o.Str("image", imageArch.Image)
		o.Str("arch", imageArch.Arch)
		return
	}
	var nonSeq *kerrors.NonSeqInterfaceError
	if errors.As(err, &nonSeq) {
		o.Int("iface", nonSeq.Iface)
		o.Str("machine", nonSeq.Machine)
		return
	}
	var mac *kerrors.MacAddressError
	if errors.As(err, &mac) {
		o.Str("mac", mac.MAC)
		o.Int("iface", mac.Iface)
		o.Str("machine", mac.Machine)
		return
	}
	var cd *kerrors.CollisionDomainError
	if errors.As(err, &cd) {
		// Variant 1 of ERROR_CODES.md §2 names an interface number instead of
		// a collision domain: "Interface {n} already set on device `{m}`."
		o.Str("machine", cd.Machine)
		if cd.Link != "" {
			o.Str("link", cd.Link)
			return
		}
		o.Int("iface", cd.Iface)
		return
	}
	var option *kerrors.OptionError
	if errors.As(err, &option) {
		o.Str("machine", option.Machine)
		o.Str("option", option.Option)
		return
	}
	var machineSet *kerrors.MachineSetError
	if errors.As(err, &machineSet) {
		o.Strings("machines", machineSet.Machines)
		return
	}
	var hostArch *kerrors.HostArchError
	if errors.As(err, &hostArch) {
		o.Str("arch", hostArch.Arch)
		return
	}
	var feature *kerrors.FeatureNotAvailableError
	if errors.As(err, &feature) {
		o.Str("feature", feature.Feature)
		return
	}
	var notFound *kerrors.SettingsNotFoundError
	if errors.As(err, &notFound) {
		o.Str("path", notFound.Path)
		return
	}
	var parse *labfile.ParseError
	if errors.As(err, &parse) {
		if parse.File != "" {
			o.Str("file", parse.File)
		}
		if parse.Line != 0 {
			o.Int("line", parse.Line)
		}
		return
	}

	// The generic wrappers come last: they attach one field to a class
	// sentinel and can wrap any of the codes above, so they must not shadow
	// them.
	var machine *kerrors.MachineError
	if errors.As(err, &machine) {
		o.Str("machine", machine.Machine)
		return
	}
	var link *kerrors.LinkError
	if errors.As(err, &link) {
		o.Str("link", link.Link)
		return
	}
	var image *kerrors.ImageError
	if errors.As(err, &image) {
		o.Str("image", image.Image)
		return
	}
	var path *kerrors.PathError
	if errors.As(err, &path) {
		o.Str("path", path.Path)
		return
	}
}
