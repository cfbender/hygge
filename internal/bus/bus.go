// Package bus provides an in-process typed pubsub system for Hygge.
//
// # Design
//
// The bus is a single-process, in-memory event multiplexer. There is no
// network delivery, no cross-binary routing, and no persistence — events
// vanish if the process restarts. Persistence is a concern for the session
// layer, not the bus.
//
// # Typed dispatch
//
// Events are Go structs; the bus uses reflection to key subscriptions by their
// concrete type. Subscribing to SessionStart never receives a SessionEnd event
// and vice versa. The generic Subscribe[T] and Publish[T] functions eliminate
// any-casts at every call site.
//
// # Drop-on-overflow
//
// Each subscription holds a bounded channel. If a subscriber is too slow to
// drain its channel before the next event arrives, the event is dropped for
// that subscriber and its Dropped counter is incremented atomically. This is
// the correct trade-off for a UI bus where stale events are useless — a fast
// publisher must never stall waiting for a slow subscriber.
//
// # Concurrency
//
// Publish holds the bus mutex only long enough to snapshot the subscriber list,
// then releases it before performing channel sends. Each entry carries its own
// read-write lock: concurrent senders take RLock, the closer (Unsubscribe or
// Bus.Close) takes an exclusive Lock. This ensures a channel close can never
// race with an in-flight send. Unsubscribe finds entries by a unique uint64 ID
// so closure-identity comparisons are never needed. Close cancels all
// subscriptions atomically and makes further publishes no-ops.
package bus

import (
	"reflect"
	"sync"
	"sync/atomic"
)

// DefaultBufferSize is the per-subscription channel buffer used when
// SubscribeOptions.BufferSize is zero.
const DefaultBufferSize = 64

// Bus is an in-process typed pubsub. The zero value is not usable; use New().
type Bus struct {
	mu     sync.Mutex
	subs   map[reflect.Type][]*subscriberEntry
	nextID atomic.Uint64
	closed bool
}

// subscriberEntry is the internal state for a single active subscription.
//
// mu serialises send against close.  Senders take RLock; the closer takes an
// exclusive Lock.  The closed flag (checked under RLock) prevents a send to an
// already-closed channel.
//
// The typed channel is never stored directly here — it lives only inside the
// send and close closures so that the non-generic entry struct does not need a
// type parameter.
type subscriberEntry struct {
	id       uint64
	mu       sync.RWMutex // guards closed, send, and closeFn
	isClosed bool
	send     func(v any) // non-blocking typed send; increments dropped on overflow
	closeFn  func()      // closes the subscriber-visible channel
	dropped  atomic.Uint64
}

// New creates a Bus.
func New() *Bus {
	return &Bus{
		subs: make(map[reflect.Type][]*subscriberEntry),
	}
}

// Close cancels all subscriptions and prevents further publishes.
// Publishing to a closed bus is a no-op (does not panic).
// Subscribing to a closed bus returns a closed channel and an unregistered Subscription.
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	var all []*subscriberEntry
	for _, entries := range b.subs {
		all = append(all, entries...)
	}
	b.subs = make(map[reflect.Type][]*subscriberEntry)
	b.mu.Unlock()

	// Close each entry outside the bus mutex to avoid lock ordering issues.
	for _, e := range all {
		e.mu.Lock()
		if !e.isClosed {
			e.isClosed = true
			e.closeFn()
		}
		e.mu.Unlock()
	}
}

// SubscribeOptions controls a single Subscribe call.
type SubscribeOptions struct {
	// BufferSize is the per-subscription channel buffer. 0 means use DefaultBufferSize.
	BufferSize int
}

// Subscription is the handle returned from Subscribe. Holding it lets the
// caller unsubscribe and read drop stats.
type Subscription[T any] struct {
	entry *subscriberEntry
	ch    <-chan T
	bus   *Bus
	typ   reflect.Type
	once  sync.Once // guards Unsubscribe so it is idempotent
}

// C returns the channel events are delivered on. Read-only.
func (s *Subscription[T]) C() <-chan T {
	return s.ch
}

// Unsubscribe stops delivery and closes the channel. Safe to call multiple times.
func (s *Subscription[T]) Unsubscribe() {
	s.once.Do(func() {
		s.bus.removeEntry(s.typ, s.entry.id)
	})
}

// Dropped returns the cumulative count of events dropped because the
// subscriber's channel buffer was full. Atomic; safe to call from any goroutine.
func (s *Subscription[T]) Dropped() uint64 {
	return s.entry.dropped.Load()
}

// removeEntry removes the entry with the given id from the map slice for typ
// and closes it.
func (b *Bus) removeEntry(typ reflect.Type, id uint64) {
	b.mu.Lock()
	entries := b.subs[typ]
	var target *subscriberEntry
	for i, e := range entries {
		if e.id == id {
			last := len(entries) - 1
			entries[i] = entries[last]
			entries[last] = nil
			b.subs[typ] = entries[:last]
			target = e
			break
		}
	}
	b.mu.Unlock()

	if target == nil {
		return // already removed (e.g. bus was closed first)
	}

	target.mu.Lock()
	if !target.isClosed {
		target.isClosed = true
		target.closeFn()
	}
	target.mu.Unlock()
}

// Subscribe registers a typed subscriber. Returns a Subscription whose channel
// receives every event of type T published after Subscribe returns. Events
// published before Subscribe are NOT replayed.
//
// Subscribing to a closed bus returns a Subscription with a closed channel.
func Subscribe[T any](b *Bus, opts SubscribeOptions) *Subscription[T] {
	bufSize := opts.BufferSize
	if bufSize <= 0 {
		bufSize = DefaultBufferSize
	}

	typ := reflect.TypeFor[T]()
	ch := make(chan T, bufSize)

	entry := &subscriberEntry{
		id: b.nextID.Add(1),
	}

	// The send and close closures capture ch and entry.  They are the ONLY
	// places that interact with ch; this ensures the channel is only ever
	// closed under entry.mu.Lock() and only ever sent to under entry.mu.RLock().
	entry.send = func(v any) {
		// Called under entry.mu.RLock() by Publish.
		select {
		case ch <- v.(T): //nolint:forcetypeassert // guaranteed: Publish[T] boxes only T values
		default:
			entry.dropped.Add(1)
		}
	}
	entry.closeFn = func() {
		// Called under entry.mu.Lock() by removeEntry or Bus.Close.
		close(ch)
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return &Subscription[T]{entry: entry, ch: ch, bus: b, typ: typ}
	}
	b.subs[typ] = append(b.subs[typ], entry)
	b.mu.Unlock()

	return &Subscription[T]{entry: entry, ch: ch, bus: b, typ: typ}
}

// Publish delivers ev to every current subscriber of type T. Returns the
// number of subscribers the event was successfully enqueued to (drops not
// included). Non-blocking: if a subscriber's channel is full, the event is
// dropped for that subscriber and its Dropped counter is incremented.
func Publish[T any](b *Bus, ev T) int {
	typ := reflect.TypeFor[T]()

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0
	}
	src := b.subs[typ]
	if len(src) == 0 {
		b.mu.Unlock()
		return 0
	}
	// Snapshot the slice so we release the bus mutex before sending.
	snapshot := make([]*subscriberEntry, len(src))
	copy(snapshot, src)
	b.mu.Unlock()

	delivered := 0
	for _, e := range snapshot {
		e.mu.RLock()
		if !e.isClosed {
			prevDrops := e.dropped.Load()
			e.send(ev)
			if e.dropped.Load() == prevDrops {
				delivered++
			}
		}
		e.mu.RUnlock()
	}
	return delivered
}

// subscriberCount returns the number of active subscribers for type T.
// This is an internal helper used by tests in the same package.
func subscriberCount[T any](b *Bus) int {
	typ := reflect.TypeFor[T]()
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[typ])
}
