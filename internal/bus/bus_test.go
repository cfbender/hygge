package bus

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

// mustReceive drains one value from ch with a short timeout; fails the test if
// nothing arrives.
func mustReceive[T any](t *testing.T, sub *Subscription[T], timeout time.Duration) T {
	t.Helper()
	select {
	case v, ok := <-sub.C():
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		return v
	case <-time.After(timeout):
		t.Fatal("timed out waiting for event")
		var zero T
		return zero
	}
}

// mustNotReceive asserts nothing arrives on ch within the timeout.
func mustNotReceive[T any](t *testing.T, sub *Subscription[T], timeout time.Duration) {
	t.Helper()
	select {
	case v, ok := <-sub.C():
		if ok {
			t.Fatalf("unexpected event received: %+v", v)
		}
		// Channel closed is acceptable in "after Unsubscribe" scenarios.
	case <-time.After(timeout):
		// OK — nothing arrived.
	}
}

// drain reads all immediately available values from sub.C() without blocking.
func drain[T any](sub *Subscription[T]) []T {
	var out []T
	for {
		select {
		case v, ok := <-sub.C():
			if !ok {
				return out
			}
			out = append(out, v)
		default:
			return out
		}
	}
}

// ---- test 1: single subscriber, single publish ------------------------------

func TestSingleSubscriberSinglePublish(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	sub := Subscribe[SessionStart](b, SubscribeOptions{})
	want := SessionStart{SessionID: "s1", Resumed: false, At: time.Now()}

	n := Publish(b, want)
	if n != 1 {
		t.Errorf("Publish returned %d, want 1", n)
	}

	got := mustReceive(t, sub, time.Second)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("received %+v, want %+v", got, want)
	}
}

// ---- test 2: multiple subscribers of same type ------------------------------

func TestMultipleSubscribersSameType(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	const n = 5
	subs := make([]*Subscription[SessionStart], n)
	for i := range n {
		subs[i] = Subscribe[SessionStart](b, SubscribeOptions{})
	}

	want := SessionStart{SessionID: "multi", At: time.Now()}
	delivered := Publish(b, want)
	if delivered != n {
		t.Errorf("Publish delivered %d, want %d", delivered, n)
	}

	for i, sub := range subs {
		got := mustReceive(t, sub, time.Second)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("sub[%d]: received %+v, want %+v", i, got, want)
		}
	}
}

// ---- test 3: type isolation -------------------------------------------------

func TestTypeIsolation(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	startSub := Subscribe[SessionStart](b, SubscribeOptions{})
	endSub := Subscribe[SessionEnd](b, SubscribeOptions{})

	// Publish only SessionStart.
	Publish(b, SessionStart{SessionID: "iso"})

	// startSub must receive.
	mustReceive(t, startSub, time.Second)

	// endSub must NOT receive anything.
	mustNotReceive(t, endSub, 50*time.Millisecond)
}

// ---- test 4: slow subscriber drop -------------------------------------------

func TestSlowSubscriberDrop(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	sub := Subscribe[SessionStart](b, SubscribeOptions{BufferSize: 1})

	const total = 10
	for i := range total {
		Publish(b, SessionStart{SessionID: string(rune('a' + i))})
	}

	// Give any in-flight goroutines a moment (none here, but be safe).
	time.Sleep(10 * time.Millisecond)

	drops := sub.Dropped()
	buffered := len(sub.C())

	if drops+uint64(buffered) != total {
		t.Errorf("drops(%d) + buffered(%d) = %d, want %d", drops, buffered, drops+uint64(buffered), total)
	}
	if drops != total-1 {
		t.Errorf("Dropped() = %d, want %d", drops, total-1)
	}
	if buffered != 1 {
		t.Errorf("buffered = %d, want 1", buffered)
	}
}

// ---- test 5: fast vs slow concurrent subscribers ----------------------------

func TestFastVsSlowConcurrentSubscribers(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	const events = 100
	fastSub := Subscribe[CostUpdated](b, SubscribeOptions{BufferSize: events + 10})
	slowSub := Subscribe[CostUpdated](b, SubscribeOptions{BufferSize: 1})

	start := time.Now()
	for i := range events {
		Publish(b, CostUpdated{SessionID: "bench", InputTokens: int64(i)})
	}
	elapsed := time.Since(start)

	// Publish loop must complete well within 1 second even if slowSub is full.
	if elapsed > 500*time.Millisecond {
		t.Errorf("Publish loop took %v — slow subscriber appears to be blocking fast path", elapsed)
	}

	// Fast subscriber must receive all events.
	time.Sleep(20 * time.Millisecond) // let goroutines settle
	fastReceived := len(fastSub.C())
	if fastReceived != events {
		t.Errorf("fast subscriber buffered %d events, want %d", fastReceived, events)
	}
	if fastSub.Dropped() != 0 {
		t.Errorf("fast subscriber dropped %d events, want 0", fastSub.Dropped())
	}

	// Slow subscriber must show drops.
	if slowSub.Dropped() == 0 {
		t.Error("slow subscriber dropped 0 events, expected > 0")
	}
}

// ---- test 6: unsubscribe stops delivery -------------------------------------

func TestUnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	sub := Subscribe[SessionEnd](b, SubscribeOptions{BufferSize: 4})

	// First publish — should be delivered.
	Publish(b, SessionEnd{SessionID: "before"})
	mustReceive(t, sub, time.Second)

	sub.Unsubscribe()

	// Second publish — should NOT be delivered.
	Publish(b, SessionEnd{SessionID: "after"})

	// Channel should be closed (Unsubscribe closes it) and empty.
	select {
	case _, ok := <-sub.C():
		if ok {
			t.Fatal("received event after Unsubscribe")
		}
		// Channel closed — correct.
	case <-time.After(50 * time.Millisecond):
		t.Fatal("channel still open after Unsubscribe; expected it to be closed")
	}
}

// ---- test 7: unsubscribe is idempotent --------------------------------------

func TestUnsubscribeIdempotent(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	sub := Subscribe[SessionEnd](b, SubscribeOptions{})

	// Must not panic.
	sub.Unsubscribe()
	sub.Unsubscribe()
	sub.Unsubscribe()
}

// ---- test 8: close kills everything -----------------------------------------

func TestCloseKillsEverything(t *testing.T) {
	t.Parallel()
	b := New()

	sub1 := Subscribe[SessionStart](b, SubscribeOptions{})
	sub2 := Subscribe[MessageAppended](b, SubscribeOptions{})

	b.Close()

	// Both channels must be closed (range must exit immediately).
	done := make(chan struct{}, 2)
	go func() {
		//nolint:revive // intentional: drain until closed
		for range sub1.C() {
		}
		done <- struct{}{}
	}()
	go func() {
		//nolint:revive // intentional: drain until closed
		for range sub2.C() {
		}
		done <- struct{}{}
	}()

	timeout := time.After(time.Second)
	for range 2 {
		select {
		case <-done:
		case <-timeout:
			t.Fatal("channel did not close after Bus.Close()")
		}
	}

	// Publish after close must return 0.
	if n := Publish(b, SessionStart{SessionID: "ghost"}); n != 0 {
		t.Errorf("Publish after Close returned %d, want 0", n)
	}
}

// ---- test 9: generic correctness (round-trip) --------------------------------

func TestGenericRoundTrip(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	want := SessionStart{
		SessionID: "round-trip",
		Resumed:   true,
		At:        time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC),
	}

	sub := Subscribe[SessionStart](b, SubscribeOptions{})
	Publish(b, want)

	got := mustReceive(t, sub, time.Second)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n  got  %+v\n  want %+v", got, want)
	}
}

// ---- test 10: no leak on unsubscribe ----------------------------------------

func TestNoLeakOnUnsubscribe(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	for range 1000 {
		sub := Subscribe[SessionStart](b, SubscribeOptions{})
		sub.Unsubscribe()
	}

	count := subscriberCount[SessionStart](b)
	if count != 0 {
		t.Errorf("subscriber count after 1000 subscribe/unsubscribe cycles = %d, want 0", count)
	}
}

// ---- test 11: concurrency stress --------------------------------------------

func TestConcurrencyStress(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	const (
		publishGoroutines   = 8
		subscribeGoroutines = 8
		duration            = time.Second
	)

	var wg sync.WaitGroup
	var panics atomic.Int64

	// Publishers: each goroutine publishes a different event type at high rate.
	eventTypes := []func(){
		func() { Publish(b, SessionStart{SessionID: "s"}) },
		func() { Publish(b, SessionEnd{SessionID: "s"}) },
		func() { Publish(b, MessageAppended{SessionID: "s", Role: "user"}) },
		func() { Publish(b, ToolCallRequested{ToolName: "read"}) },
		func() { Publish(b, ToolCallCompleted{ToolName: "read"}) },
		func() { Publish(b, PermissionAsked{Category: "shell"}) },
		func() { Publish(b, CostUpdated{DollarsTotal: 0.01}) },
		func() { Publish(b, ContextUsageUpdated{PctUsed: 0.5}) },
	}

	deadline := time.Now().Add(duration)

	for i := range publishGoroutines {
		wg.Add(1)
		publish := eventTypes[i%len(eventTypes)]
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panics.Add(1)
				}
			}()
			for time.Now().Before(deadline) {
				publish()
			}
		}()
	}

	// Subscriber churn goroutines.
	for range subscribeGoroutines {
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					panics.Add(1)
				}
			}()
			for time.Now().Before(deadline) {
				sub := Subscribe[SessionStart](b, SubscribeOptions{BufferSize: 4})
				// Briefly drain.
				select {
				case <-sub.C():
				default:
				}
				sub.Unsubscribe()
			}
		})
	}

	wg.Wait()

	if p := panics.Load(); p != 0 {
		t.Errorf("stress test caused %d panic(s)", p)
	}
}

// ---- test 12: close is idempotent -------------------------------------------

func TestCloseIdempotent(t *testing.T) {
	t.Parallel()
	b := New()
	b.Close()
	b.Close() // must not panic
}

// ---- test 13: subscribe to closed bus returns closed channel ----------------

func TestSubscribeToClosedBus(t *testing.T) {
	t.Parallel()
	b := New()
	b.Close()

	sub := Subscribe[SessionStart](b, SubscribeOptions{})
	select {
	case _, ok := <-sub.C():
		if ok {
			t.Fatal("expected closed channel from Subscribe on closed bus")
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("channel not closed for Subscribe on closed bus")
	}
}

// ---- test 14: all v0.1 event types compile and publish ----------------------

func TestAllEventTypesCompile(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	now := time.Now()

	// Subscribe to each type.
	ss := Subscribe[SessionStart](b, SubscribeOptions{})
	se := Subscribe[SessionEnd](b, SubscribeOptions{})
	ma := Subscribe[MessageAppended](b, SubscribeOptions{})
	tcr := Subscribe[ToolCallRequested](b, SubscribeOptions{})
	tcc := Subscribe[ToolCallCompleted](b, SubscribeOptions{})
	pa := Subscribe[PermissionAsked](b, SubscribeOptions{})
	pr := Subscribe[PermissionReplied](b, SubscribeOptions{})
	cu := Subscribe[CostUpdated](b, SubscribeOptions{})
	ctx := Subscribe[ContextUsageUpdated](b, SubscribeOptions{})

	// Publish one of each.
	Publish(b, SessionStart{SessionID: "1", At: now})
	Publish(b, SessionEnd{SessionID: "1", At: now})
	Publish(b, MessageAppended{SessionID: "1", MessageID: "m1", Role: "user", At: now})
	Publish(b, ToolCallRequested{SessionID: "1", ToolName: "read", Args: []byte(`{}`), At: now})
	Publish(b, ToolCallCompleted{SessionID: "1", ToolName: "read", Result: []byte(`{}`), At: now})
	Publish(b, PermissionAsked{RequestID: "r1", Category: "shell", At: now})
	Publish(b, PermissionReplied{RequestID: "r1", Decision: "allow", Scope: "once", At: now})
	Publish(b, CostUpdated{SessionID: "1", DollarsTotal: 0.01, At: now})
	Publish(b, ContextUsageUpdated{SessionID: "1", PctUsed: 0.5, At: now})

	// Each subscriber must receive one event.
	mustReceive(t, ss, time.Second)
	mustReceive(t, se, time.Second)
	mustReceive(t, ma, time.Second)
	mustReceive(t, tcr, time.Second)
	mustReceive(t, tcc, time.Second)
	mustReceive(t, pa, time.Second)
	mustReceive(t, pr, time.Second)
	mustReceive(t, cu, time.Second)
	mustReceive(t, ctx, time.Second)
}

// ---- test 15: Dropped counter does not race ---------------------------------

func TestDroppedCounterNoRace(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	sub := Subscribe[SessionStart](b, SubscribeOptions{BufferSize: 1})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 100 {
				Publish(b, SessionStart{SessionID: "race"})
			}
		})
	}

	wg.Wait()

	// Reading Dropped() from another goroutine while publishing finishes.
	_ = sub.Dropped()
}

// ---- drain helper sanity (internal) -----------------------------------------

func TestDrainHelper(t *testing.T) {
	t.Parallel()
	b := New()
	defer b.Close()

	sub := Subscribe[SessionEnd](b, SubscribeOptions{BufferSize: 8})
	for i := range 5 {
		Publish(b, SessionEnd{SessionID: string(rune('a' + i))})
	}
	time.Sleep(10 * time.Millisecond) // ensure all are buffered
	got := drain(sub)
	if len(got) != 5 {
		t.Errorf("drain returned %d items, want 5", len(got))
	}
}
