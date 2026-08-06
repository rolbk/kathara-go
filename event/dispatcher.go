package event

import (
	"sync"

	"github.com/KatharaFramework/kathara-go/model"
)

// Dispatcher is the port of `event/EventDispatcher.py`: a table of event name
// to subscriber list, walked in registration order.
//
// The zero value is ready to use; [New] exists for symmetry and [Default]
// returns the process-wide one that replaces `EventDispatcher.get_instance()`.
// A Dispatcher must not be copied after first use — it holds a mutex — so it
// is always handled through a pointer.
//
// # Why the internals look like a Python list
//
// Every method below manipulates a `*subscriberList` rather than a plain
// slice, and the two loops re-read that list on each step instead of taking a
// snapshot. That is deliberate: it reproduces the identity semantics of
// `self.events[event]`, which are observable whenever a subscriber mutates the
// table from inside a callback. testdata/dispatch_traces.json records what
// 3.8.3 does in each of those cases; oracle_test.go replays them.
type Dispatcher struct {
	mu     sync.Mutex
	events map[Name]*subscriberList
}

// subscriberList is the list object living at `self.events[event]`. Its
// identity matters: `unregister` deletes the *key* (`EventDispatcher.py:104`)
// while a running `dispatch` still holds the list, and a later `register`
// builds a *new* list under the same key (`EventDispatcher.py:62-63`). Holding
// a pointer to the list, not to the map entry, is what makes those two facts
// come out right.
type subscriberList struct {
	subs []*subscription
}

// subscription is one `(run_callback, unregister_callback)` tuple
// (`EventDispatcher.py:65-70`).
type subscription struct {
	// handle is the `func(E) error` this subscription was created with, held
	// as any because the table is keyed by event name and cannot be generic.
	// Only [Subscribe] and [SubscribeWithHook] write it, and the [Payload]
	// constraint makes the name it is filed under and its func type determine
	// each other, so the assertion in [Dispatch] cannot fail.
	handle any
	// onUnsubscribe is the hook [Unsubscribe] calls, or nil for a subscriber
	// that has none — 3.8.3 stores None when the object has no `unregister`
	// attribute (`EventDispatcher.py:68`, NILABILITY.tsv row 149).
	onUnsubscribe func() error
}

// New returns an empty Dispatcher.
func New() *Dispatcher { return &Dispatcher{} }

// defaultDispatcher is the process-wide instance. 3.8.3 makes the dispatcher a
// singleton and raises `InstantiationError` on a second construction
// (`EventDispatcher.py:24-35`); that guard has no Go equivalent and
// ERROR_CODES.tsv drops it, so [New] is free and this is simply the one the
// CLI wires its subscribers into.
var defaultDispatcher = New()

// Default returns the process-wide Dispatcher, the stand-in for
// `EventDispatcher.get_instance()`. Library callers that want isolation — the
// tests here, and any embedder running two managers — use [New] instead and
// pass the result down; nothing in this package reaches for the default on its
// own.
func Default() *Dispatcher { return defaultDispatcher }

// NameOf returns the wire name of an event type, e.g.
// `NameOf[LinkDeployed]() == NameLinkDeployed`.
//
// It is total, and the [Payload] constraint is what makes it so: E is one of 19
// struct types, its zero value is a struct and not a nil interface or a nil
// pointer, and `eventName` reads no field. There is no instantiation of this
// function that compiles and panics (PORT_SPEC §10).
func NameOf[E Payload]() Name {
	var zero E
	return zero.eventName()
}

// Subscribe registers handle for event E. It is the port of
// `register(event, obj, method)` for an object with no `unregister` attribute:
// subscribers are appended, and [Dispatch] calls them in that order
// (`EventDispatcher.py:62-70`, ORDERING.tsv row 97).
//
// The `method` argument disappears with `getattr`: where 3.8.3 names the
// callback with a string and falls back to `run`, Go passes the method value
// itself, so `Subscribe(d, bar.Init)` and `Subscribe(d, bar.Update)` are two
// subscriptions on one object with no reflection in between. E is inferred
// from handle.
//
// Subscribing on a nil Dispatcher does nothing, which leaves the caller in the
// state a Python process with no `register_cli_events()` call is in: events
// fire into an empty table.
func Subscribe[E Payload](d *Dispatcher, handle func(E) error) {
	d.subscribe(NameOf[E](), &subscription{handle: handle})
}

// SubscribeWithHook is [Subscribe] for a subscriber that also wants to be told
// when its event is torn down: onUnsubscribe is the `unregister` method 3.8.3
// finds with `hasattr(obj, 'unregister')` and stores next to the callback
// (`EventDispatcher.py:68`).
//
// The hook belongs to the *subscription*, not to the subscriber, exactly as in
// Python: one `HandleProgressBar` subscribes to three events
// (`register.py:59-61`) and its `finish` runs once per event torn down, and a
// subscriber that subscribes to one event three times is asked three times
// (trace `same_subscriber_registered_three_times`).
func SubscribeWithHook[E Payload](d *Dispatcher, handle func(E) error, onUnsubscribe func() error) {
	d.subscribe(NameOf[E](), &subscription{handle: handle, onUnsubscribe: onUnsubscribe})
}

// Dispatch delivers e to every subscriber of E, in registration order, and
// returns nil when they all succeed. It is the port of
// `dispatch(event, **kwargs)` (`EventDispatcher.py:72-86`); the keyword
// arguments are the fields of e.
//
// An event nobody subscribed to is dropped silently, as
// `if event not in self.events: return` does (`EventDispatcher.py:82-83`).
//
// # Errors
//
// 3.8.3 wraps nothing in a try block, so a subscriber that raises aborts the
// remaining subscribers and unwinds into the dispatching thread — where, for
// the deploy fan-out, it becomes that pool worker's exception (CONCURRENCY.tsv
// row 39). Dispatch returns the first such error and stops for the same
// reason, leaving the subscriber list untouched. The reachable case is
// [DockerImageUpdateFound], whose subscriber pulls an image and can fail
// offline.
func Dispatch[E Payload](d *Dispatcher, e E) error {
	return d.walk(e.eventName(), func(s *subscription) error {
		handle, ok := s.handle.(func(E) error)
		if !ok {
			// Unreachable: [Payload] closes E over the 19 payload structs, each
			// with its own name, so every subscription filed under
			// `e.eventName()` holds a `func(E) error`. Skipping beats panicking
			// if that invariant is ever broken.
			return nil
		}
		return handle(e)
	})
}

// Unsubscribe drops every subscriber of E after calling their hooks in
// registration order — the port of `unregister(event)`
// (`EventDispatcher.py:88-104`). Unsubscribing an event nobody subscribed to
// does nothing, and so does unsubscribing one twice: `register.py:37,44`
// unregisters `machine_deployed` a second time and 3.8.3 returns at the `not
// in self.events` guard.
//
// The hooks are what stops the progress bars: `unregister_cli_events()` runs
// on every exit path of the entrypoint, Ctrl-C included
// (`src/kathara.py:61,66,75,83,87,93,100,107`), and each hook is
// `HandleProgressBar.finish`. By then the deploy pools have joined, which is
// why 3.8.3 gets away with tearing the table down without a lock.
//
// A hook that fails aborts the teardown at that hook and the event stays
// subscribed, because the `del self.events[event]` sits *after* the hook loop
// and never runs (trace
// `hook_exception_aborts_unregister_and_skips_the_delete`).
//
// A hook that unsubscribes the same event reentrantly is the mirror image: the
// inner call deletes the key, the outer loop still finishes its hooks, and then
// the outer `del` has nothing to delete and raises. That KeyError comes back
// here, as a [model.PyRuntimeError] of class KeyError
// (TestReentrantUnsubscribeFromAHookIsAKeyError).
func Unsubscribe[E Payload](d *Dispatcher) error {
	return d.unsubscribe(NameOf[E]())
}

// Subscribers returns the handlers registered for E, in registration order —
// the port of `get_subscribers(event)`, which drops the `unregister` half of
// each tuple (`EventDispatcher.py:37-46`).
//
// It indexes `self.events[event]` with no guard, so an event nobody subscribed
// to is a `KeyError` and not an empty list; the port returns the same failure
// as a [model.PyRuntimeError] of class KeyError, testable with
// `errors.Is(err, model.ErrPyKeyError)`. The returned slice is a copy: 3.8.3
// builds a fresh list too, and mutating it must not touch the table.
func Subscribers[E Payload](d *Dispatcher) ([]func(E) error, error) {
	name := NameOf[E]()
	if d == nil {
		return nil, newKeyError(name)
	}

	// The lookup and the copy are one critical section: CPython evaluates the
	// list comprehension without yielding, so no concurrent subscribe can land
	// between them there either.
	d.mu.Lock()
	defer d.mu.Unlock()

	list, ok := d.events[name]
	if !ok {
		return nil, newKeyError(name)
	}
	out := make([]func(E) error, 0, len(list.subs))
	for _, s := range list.subs {
		if handle, ok := s.handle.(func(E) error); ok {
			out = append(out, handle)
		}
	}
	return out, nil
}

// newKeyError is `self.events[event]` on a missing key. CPython's KeyError
// carries `repr(key)`, and every event name is a plain ASCII identifier, so
// the repr is the name in single quotes.
func newKeyError(name Name) error {
	return &model.PyRuntimeError{Class: "KeyError", Msg: "'" + string(name) + "'"}
}

// ---------------------------------------------------------------------------
// The table, and the locking around it
// ---------------------------------------------------------------------------
//
// Python needs no lock here: the GIL makes each list append and each dict
// delete atomic, and it is released between callbacks. Go needs one, because
// the backends dispatch from the deploy and undeploy workers and the
// Kubernetes watcher goroutines while the caller's goroutine dispatches the
// `_started`/`_ended` events (CONCURRENCY.tsv row 39).
//
// The lock is held only across table access, never across a callback. Holding
// it over a callback would deadlock the moment a subscriber dispatched,
// subscribed or unsubscribed — all three of which subscribers legitimately do
// — and would serialize the progress bar behind the deploy. That mirrors the
// GIL's behaviour of yielding between callbacks, which is what makes the
// mid-dispatch mutation traces reproducible at all.

// subscribe is `if event not in self.events: self.events[event] = []` followed
// by `self.events[event].append(...)` (`EventDispatcher.py:62-70`).
func (d *Dispatcher) subscribe(name Name, s *subscription) {
	if d == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.events == nil {
		d.events = make(map[Name]*subscriberList)
	}
	list, ok := d.events[name]
	if !ok {
		list = &subscriberList{}
		d.events[name] = list
	}
	list.subs = append(list.subs, s)
}

// listOf resolves the list at a key, or nil when the key is absent — the `not
// in self.events` guard both `dispatch` and `unregister` open with.
func (d *Dispatcher) listOf(name Name) *subscriberList {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	return d.events[name]
}

// at returns the i-th subscriber of list, or nil once the list is exhausted.
// Re-reading the length on every step is what makes a subscriber registered
// from inside a dispatch run in that same dispatch, the way appending to a
// list CPython is iterating does (trace
// `register_during_dispatch_is_seen_by_that_dispatch`).
//
// It is only ever reached through a non-nil list, which a nil Dispatcher
// cannot produce.
func (d *Dispatcher) at(list *subscriberList, i int) *subscription {
	d.mu.Lock()
	defer d.mu.Unlock()

	if i >= len(list.subs) {
		return nil
	}
	return list.subs[i]
}

// walk is the `for (run_callback, _) in self.events[event]` loop
// (`EventDispatcher.py:85-86`). It resolves the list once and then iterates
// *that list*, so an unsubscribe from inside a callback — which deletes the
// key but not the list — does not stop the subscribers that follow (trace
// `unregister_during_dispatch_still_runs_the_rest`), and a re-subscribe after
// that unsubscribe files into a fresh list this loop never sees (trace
// `unregister_then_register_during_dispatch`).
func (d *Dispatcher) walk(name Name, call func(*subscription) error) error {
	list := d.listOf(name)
	if list == nil {
		return nil
	}

	for i := 0; ; i++ {
		s := d.at(list, i)
		if s == nil {
			return nil
		}
		if err := call(s); err != nil {
			return err
		}
	}
}

// unsubscribe is `unregister(event)` (`EventDispatcher.py:88-104`): every hook
// in order, then the key. The delete is deliberately last, and deliberately
// skipped when a hook fails.
func (d *Dispatcher) unsubscribe(name Name) error {
	list := d.listOf(name)
	if list == nil {
		return nil
	}

	for i := 0; ; i++ {
		s := d.at(list, i)
		if s == nil {
			break
		}
		if s.onUnsubscribe == nil {
			continue
		}
		if err := s.onUnsubscribe(); err != nil {
			return err
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// `del self.events[event]` (`EventDispatcher.py:104`), key check included:
	// the guard at the top of the function ran before the hooks, and a hook
	// that unsubscribed this event reentrantly — or a concurrent goroutine that
	// did — has taken the key away since. CPython raises KeyError there; Go's
	// `delete` would not. The test is on the key's *presence*, not on the
	// list's identity, so a hook that unsubscribes and then subscribes again
	// finds a fresh list under the key and the delete succeeds, as it does in
	// 3.8.3.
	if _, ok := d.events[name]; !ok {
		return newKeyError(name)
	}
	delete(d.events, name)
	return nil
}
