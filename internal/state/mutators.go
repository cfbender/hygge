package state

import "fmt"

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
