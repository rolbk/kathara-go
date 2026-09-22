package cliout

import (
	"errors"
	"fmt"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/labfile"
	"github.com/KatharaFramework/kathara-go/model"
)

type humanLabeler interface{ HumanLabel() string }

// HumanLabelOf is the `{label}` of `CRITICAL ({label}) {message}`.
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

func (c *Console) EmitError(err error) int {
	if err == nil {
		return 0
	}
	batch := kerrors.Joined(err)
	primary := primaryOf(err, batch)
	switch c.Format {
	case FormatJSON:
		obj := newObj().Raw("error", encodeErrorObject(primary))
		if len(batch) > 1 {
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
		obj := newObj().Str("type", "error").Raw("error", encodeErrorObject(primary))
		c.mu.Lock()
		_, _ = c.Out.Write(obj.Bytes())
		_, _ = fmt.Fprintln(c.Out)
		c.mu.Unlock()
	default:
		c.Log(LevelCritical, "(%s) %s", HumanLabelOf(primary), primary.Error())
		if c.Traceback {
			for cause := errors.Unwrap(primary); cause != nil; cause = errors.Unwrap(cause) {
				c.Log(LevelCritical, "  caused by: %s", cause.Error())
			}
		}
	}
	return 1
}

func primaryOf(err error, batch []error) error {
	if len(batch) == 0 {
		return err
	}
	return batch[0]
}

func encodeErrorObject(err error) []byte {
	o := newObj()
	o.Str("code", kerrors.Code(err))
	o.Str("message", err.Error())
	addErrorFields(o, err)
	return o.Bytes()
}

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
