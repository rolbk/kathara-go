package event

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/model"
)

// testdata/dispatch_traces.json was recorded by driving the real 3.8.3
// `EventDispatcher` (CPython, `/root/kathara/pyvenv`) through each scenario's
// steps and writing down every callback that fired, in order. This file
// replays the same steps against [Dispatcher] and compares the logs entry for
// entry — which is the only way to pin the mid-dispatch mutation behaviour,
// since it falls out of CPython's list and dict semantics and not out of
// anything the source says.
//
// The two events the scenarios use are real catalog entries, so the KeyError
// text a trace records is the text this package must produce.

type traceFile struct {
	Note      string      `json:"note"`
	Scenarios []traceCase `json:"scenarios"`
}

type traceCase struct {
	Name  string      `json:"name"`
	Note  string      `json:"note"`
	Steps []traceStep `json:"steps"`
	Log   []string    `json:"log"`
}

type traceStep struct {
	Op     string        `json:"op"`
	Event  string        `json:"event"`
	Tag    string        `json:"tag"`
	Hook   bool          `json:"hook"`
	OnRun  []traceAction `json:"on_run"`
	OnHook []traceAction `json:"on_hook"`
}

type traceAction struct {
	Do    string `json:"do"`
	Event string `json:"event"`
	Tag   string `json:"tag"`
	Hook  bool   `json:"hook"`
	Msg   string `json:"msg"`
}

// raised is a subscriber blowing up: 3.8.3's `RuntimeError`, which propagates
// out of `dispatch` untouched.
type raised struct{ msg string }

func (e *raised) Error() string { return e.msg }

// replay executes a scenario's steps against one Dispatcher.
type replay struct {
	t   *testing.T
	d   *Dispatcher
	log []string

	// probing switches the subscribers to reporting their tag instead of
	// running, so a `subscribers` step can name the handlers [Subscribers]
	// handed back without their side effects firing.
	probing bool
	probed  []string
}

// eventOps binds the generic API to one concrete event type. Resolving a
// trace's event *name* to one of these is the whole of the replay's dynamic
// dispatch, and it lives here in the test rather than in the package — which
// is the point of the redesign.
type eventOps struct {
	subscribe   func(r *replay, tag string, hook bool, onRun, onHook []traceAction)
	dispatch    func(r *replay) error
	unsubscribe func(r *replay) error
	subscribers func(r *replay) ([]string, error)
}

func opsFor[E Payload]() eventOps {
	var zero E
	return eventOps{
		subscribe: func(r *replay, tag string, hook bool, onRun, onHook []traceAction) {
			handle := func(E) error {
				if r.probing {
					r.probed = append(r.probed, tag)
					return nil
				}
				r.log = append(r.log, "run:"+tag)
				return r.act(onRun)
			}
			if !hook {
				Subscribe(r.d, handle)
				return
			}
			SubscribeWithHook(r.d, handle, func() error {
				r.log = append(r.log, "hook:"+tag)
				return r.act(onHook)
			})
		},
		dispatch:    func(r *replay) error { return Dispatch(r.d, zero) },
		unsubscribe: func(r *replay) error { return Unsubscribe[E](r.d) },
		subscribers: func(r *replay) ([]string, error) {
			handlers, err := Subscribers[E](r.d)
			if err != nil {
				return nil, err
			}
			r.probing = true
			defer func() { r.probing = false }()

			tags := make([]string, 0, len(handlers))
			for _, handle := range handlers {
				r.probed = r.probed[:0]
				_ = handle(zero)
				tags = append(tags, r.probed...)
			}
			return tags, nil
		},
	}
}

// opsFor binds a trace's event name to the generic API. It is a function and
// not a table because the ops close over [replay.act], which calls back into
// it — a package-level map would be an initialization cycle.
func (r *replay) opsFor(name string) eventOps {
	switch Name(name) {
	case NameLinkDeployed:
		return opsFor[LinkDeployed]()
	case NameMachineDeployed:
		return opsFor[MachineDeployed]()
	default:
		r.t.Fatalf("trace uses event %q, which the replay does not bind", name)
		return eventOps{}
	}
}

// act runs what a subscriber does from inside its callback.
func (r *replay) act(actions []traceAction) error {
	for _, a := range actions {
		switch a.Do {
		case "register":
			r.opsFor(a.Event).subscribe(r, a.Tag, a.Hook, nil, nil)
		case "unregister":
			if err := r.opsFor(a.Event).unsubscribe(r); err != nil {
				return err
			}
		case "dispatch":
			if err := r.opsFor(a.Event).dispatch(r); err != nil {
				return err
			}
		case "raise":
			return &raised{msg: a.Msg}
		default:
			r.t.Fatalf("unknown action %q", a.Do)
		}
	}
	return nil
}

func (r *replay) step(s traceStep) {
	ops := r.opsFor(s.Event)
	switch s.Op {
	case "register":
		ops.subscribe(r, s.Tag, s.Hook, s.OnRun, s.OnHook)
	case "dispatch":
		if err := ops.dispatch(r); err != nil {
			r.log = append(r.log, "err:"+pyError(err))
		}
	case "unregister":
		if err := ops.unsubscribe(r); err != nil {
			r.log = append(r.log, "err:"+pyError(err))
		}
	case "subscribers":
		tags, err := ops.subscribers(r)
		if err != nil {
			r.log = append(r.log, "subs:!"+pyError(err))
			return
		}
		r.log = append(r.log, "subs:"+strings.Join(tags, ","))
	default:
		r.t.Fatalf("unknown op %q", s.Op)
	}
}

// pyError renders an error the way the trace recorder rendered the exception
// that escaped: `{type(e).__name__}:{e}`.
func pyError(err error) string {
	var py *model.PyRuntimeError
	if errors.As(err, &py) {
		return py.Class + ":" + py.Msg
	}
	var boom *raised
	if errors.As(err, &boom) {
		return "RuntimeError:" + boom.msg
	}
	return "UnexpectedGoError:" + err.Error()
}

func TestDispatcherAgainstOracleTraces(t *testing.T) {
	data, err := os.ReadFile("testdata/dispatch_traces.json")
	if err != nil {
		t.Fatalf("read traces: %v", err)
	}
	var file traceFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse traces: %v", err)
	}
	if len(file.Scenarios) == 0 {
		t.Fatal("no scenarios in testdata/dispatch_traces.json")
	}

	for _, tc := range file.Scenarios {
		t.Run(tc.Name, func(t *testing.T) {
			r := &replay{t: t, d: New()}
			for _, s := range tc.Steps {
				r.step(s)
			}
			if !slices.Equal(r.log, tc.Log) {
				t.Errorf("%s\ngot  %q\nwant %q (3.8.3)\nnote: %s",
					tc.Name, r.log, tc.Log, tc.Note)
			}
		})
	}
	t.Logf("%d oracle scenarios replayed", len(file.Scenarios))
}
