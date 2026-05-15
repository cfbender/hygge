package config

import (
	"fmt"
	"log/slog"
	"reflect"
)

// unsetSentinel is a string value that, when encountered during a merge,
// removes the corresponding key from the merged result.
//
// Use this in a higher-precedence source to clear a key that a lower-
// precedence source has set.  Example in a profile TOML:
//
//	[model]
//	api_key = "__hygge_unset__"   # clears api_key even if user config sets it
const unsetSentinel = "__hygge_unset__"

// deepMergeInto merges src into dst, modifying dst in-place.
// prov is updated to record src as a contributing source for every affected
// leaf key.  prefix is the dotted-path prefix accumulated through recursion.
//
// Merge semantics (documented per spec):
//
//   - Scalars: higher-precedence (src) overrides lower-precedence (dst).
//     A type mismatch (excluding nil) is an error.
//   - Maps: merged recursively by key.  Keys only in dst are kept.
//   - Arrays of scalars: replaced wholesale by src (last writer wins).
//     This is intentional and deliberate.
//   - Arrays of tables: if every element has a stable merge key, merged by
//     that key (higher-precedence wins per key; all keys from both layers are
//     kept). The default merge key is "id"; config-schema-specific arrays may
//     use another key, e.g. modes merge by "name".
//     If any element lacks the merge key, the array is replaced wholesale and
//     a slog.Warn is emitted.
//   - Unset sentinel ("__hygge_unset__"): removes the key from dst.
func deepMergeInto(dst, src map[string]any, prov Provenance, source Source) error {
	return deepMerge(dst, src, prov, source, "")
}

func deepMerge(dst, src map[string]any, prov Provenance, source Source, prefix string) error {
	for k, srcVal := range src {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}

		// Handle unset sentinel.
		if s, ok := srcVal.(string); ok && s == unsetSentinel {
			delete(dst, k)
			prov[key] = append(prov[key], source)
			continue
		}

		dstVal, exists := dst[k]

		if !exists {
			// New key from higher-precedence source — just set it.
			dst[k] = srcVal
			recordLeafProvenance(prov, srcVal, source, key)
			continue
		}

		// Both sides have a value.  Dispatch on type.
		switch sv := srcVal.(type) {
		case map[string]any:
			dv, ok := dstVal.(map[string]any)
			if !ok {
				// dst had a scalar; src has a map — type mismatch.
				return &MergeTypeError{
					Key:      key,
					LowFile:  sourceFileOf(prov, key),
					HighFile: source.File,
					LowType:  typeName(dstVal),
					HighType: typeName(srcVal),
				}
			}
			if err := deepMerge(dv, sv, prov, source, key); err != nil {
				return err
			}

		case []any:
			dv, ok := dstVal.([]any)
			if !ok {
				// dst is not a slice — replace.
				dst[k] = srcVal
				prov[key] = append(prov[key], source)
				continue
			}
			merged, err := mergeArrays(dv, sv, prov, source, key)
			if err != nil {
				return err
			}
			dst[k] = merged

		default:
			// Scalar merge: type-check then override.
			if !typesCompatible(dstVal, srcVal) {
				return &MergeTypeError{
					Key:      key,
					LowFile:  sourceFileOf(prov, key),
					HighFile: source.File,
					LowType:  typeName(dstVal),
					HighType: typeName(srcVal),
				}
			}
			dst[k] = srcVal
			prov[key] = append(prov[key], source)
		}
	}
	return nil
}

// mergeArrays handles array merging:
//   - If every element in both slices is a map[string]any with the array's
//     merge key, the arrays are merged by that key.
//   - Otherwise, src replaces dst wholesale and a warning is logged.
func mergeArrays(dst, src []any, prov Provenance, source Source, key string) ([]any, error) {
	mergeKey := arrayMergeKey(key)
	if allHaveMergeKey(dst, mergeKey) && allHaveMergeKey(src, mergeKey) {
		return mergeByKey(dst, src, prov, source, key, mergeKey)
	}

	// Wholesale replacement.
	if len(dst) > 0 {
		slog.Warn("config: array replaced wholesale (elements lack merge key field)",
			"key", key, "merge_key", mergeKey, "source", source.File)
	}
	prov[key] = append(prov[key], source)
	return src, nil
}

func arrayMergeKey(key string) string {
	if key == "modes" {
		return "name"
	}
	return "id"
}

// allHaveMergeKey returns true if every element in slc is a map[string]any that
// contains mergeKey.
func allHaveMergeKey(slc []any, mergeKey string) bool {
	if len(slc) == 0 {
		return true // vacuously — empty arrays always "have" the merge key
	}
	for _, v := range slc {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if _, has := m[mergeKey]; !has {
			return false
		}
	}
	return true
}

// mergeByKey merges two arrays of tables using mergeKey as a stable key.
// Elements from dst that are not overridden by src are kept. Elements in src
// not in dst are appended.
func mergeByKey(dst, src []any, prov Provenance, source Source, key, mergeKey string) ([]any, error) {
	byKey := make(map[any]map[string]any)
	order := make([]any, 0, len(dst))

	for _, elem := range dst {
		m := elem.(map[string]any)
		mergeValue := fmt.Sprint(m[mergeKey])
		byKey[mergeValue] = m
		order = append(order, mergeValue)
	}

	for _, elem := range src {
		m := elem.(map[string]any)
		mergeValue := fmt.Sprint(m[mergeKey])
		existing, exists := byKey[mergeValue]
		if exists {
			subKey := key + "." + mergeValue
			subProv := make(Provenance)
			if err := deepMerge(existing, m, subProv, source, subKey); err != nil {
				return nil, err
			}
			for pk, ps := range subProv {
				prov[pk] = append(prov[pk], ps...)
			}
		} else {
			byKey[mergeValue] = m
			order = append(order, mergeValue)
			recordLeafProvenance(prov, m, source, key+"."+mergeValue)
		}
	}

	result := make([]any, 0, len(order))
	seen := make(map[any]bool)
	for _, mergeValue := range order {
		if seen[mergeValue] {
			continue
		}
		seen[mergeValue] = true
		result = append(result, byKey[fmt.Sprint(mergeValue)])
	}
	return result, nil
}

// typesCompatible returns true when low and high can be merged (same base
// type, or one of them is nil).
func typesCompatible(low, high any) bool {
	if low == nil || high == nil {
		return true
	}
	// Allow numeric cross-type (int64 vs float64 from different sources).
	if isNumeric(low) && isNumeric(high) {
		return true
	}
	return reflect.TypeOf(low) == reflect.TypeOf(high)
}

func isNumeric(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	}
	return false
}

// typeName returns a human-readable type name for error messages.
func typeName(v any) string {
	if v == nil {
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}

// sourceFileOf returns the file of the last Source recorded for key, or
// "<unknown>" if none.
func sourceFileOf(prov Provenance, key string) string {
	if sources, ok := prov[key]; ok && len(sources) > 0 {
		return sources[len(sources)-1].File
	}
	return "<unknown>"
}

// recordLeafProvenance records source for every leaf key in v under prefix.
func recordLeafProvenance(prov Provenance, v any, source Source, prefix string) {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			subKey := k
			if prefix != "" {
				subKey = prefix + "." + k
			}
			recordLeafProvenance(prov, child, source, subKey)
		}
	default:
		prov[prefix] = append(prov[prefix], source)
	}
}
