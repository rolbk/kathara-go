package event

import (
	"sync"

	"github.com/KatharaFramework/kathara-go/model"
)

// Dispatcher implements `event/EventDispatcher.py`: a table of event name
// to subscriber list, walked in registration order.
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

	onUnsubscribe func() error
}

// New returns an empty Dispatcher.
func New() *Dispatcher { return &Dispatcher{} }

var defaultDispatcher = New()

// Default returns the process-wide Dispatcher, the stand-in for
// `EventDispatcher.get_instance()`. Library callers that want isolation — the
// tests here, and any embedder running two managers — use [New] instead and
// pass the result down; nothing in this package reaches for the default on its
// own.
func Default() *Dispatcher { return defaultDispatcher }

// NameOf returns the wire name of an event type, e.g.
// `NameOf[LinkDeployed]() == NameLinkDeployed`.
func NameOf[E Payload]() Name {
	var zero E
	return zero.eventName()
}

// Subscribe registers handle for event E.
func Subscribe[E Payload](d *Dispatcher, handle func(E) error) {
	d.subscribe(NameOf[E](), &subscription{handle: handle})
}

// SubscribeWithHook is [Subscribe] for a subscriber that also wants to be told
// when its event is torn down: onUnsubscribe is the `unregister` method 3.8.3
// finds with `hasattr(obj, 'unregister')` and stores next to the callback
// (`EventDispatcher.py:68`).
func SubscribeWithHook[E Payload](d *Dispatcher, handle func(E) error, onUnsubscribe func() error) {
	d.subscribe(NameOf[E](), &subscription{handle: handle, onUnsubscribe: onUnsubscribe})
}

// Dispatch delivers e to every subscriber of E, in registration order, and
// returns nil when they all succeed. It implements
// `dispatch(event, **kwargs)` (`EventDispatcher.py:72-86`); the keyword
// arguments are the fields of e.
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
// registration order — this implementation of `unregister(event)`
// (`EventDispatcher.py:88-104`). Unsubscribing an event nobody subscribed to
// does nothing, and so does unsubscribing one twice: `register.py:37,44`
// unregisters `machine_deployed` a second time and 3.8.3 returns at the `not
// in self.events` guard.
func Unsubscribe[E Payload](d *Dispatcher) error {
	return d.unsubscribe(NameOf[E]())
}

// Subscribers returns the handlers registered for E, in registration order —
// this implementation of `get_subscribers(event)`, which drops the `unregister` half of
// each tuple (`EventDispatcher.py:37-46`).
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
