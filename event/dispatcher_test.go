package event

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/KatharaFramework/kathara-go/model"
)

// recorder collects the tags of the subscribers that fired, in order.
type recorder struct {
	mu   sync.Mutex
	tags []string
}

func (r *recorder) note(tag string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tags = append(r.tags, tag)
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tags...)
}

// noteOf returns a subscriber that records tag and succeeds.
func noteOf[E Payload](r *recorder, tag string) func(E) error {
	return func(E) error {
		r.note(tag)
		return nil
	}
}

// ---------------------------------------------------------------------------
// Ordering
// ---------------------------------------------------------------------------

func TestSubscribersRunInRegistrationOrder(t *testing.T) {
	for _, n := range []int{1, 2, 3, 17, 200} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			d := New()
			rec := &recorder{}
			want := make([]string, 0, n)
			for i := range n {
				tag := fmt.Sprintf("s%d", i)
				want = append(want, tag)
				Subscribe(d, noteOf[LinkDeployed](rec, tag))
			}

			if err := Dispatch(d, LinkDeployed{}); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if got := rec.seen(); !slices.Equal(got, want) {
				t.Errorf("order = %v, want %v", got, want)
			}
		})
	}
}

func TestProgressBarThenTerminalOnMachineDeployed(t *testing.T) {
	d := New()
	rec := &recorder{}

	Subscribe(d, noteOf[MachineDeployed](rec, "progress-bar.update"))
	Subscribe(d, noteOf[MachineDeployed](rec, "terminal.run"))

	if err := Dispatch(d, MachineDeployed{Machine: &model.Machine{Name: "pc1"}}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	want := []string{"progress-bar.update", "terminal.run"}
	if got := rec.seen(); !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestUnsubscribeFiresHooksInRegistrationOrder covers the teardown half of the
// same list (`event/EventDispatcher.py:100-102`), including the subscriber that
// has no hook — 3.8.3 stores None for it and skips it.
func TestUnsubscribeFiresHooksInRegistrationOrder(t *testing.T) {
	d := New()
	rec := &recorder{}

	SubscribeWithHook(d, noteOf[LinkDeployed](rec, "a"), func() error {
		rec.note("hook:a")
		return nil
	})
	Subscribe(d, noteOf[LinkDeployed](rec, "b"))
	SubscribeWithHook(d, noteOf[LinkDeployed](rec, "c"), func() error {
		rec.note("hook:c")
		return nil
	})

	if err := Unsubscribe[LinkDeployed](d); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	want := []string{"hook:a", "hook:c"}
	if got := rec.seen(); !slices.Equal(got, want) {
		t.Errorf("hooks = %v, want %v", got, want)
	}

	// The event is gone: dispatching it is a no-op again.
	if err := Dispatch(d, LinkDeployed{}); err != nil {
		t.Fatalf("Dispatch after Unsubscribe: %v", err)
	}
	if got := rec.seen(); !slices.Equal(got, want) {
		t.Errorf("subscribers survived Unsubscribe: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var errBoom = errors.New("subscriber failed")

func TestDispatchReturnsFirstErrorAndStops(t *testing.T) {
	d := New()
	rec := &recorder{}

	Subscribe(d, noteOf[DockerImageUpdateFound](rec, "head"))
	Subscribe(d, func(DockerImageUpdateFound) error {
		rec.note("boom")
		return errBoom
	})
	Subscribe(d, noteOf[DockerImageUpdateFound](rec, "tail"))

	err := Dispatch(d, DockerImageUpdateFound{ImageName: "kathara/base"})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Dispatch error = %v, want %v", err, errBoom)
	}
	if got, want := rec.seen(), []string{"head", "boom"}; !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v — the subscriber behind the failure must not run", got, want)
	}

	subs, err := Subscribers[DockerImageUpdateFound](d)
	if err != nil {
		t.Fatalf("Subscribers: %v", err)
	}
	if len(subs) != 3 {
		t.Errorf("subscribers = %d, want 3 — a failed dispatch unsubscribes nobody", len(subs))
	}
}

// TestUnsubscribeHookErrorKeepsTheEvent is the `del self.events[event]` that
// sits after the hook loop and is skipped when a hook raises
// (`event/EventDispatcher.py:100-104`).
func TestUnsubscribeHookErrorKeepsTheEvent(t *testing.T) {
	d := New()
	rec := &recorder{}

	SubscribeWithHook(d, noteOf[LinksDeployEnded](rec, "a"), func() error {
		rec.note("hook:a")
		return errBoom
	})
	SubscribeWithHook(d, noteOf[LinksDeployEnded](rec, "b"), func() error {
		rec.note("hook:b")
		return nil
	})

	if err := Unsubscribe[LinksDeployEnded](d); !errors.Is(err, errBoom) {
		t.Fatalf("Unsubscribe error = %v, want %v", err, errBoom)
	}
	if got, want := rec.seen(), []string{"hook:a"}; !slices.Equal(got, want) {
		t.Errorf("hooks = %v, want %v — the hook behind the failure must not run", got, want)
	}

	if err := Dispatch(d, LinksDeployEnded{}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	want := []string{"hook:a", "a", "b"}
	if got := rec.seen(); !slices.Equal(got, want) {
		t.Errorf("after the failed teardown the event must still be subscribed: %v, want %v", got, want)
	}
}

// TestReentrantUnsubscribeFromAHookIsAKeyError: `del self.events[event]`
// (`event/EventDispatcher.py:104`) is an unguarded `del`, and the guard that
// would have protected it (`if event not in self.events: return`) ran before the
// hooks. A hook that unsubscribes its own event takes the key away, the outer
// loop still finishes its hooks off the list it is holding, and then the outer
// `del` raises. Go's `delete` is a silent no-op on a missing key, so the check
// has to be written out.
func TestReentrantUnsubscribeFromAHookIsAKeyError(t *testing.T) {
	d := New()
	rec := &recorder{}

	spent := false
	SubscribeWithHook(d, noteOf[LinkDeployed](rec, "a"), func() error {
		rec.note("hook:a")
		if spent {
			return nil
		}
		spent = true
		return Unsubscribe[LinkDeployed](d)
	})
	SubscribeWithHook(d, noteOf[LinkDeployed](rec, "b"), func() error {
		rec.note("hook:b")
		return nil
	})

	err := Unsubscribe[LinkDeployed](d)
	if !errors.Is(err, model.ErrPyKeyError) {
		t.Fatalf("Unsubscribe = %v, want the KeyError 3.8.3 raises", err)
	}
	if got, want := err.Error(), "'link_deployed'"; got != want {
		t.Errorf("KeyError message = %q, want %q", got, want)
	}
	want := []string{"hook:a", "hook:a", "hook:b", "hook:b"}
	if got := rec.seen(); !slices.Equal(got, want) {
		t.Errorf("hooks = %v, want %v (3.8.3)", got, want)
	}

	// The inner call is the one that deleted the key, so the event is gone.
	if err := Dispatch(d, LinkDeployed{}); err != nil {
		t.Fatalf("Dispatch after the teardown: %v", err)
	}
	if got := rec.seen(); !slices.Equal(got, want) {
		t.Errorf("subscribers survived the teardown: %v", got)
	}
}

// TestHookThatUnsubscribesAndSubscribesAgainDeletesTheFreshList is the other
// half of the same `del`: what it tests is the key's *presence*, not the list
// it was holding. A hook that unsubscribes and then subscribes again leaves a
// fresh list under the key, the outer `del` finds it and removes it — taking
// the new subscriber with it and raising nothing.
func TestHookThatUnsubscribesAndSubscribesAgainDeletesTheFreshList(t *testing.T) {
	d := New()
	rec := &recorder{}

	spent := false
	SubscribeWithHook(d, noteOf[MachineDeployed](rec, "c"), func() error {
		rec.note("hook:c")
		if spent {
			return nil
		}
		spent = true
		if err := Unsubscribe[MachineDeployed](d); err != nil {
			return err
		}
		Subscribe(d, noteOf[MachineDeployed](rec, "d"))
		return nil
	})

	if err := Unsubscribe[MachineDeployed](d); err != nil {
		t.Fatalf("Unsubscribe = %v, want nil — the key is back, so the delete lands", err)
	}
	want := []string{"hook:c", "hook:c"}
	if got := rec.seen(); !slices.Equal(got, want) {
		t.Errorf("hooks = %v, want %v (3.8.3)", got, want)
	}

	// The subscriber the hook added went with the key.
	if err := Dispatch(d, MachineDeployed{Name: "pc1"}); err != nil {
		t.Fatalf("Dispatch after the teardown: %v", err)
	}
	if got := rec.seen(); !slices.Equal(got, want) {
		t.Errorf("the re-subscribed handler survived: %v", got)
	}
}

// ---------------------------------------------------------------------------
// Unknown events
// ---------------------------------------------------------------------------

func TestUnknownEventIsSilentForDispatchAndUnsubscribe(t *testing.T) {
	d := New()

	if err := Dispatch(d, MachineStartupWaitStarted{}); err != nil {
		t.Errorf("Dispatch on an unsubscribed event = %v, want nil", err)
	}
	// register.py:37 and :44 both unregister machine_deployed; the second call
	// must be as quiet as the first.
	for i := range 2 {
		if err := Unsubscribe[MachineDeployed](d); err != nil {
			t.Errorf("Unsubscribe #%d = %v, want nil", i+1, err)
		}
	}
}

// TestSubscribersOnUnknownEventIsAKeyError: `get_subscribers` indexes the dict
// with no guard (`event/EventDispatcher.py:46`), so the empty case is a
// KeyError carrying `repr(event)`.
func TestSubscribersOnUnknownEventIsAKeyError(t *testing.T) {
	d := New()

	subs, err := Subscribers[LinkUndeployed](d)
	if subs != nil {
		t.Errorf("subscribers = %v, want nil", subs)
	}
	if !errors.Is(err, model.ErrPyKeyError) {
		t.Fatalf("error = %v, want a Python KeyError", err)
	}
	if got, want := err.Error(), "'link_undeployed'"; got != want {
		t.Errorf("KeyError message = %q, want %q", got, want)
	}
}

// TestSubscribersReturnsACopy: 3.8.3 builds a fresh list in a comprehension,
// so a caller mutating the result cannot reach the table.
func TestSubscribersReturnsACopy(t *testing.T) {
	d := New()
	rec := &recorder{}
	Subscribe(d, noteOf[LinkDeployed](rec, "a"))

	subs, err := Subscribers[LinkDeployed](d)
	if err != nil {
		t.Fatalf("Subscribers: %v", err)
	}
	subs = append(subs, noteOf[LinkDeployed](rec, "injected"))
	if len(subs) != 2 {
		t.Fatalf("local slice = %d entries, want 2", len(subs))
	}

	if err := Dispatch(d, LinkDeployed{}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got, want := rec.seen(), []string{"a"}; !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v — the returned slice must not be the table", got, want)
	}
}

// ---------------------------------------------------------------------------
// Dispatcher lifecycle
// ---------------------------------------------------------------------------

// TestZeroValueDispatcherWorks: no constructor is required, the map is built
// on first subscribe.
func TestZeroValueDispatcherWorks(t *testing.T) {
	var d Dispatcher
	rec := &recorder{}

	Subscribe(&d, noteOf[MachinesDeployEnded](rec, "a"))
	if err := Dispatch(&d, MachinesDeployEnded{}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got, want := rec.seen(), []string{"a"}; !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
}

func TestNilDispatcherIsInert(t *testing.T) {
	var d *Dispatcher
	rec := &recorder{}

	Subscribe(d, noteOf[LinkDeployed](rec, "a"))
	SubscribeWithHook(d, noteOf[LinkDeployed](rec, "b"), func() error {
		rec.note("hook:b")
		return nil
	})
	if err := Dispatch(d, LinkDeployed{}); err != nil {
		t.Errorf("Dispatch on nil = %v, want nil", err)
	}
	if err := Unsubscribe[LinkDeployed](d); err != nil {
		t.Errorf("Unsubscribe on nil = %v, want nil", err)
	}
	if _, err := Subscribers[LinkDeployed](d); !errors.Is(err, model.ErrPyKeyError) {
		t.Errorf("Subscribers on nil = %v, want a KeyError", err)
	}
	if got := rec.seen(); len(got) != 0 {
		t.Errorf("a nil dispatcher fired %v", got)
	}
}

// TestDefaultIsTheSameDispatcher pins the stand-in for
// `EventDispatcher.get_instance()`: one instance per process, and it works.
func TestDefaultIsTheSameDispatcher(t *testing.T) {
	first, second := Default(), Default()
	if first == nil {
		t.Fatal("Default() = nil")
	}
	if first != second {
		t.Fatal("Default() returned two different dispatchers")
	}
	if fresh := New(); first == fresh {
		t.Fatal("New() returned the default dispatcher")
	}

	rec := &recorder{}
	Subscribe(Default(), noteOf[MachineStartupWaitEnded](rec, "a"))
	t.Cleanup(func() {
		if err := Unsubscribe[MachineStartupWaitEnded](Default()); err != nil {
			t.Errorf("cleanup Unsubscribe: %v", err)
		}
	})

	if err := Dispatch(Default(), MachineStartupWaitEnded{}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got, want := rec.seen(), []string{"a"}; !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestConcurrentDispatchAndSubscribe(t *testing.T) {
	d := New()
	rec := &recorder{}

	const workers = 8
	const perWorker = 50

	Subscribe(d, noteOf[MachineDeployed](rec, "bar"))

	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range perWorker {
				if err := Dispatch(d, MachineDeployed{Name: fmt.Sprintf("pc%d-%d", w, i)}); err != nil {
					t.Errorf("Dispatch: %v", err)
					return
				}
			}
		}()
	}

	// Concurrent table writes on other events: a second manager subscribing
	// and tearing down while the fan-out runs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range perWorker {
			Subscribe(d, noteOf[LinkDeployed](rec, "link"))
			if err := Dispatch(d, LinkDeployed{}); err != nil {
				t.Errorf("Dispatch: %v", err)
				return
			}
			if err := Unsubscribe[LinkDeployed](d); err != nil {
				t.Errorf("Unsubscribe: %v", err)
				return
			}
		}
	}()

	wg.Wait()

	bars := 0
	for _, tag := range rec.seen() {
		if tag == "bar" {
			bars++
		}
	}
	if want := workers * perWorker; bars != want {
		t.Errorf("machine_deployed fired %d times, want %d", bars, want)
	}
}

// TestSubscribeFromInsideDispatchIsRaceFree exercises the one re-entrancy the
// lock discipline exists for: a subscriber that touches the table while the
// walk is in flight (see the `*_during_dispatch` oracle traces) must not
// deadlock, since the mutex is never held across a callback.
func TestSubscribeFromInsideDispatchIsRaceFree(t *testing.T) {
	d := New()
	rec := &recorder{}

	Subscribe(d, func(LinksDeployStarted) error {
		rec.note("outer")
		Subscribe(d, noteOf[LinksDeployStarted](rec, "inner"))
		if err := Unsubscribe[LinksDeployEnded](d); err != nil {
			return err
		}
		if _, err := Subscribers[LinksDeployStarted](d); err != nil {
			return err
		}
		return Dispatch(d, LinksDeployEnded{})
	})

	if err := Dispatch(d, LinksDeployStarted{Links: []*model.Link{{Name: "A"}}}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// The subscriber added mid-walk runs in this same dispatch — exactly what
	// appending to a list CPython is iterating does.
	if got, want := rec.seen(), []string{"outer", "inner"}; !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
}
