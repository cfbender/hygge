package state

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// SetActiveProfile sets the active profile name and persists the change.
func SetActiveProfile(name string, opts LoadOptions) error {
	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("state: SetActiveProfile: %w", err)
	}
	s.ActiveProfile = name
	return Save(s, opts)
}

// SetLastUsedModel records the most recently used provider/model and persists
// the change.
func SetLastUsedModel(ref ModelRef, opts LoadOptions) error {
	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("state: SetLastUsedModel: %w", err)
	}
	s.LastUsedModel = &ref
	return Save(s, opts)
}

// AddRecentSession prepends id to [State.RecentSessions].  If id is already
// present in the list it is removed from its prior position before being
// prepended (most-recent reference wins).  The list is capped at
// [MaxRecentSessions]; entries beyond the cap are dropped from the tail.
func AddRecentSession(id string, opts LoadOptions) error {
	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("state: AddRecentSession: %w", err)
	}

	// Remove duplicate occurrences of id.
	filtered := s.RecentSessions[:0:0] // zero-length, avoids aliasing the original slice
	for _, existing := range s.RecentSessions {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}

	// Prepend and cap.
	capN := min(len(filtered)+1, MaxRecentSessions)
	updated := make([]string, 0, capN)
	updated = append(updated, id)
	for _, v := range filtered {
		if len(updated) >= MaxRecentSessions {
			break
		}
		updated = append(updated, v)
	}
	s.RecentSessions = updated

	return Save(s, opts)
}

// TrustConfig records that the file at absPath has been trusted at the given
// sha256hex digest.  On subsequent loads, callers should compare the stored
// digest to the live file's digest; a mismatch means trust has expired.
func TrustConfig(absPath string, sha256hex string, opts LoadOptions) error {
	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("state: TrustConfig: %w", err)
	}
	if s.TrustedConfigs == nil {
		s.TrustedConfigs = make(map[string]string)
	}
	s.TrustedConfigs[absPath] = sha256hex
	return Save(s, opts)
}

// IsConfigTrusted returns true iff a sha256 digest has been recorded for
// absPath and it matches expectedSha256.  Missing entries and digest
// mismatches both return false.
func IsConfigTrusted(absPath string, expectedSha256 string, opts LoadOptions) (bool, error) {
	s, err := Load(opts)
	if err != nil {
		return false, fmt.Errorf("state: IsConfigTrusted: %w", err)
	}
	stored, ok := s.TrustedConfigs[absPath]
	if !ok {
		return false, nil
	}
	return stored == expectedSha256, nil
}

// ToggleFavoriteModel adds ref to [State.FavoriteModels] if it is not already
// present, or removes it if it is.  The list preserves insertion order; new
// favorites are appended.  ref should be in "provider/model" form.
func ToggleFavoriteModel(ref string, opts LoadOptions) error {
	if strings.Count(ref, "/") != 1 {
		return fmt.Errorf("state: ToggleFavoriteModel: %w", errors.New("favorite model ref must be in provider/model form"))
	}
	parts := strings.SplitN(ref, "/", 2)
	if parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("state: ToggleFavoriteModel: %w", errors.New("favorite model ref must include non-empty provider and model"))
	}

	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("state: ToggleFavoriteModel: %w", err)
	}
	found := false
	filtered := s.FavoriteModels[:0:0]
	for _, v := range s.FavoriteModels {
		if v == ref {
			found = true
			continue // drop it (toggle off)
		}
		filtered = append(filtered, v)
	}
	if !found {
		filtered = append(filtered, ref) // toggle on
	}
	s.FavoriteModels = filtered
	return Save(s, opts)
}

// AddAllowRule appends rule to [State.AllowedRules] and persists.  If a rule
// with the same Category and Pattern already exists, it is left in place and
// the timestamp is not refreshed (idempotent).  The CreatedAt field is set
// from time.Now if zero.
func AddAllowRule(rule AllowRule, opts LoadOptions) error {
	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("state: AddAllowRule: %w", err)
	}
	for _, existing := range s.AllowedRules {
		if existing.Category == rule.Category && existing.Pattern == rule.Pattern {
			return nil // already present; no-op
		}
	}
	if rule.CreatedAt == 0 {
		rule.CreatedAt = time.Now().UnixMilli()
	}
	s.AllowedRules = append(s.AllowedRules, rule)
	return Save(s, opts)
}

// RemoveAllowRule removes any rules in [State.AllowedRules] whose Category and
// Pattern match the supplied values.  Idempotent: removing a missing rule
// returns nil.
func RemoveAllowRule(category, pattern string, opts LoadOptions) error {
	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("state: RemoveAllowRule: %w", err)
	}
	if len(s.AllowedRules) == 0 {
		return nil
	}
	filtered := s.AllowedRules[:0:0]
	for _, r := range s.AllowedRules {
		if r.Category == category && r.Pattern == pattern {
			continue
		}
		filtered = append(filtered, r)
	}
	if len(filtered) == len(s.AllowedRules) {
		return nil // nothing changed
	}
	s.AllowedRules = filtered
	return Save(s, opts)
}
