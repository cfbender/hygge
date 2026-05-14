package session

import (
	"errors"
	"fmt"
	"sync"

	"github.com/oklog/ulid/v2"
)

// idEntropy guards a single monotonic entropy source for the whole process.
// ULIDs generated through the helpers below are strictly increasing within
// the same millisecond and globally unique.
var (
	idMu      sync.Mutex
	idEntropy = ulid.Monotonic(ulid.DefaultEntropy(), 0)
)

// errIDGen is the underlying ulid error wrapper used by tests.
var errIDGen = errors.New("session: generate ulid")

// newULID is the workhorse that all NewXxxID helpers call.
func newULID() string {
	idMu.Lock()
	defer idMu.Unlock()
	id, err := ulid.New(ulid.Now(), idEntropy)
	if err != nil {
		// ulid.MonotonicEntropy returns ErrMonotonicOverflow if more than
		// 2^80 IDs are generated in the same millisecond — astronomically
		// unlikely.  Panic rather than smuggle a sentinel through the
		// helper's string return.
		panic(fmt.Errorf("%w: %w", errIDGen, err))
	}
	return id.String()
}

// NewSessionID returns a 26-character canonical ULID string.
func NewSessionID() string { return newULID() }

// NewMessageID returns a 26-character canonical ULID string.
func NewMessageID() string { return newULID() }

// NewMarkerID returns a 26-character canonical ULID string.
func NewMarkerID() string { return newULID() }
