package session

import (
	"context"
	"fmt"
	"log/slog"
)

// maxParentChainDepth caps the number of hops ResolveRootSessionID will
// follow to prevent unbounded work if a cycle were ever introduced by a
// store bug.  32 matches the limit used by PropagateTotals.
const maxParentChainDepth = 32

// ResolveRootSessionID walks the ParentID chain starting at sessionID and
// returns the ID of the root ancestor — the session that has no parent.
// When sessionID is already a root, sessionID itself is returned.
//
// The walk is capped at maxParentChainDepth hops and is guarded against
// cycles: if a session ID is visited twice the walk stops and the current
// position is treated as the root.
//
// A non-nil error is returned only when a store call fails.  Callers that
// want best-effort behaviour should log the error and fall back to using
// sessionID as-is.
func ResolveRootSessionID(ctx context.Context, store Store, sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("session: ResolveRootSessionID: sessionID is required")
	}

	visited := make(map[string]struct{}, maxParentChainDepth)
	currentID := sessionID

	for range maxParentChainDepth {
		if _, seen := visited[currentID]; seen {
			// Cycle detected; treat current position as the root.
			slog.Warn("session: ResolveRootSessionID: cycle detected; using current id as root",
				"session_id", currentID)
			break
		}
		visited[currentID] = struct{}{}

		sess, err := store.GetSession(ctx, currentID)
		if err != nil {
			return currentID, fmt.Errorf("session: ResolveRootSessionID: get session %q: %w", currentID, err)
		}

		if sess.ParentID == "" {
			// currentID is the root.
			break
		}
		currentID = sess.ParentID
	}

	return currentID, nil
}
