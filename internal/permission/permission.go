// Package permission implements the allow/deny/ask engine that gates every
// side-effecting tool call in Hygge.
//
// # Decision pipeline
//
// Each [Request] flows through a fixed sequence of layers, evaluated in this
// order with first-match-wins semantics:
//
//  1. Secrets denylist (hard-coded; matches → deny).  User config CANNOT
//     override this layer.
//  2. Persisted state allowances ([state.State.AllowedRules]).  These are
//     the "always-allow" decisions the user has previously made through a
//     prompt.
//  3. In-memory session-scope cache.  An allow with scope="session" applies
//     for the remainder of the session.
//  4. Default policy synthesised from [config.Config.Permission].
//
// If the matching layer's action is "ask", the engine publishes a
// [bus.PermissionAsked] event, subscribes to [bus.PermissionReplied], and
// blocks until a reply with the matching RequestID arrives or the context is
// cancelled.
//
// # Secrets denylist carve-out
//
// The denylist applies to the file.read and file.write categories ONLY.
// A shell command that incidentally reads .env is gated by the "shell"
// category, not "file.read", so the denylist does not constrain it.  This is
// a known v0.1 limitation: when MCP tools land we will revisit and may add a
// distinct "shell.exec-that-reads-secrets" detector.  For v0.1 there is no
// MCP tool that meaningfully exploits this carve-out.
//
// # Concurrency
//
// The Engine is safe for concurrent Asks from many goroutines.  Each Ask
// gets its own bus subscription so replies cannot cross-pollinate.  The
// session cache uses a sync.RWMutex.  Closing the engine cancels all
// pending Asks via the engine's internal context.
package permission

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/state"
)

// Category identifies the kind of action being gated.
type Category string

// The frozen v0.1 categories.
const (
	CategoryFileRead  Category = "file.read"
	CategoryFileWrite Category = "file.write"
	CategoryShell     Category = "shell"
	CategoryNetwork   Category = "network"
)

// Action is the decision outcome.
type Action string

// Action constants.
const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
	ActionAsk   Action = "ask"
)

// Scope describes how long an Allow/Deny applies.
type Scope string

// Scope constants.
const (
	ScopeOnce    Scope = "once"
	ScopeSession Scope = "session"
	ScopeAlways  Scope = "always"
)

// Request is everything the engine needs to evaluate a permission decision.
type Request struct {
	// SessionID is the session that triggered the request.  Used to scope
	// the in-memory session cache.
	SessionID string

	// Category is the permission category.
	Category Category

	// Target is the path or command being acted on.  For file.* this is an
	// absolute filesystem path; for shell it is the full command string;
	// for network it is a URL or host.
	Target string

	// ToolName is the name of the calling tool — surfaced to the UI prompt
	// so the user knows who is asking.
	ToolName string

	// Pwd is the session's working directory.  Used by the default policy
	// to evaluate "inside $PWD" rules.
	Pwd string

	// Command, DiffPath, and Reason are optional metadata threaded through
	// to the UI prompt via the bus event.  None of them participate in
	// rule matching.
	Command  string // for shell: same as Target but kept distinct so UI knows it's a command
	DiffPath string // for file.write: path being modified (same as Target)
	Reason   string // free-text rationale from the calling tool
}

// Decision is the outcome of [Engine.Ask].  Ask never returns ActionAsk —
// any "ask" rule is resolved by waiting on the bus.
type Decision struct {
	// Action is the resolved action: ActionAllow or ActionDeny.
	Action Action

	// Scope is the scope of the decision: once, session, or always.
	Scope Scope

	// Reason is a short, human-readable explanation populated for denies
	// and for allows that came from a non-default source (e.g. the secrets
	// denylist or a persisted state rule).
	Reason string
}

// EngineOptions configures a new Engine.
type EngineOptions struct {
	// Bus is the event bus used to publish PermissionAsked events and
	// receive PermissionReplied events.  Required.
	Bus *bus.Bus

	// Config carries the resolved permission scalars (default policy).
	// Optional; when nil the engine falls back to "ask everything except
	// deny network" defaults.
	Config *config.Config

	// State controls where persisted "always" approvals are loaded from
	// and saved to.  Pass-through to [state.Load] and [state.Save].
	State state.LoadOptions

	// Clock is an injectable time source used for the At fields of
	// published bus events and for AllowRule timestamps.  Defaults to
	// [time.Now].
	Clock func() time.Time
}

// SecretsDenylist is the hard-coded list of globs that block file.read and
// file.write access.  User configuration cannot override this list.
//
// These patterns match common credential files; new entries may be added in
// future versions but existing entries will never be removed.
var SecretsDenylist = []string{
	"**/.env",
	"**/.env.*",
	"**/*.pem",
	"**/*.key",
	"**/id_rsa*",
	"**/id_ed25519*",
	"**/id_ecdsa*",
	"**/.aws/credentials",
	"**/.netrc",
	"**/.git-credentials",
	"**/.ssh/id*",
	"**/1Password/**",
	"**/keychain*",
	"**/*.kdbx",
}

// Engine evaluates Requests.  Construct with [New]; the zero value is not
// usable.
type Engine struct {
	bus       *bus.Bus
	stateOpts state.LoadOptions
	clock     func() time.Time

	matcher *Matcher

	mu           sync.RWMutex // guards closed and sessionCache
	closed       bool
	sessionCache map[sessionCacheKey]Decision
}

type sessionCacheKey struct {
	SessionID string
	Category  Category
	Target    string
}

// New constructs an Engine.  An error is returned only if the rule set
// (secrets denylist + state allowances + config defaults) contains an
// invalid pattern; in practice this should never happen with the supplied
// inputs because the denylist is hard-coded and the synthesised defaults use
// "**".
func New(opts EngineOptions) (*Engine, error) {
	if opts.Bus == nil {
		return nil, fmt.Errorf("permission: New: Bus is required")
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}

	rules, err := buildRules(opts.Config, opts.State)
	if err != nil {
		return nil, err
	}
	matcher, err := NewMatcher(rules)
	if err != nil {
		return nil, err
	}

	return &Engine{
		bus:          opts.Bus,
		stateOpts:    opts.State,
		clock:        clock,
		matcher:      matcher,
		sessionCache: make(map[sessionCacheKey]Decision),
	}, nil
}

// buildRules assembles the full ordered rule set for the engine.  The order
// is: secrets denylist → persisted state allowances → default policy.  User
// TOML-declared "[[permission.rules]]" entries would go between state and
// defaults; this slot is reserved for a future config extension.
func buildRules(cfg *config.Config, stateOpts state.LoadOptions) ([]Rule, error) {
	var rules []Rule

	// 1. Secrets denylist (file.read + file.write).
	for _, pat := range SecretsDenylist {
		rules = append(rules,
			Rule{Category: CategoryFileRead, Pattern: pat, Action: ActionDeny, Source: "secrets-denylist"},
			Rule{Category: CategoryFileWrite, Pattern: pat, Action: ActionDeny, Source: "secrets-denylist"},
		)
	}

	// 2. Persisted state allowances.
	st, err := state.Load(stateOpts)
	if err != nil {
		return nil, fmt.Errorf("permission: load state: %w", err)
	}
	for _, r := range st.AllowedRules {
		rules = append(rules, Rule{
			Category: Category(r.Category),
			Pattern:  r.Pattern,
			Action:   ActionAllow,
			Source:   "state",
		})
	}

	// 3. (Reserved for config.Permission.Rules when the schema gains it.)

	// 4. Default policy.
	rules = append(rules, defaultRules(cfg)...)
	return rules, nil
}

// Close marks the engine as closed and clears the session cache.  All
// subsequent Ask calls return [ErrEngineClosed].  Close is idempotent and
// safe to call from any goroutine.
//
// Close does NOT close the underlying bus — that lifecycle is owned by the
// caller (typically the application's root context).
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	e.sessionCache = nil
}

// Ask evaluates req and returns a Decision.  The returned Action is always
// ActionAllow or ActionDeny — internal "ask" actions are resolved by
// publishing a [bus.PermissionAsked] event and blocking on the matching
// [bus.PermissionReplied].
//
// If ctx is cancelled before a reply arrives, Ask returns ctx.Err.  If the
// engine has been closed, Ask returns [ErrEngineClosed].  If the bus
// subscription is closed before a reply arrives, Ask returns [ErrBusClosed].
func (e *Engine) Ask(ctx context.Context, req Request) (Decision, error) {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return Decision{}, ErrEngineClosed
	}
	e.mu.RUnlock()

	action, rule := e.matcher.Match(req)

	switch action {
	case ActionAllow:
		return Decision{
			Action: ActionAllow,
			Scope:  ScopeOnce,
			Reason: reasonFromRule(rule),
		}, nil
	case ActionDeny:
		return Decision{
			Action: ActionDeny,
			Scope:  ScopeOnce,
			Reason: reasonFromRule(rule),
		}, nil
	case ActionAsk:
		// Check the session cache before bothering the user.
		if cached, ok := e.lookupSession(req); ok {
			return cached, nil
		}
		return e.askUser(ctx, req)
	default:
		return Decision{}, fmt.Errorf("permission: unknown action %q from matcher", action)
	}
}

// askUser publishes a PermissionAsked event and waits for the matching
// PermissionReplied.
func (e *Engine) askUser(ctx context.Context, req Request) (Decision, error) {
	requestID, err := newRequestID()
	if err != nil {
		return Decision{}, fmt.Errorf("permission: generate request id: %w", err)
	}

	// Generous buffer: many concurrent Asks may broadcast replies via the
	// same bus, and every subscriber receives every reply regardless of
	// whose RequestID it carries.  A tight buffer combined with the
	// drop-on-overflow bus policy can drop a matching reply if many
	// unrelated replies arrive first.  256 is well above any realistic
	// concurrent-Ask count for a single session.
	sub := bus.Subscribe[bus.PermissionReplied](e.bus, bus.SubscribeOptions{BufferSize: 256})
	defer sub.Unsubscribe()

	bus.Publish(e.bus, bus.PermissionAsked{
		RequestID: requestID,
		SessionID: req.SessionID,
		Category:  string(req.Category),
		Target:    req.Target,
		ToolName:  req.ToolName,
		At:        e.clock(),
	})

	for {
		select {
		case <-ctx.Done():
			return Decision{}, ctx.Err()
		case reply, ok := <-sub.C():
			if !ok {
				return Decision{}, ErrBusClosed
			}
			if reply.RequestID != requestID {
				continue
			}
			return e.handleReply(req, reply), nil
		}
	}
}

// handleReply converts a bus reply into a Decision, persists "always" allows
// to state, and updates the session cache for "session" allows.
func (e *Engine) handleReply(req Request, reply bus.PermissionReplied) Decision {
	decision := Decision{
		Action: Action(reply.Decision),
		Scope:  Scope(reply.Scope),
		Reason: "user reply",
	}

	if decision.Action == ActionAllow && decision.Scope == ScopeAlways {
		rule := state.AllowRule{
			Category:  string(req.Category),
			Pattern:   req.Target,
			CreatedAt: e.clock().UnixMilli(),
		}
		if err := state.AddAllowRule(rule, e.stateOpts); err != nil {
			slog.Warn("permission: persist always-allow rule failed",
				"category", req.Category,
				"target", req.Target,
				"err", err,
			)
		}
	}

	if decision.Scope == ScopeSession {
		e.storeSession(req, decision)
	}
	return decision
}

// lookupSession returns a cached Decision for the (session, category, target)
// triple if one exists.
func (e *Engine) lookupSession(req Request) (Decision, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed || e.sessionCache == nil {
		return Decision{}, false
	}
	d, ok := e.sessionCache[sessionCacheKey{
		SessionID: req.SessionID,
		Category:  req.Category,
		Target:    req.Target,
	}]
	return d, ok
}

// storeSession records a session-scoped decision in the in-memory cache.
func (e *Engine) storeSession(req Request, d Decision) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.sessionCache == nil {
		return
	}
	e.sessionCache[sessionCacheKey{
		SessionID: req.SessionID,
		Category:  req.Category,
		Target:    req.Target,
	}] = d
}

// reasonFromRule produces a Decision.Reason string from a matched rule.
// For nil rules it returns an empty string.
func reasonFromRule(r *Rule) string {
	if r == nil {
		return ""
	}
	if r.Source == "" {
		return ""
	}
	return "rule: " + r.Source
}

// newRequestID returns a 16-byte cryptographically random ID, hex-encoded.
func newRequestID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
