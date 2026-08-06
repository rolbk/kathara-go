// Package event is the port of `Kathara/event/EventDispatcher.py` (PORT_SPEC
// §3.1 row 13, §0.2 #13): the fan-out that lets the backends tell a UI what
// they are doing without importing one. The backends dispatch
// ([Dispatch]); the CLI subscribes ([Subscribe]) and, when it is done, tears
// its subscriptions down ([Unsubscribe]).
//
// # What the redesign replaces
//
// 3.8.3 subscribes an *object* and a *method name*:
// `register("machine_deployed", handler, "update")` stores
// `getattr(obj, method or 'run')` in a list keyed by the event's string name,
// and `dispatch("machine_deployed", item=machine)` calls each stored callable
// with those keyword arguments. Two things are unchecked there: nothing
// guarantees the object has the method (an `AttributeError` at registration),
// and nothing guarantees the subscriber's signature matches the kwargs the
// dispatch site passes (a `TypeError` at deploy time, oracle-probed).
//
// The port keeps the fan-out and drops the reflection. Each of the 19 events
// is a struct whose fields are that dispatch site's kwargs (events.go), the
// table is keyed by the payload's own type, and a subscriber is the method
// value itself — `Subscribe(d, bar.Update)`, where Go infers the event from
// the parameter type. A subscriber wired to the wrong event no longer
// compiles.
//
// The generic entry points are constrained by [Payload], the 19 payload
// structs spelled as a union, and not by the [Event] interface they satisfy.
// That is deliberate: it keeps the event name and the stored handler type in
// bijection, so the two instantiations a Go caller reaches by accident —
// `Subscribe(d, func(*LinkDeployed) error)` and `Dispatch(d, someEventValue)`
// with an interface-typed value — are compile errors rather than, respectively,
// a panic and a dispatch that silently reaches nobody. Neither failure has a
// Python analogue: a string-keyed `dispatch` always reaches the callbacks it
// stored.
//
// # What could not change
//
//   - **Registration order.** Subscribers run in the order they subscribed.
//     `machine_deployed` has two in the stock CLI and the order is visible:
//     the progress bar advances, then the terminal window opens
//     (`cli/ui/event/register.py:72,81`, ORDERING.tsv rows 96-97).
//   - **The wire names.** They are the contract the CLI and any embedder
//     subscribe by, so they survive as [Name] constants;
//     testdata/catalog.json is generated from the Python sources and
//     TestCatalogMatchesPythonSources fails if this package and 3.8.3 ever
//     disagree about the set.
//   - **Silence on an unknown event.** Dispatching an event nobody subscribed
//     to is a no-op, and so is unsubscribing one — `register.py:37,44`
//     unsubscribes `machine_deployed` twice and relies on it.
//   - **Errors travel.** No `try` wraps the callback loop, so a subscriber
//     that raises aborts the ones behind it and unwinds into the dispatching
//     thread. [Dispatch] returns that error instead. The reachable case is
//     [DockerImageUpdateFound]: its subscriber pulls an image, and a pull can
//     fail.
//   - **Teardown hooks.** A subscriber with an `unregister` method gets it
//     called when its event is torn down, and it is stored per subscription,
//     not per object: the progress-bar handler subscribed to three events is
//     told three times.
//
// # Mid-dispatch mutation
//
// Subscribers mutate the table from inside callbacks, and CPython's list and
// dict semantics decide what happens next. `dispatch` iterates the list object
// itself, while `unregister` deletes the *key*, so:
//
//   - subscribing from inside a dispatch appends to the list being walked, and
//     the new subscriber runs in that same dispatch;
//   - unsubscribing from inside a dispatch fires every hook at once and drops
//     the key, but the walk holds the list and the remaining subscribers still
//     run;
//   - subscribing again after that lands in a fresh list, which the walk in
//     flight never sees.
//
// None of it is designed, all of it is reachable — the Kubernetes watcher and
// the deploy workers dispatch concurrently with the caller — so
// [Dispatcher] reproduces it: the table holds `*subscriberList` values whose
// identity survives the key's deletion, and the two loops re-read the list on
// every step rather than snapshotting it. testdata/dispatch_traces.json is
// recorded from 3.8.3 and oracle_test.go replays all 16 scenarios.
//
// # Concurrency
//
// Python needs no lock: the GIL makes the append and the delete atomic and
// releases between callbacks. Go does, because `machine_deployed`,
// `link_deployed`, `machine_undeployed` and `link_undeployed` are dispatched
// from pool workers and from the Kubernetes watcher goroutines while the
// `_started`/`_ended` events fire on the caller's (CONCURRENCY.tsv row 39).
// [Dispatcher] holds its mutex across table access only, never across a
// callback — subscribers dispatch, subscribe and unsubscribe from inside
// callbacks, and a lock held over the callback would deadlock on the first one
// to do it.
//
// Subscribers themselves must be goroutine-safe. That obligation moves with
// the events: it is the progress bar's problem, not this package's.
//
// # Payload variance between backends
//
// Two events carry different types depending on who dispatched them, and the
// port keeps both rather than picking one: Docker sends the device object
// while Kubernetes sends the device name ([MachineDeployed],
// [MachineUndeployed]), and the undeploy events carry SDK handles no model
// type can describe ([APIObject]). PACKAGE_GRAPH.md §2.7 pins that as
// documented struct fields; see the individual types for which backend fills
// which.
//
// # Dependencies
//
// `model` and the standard library, nothing else (PACKAGE_GRAPH.md §1.2). The
// payloads name [model.Lab], [model.Machine] and [model.Link]; the backend SDK
// objects stay opaque precisely so this package does not have to import a
// backend, which sits above it.
package event
