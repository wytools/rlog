package rotation

import "log/slog"

// countEmptyGroups returns the number of empty group values in its argument.
func countEmptyGroups(as []slog.Attr) int {
	n := 0
	for _, a := range as {
		if isEmptyGroup(&a.Value) {
			n++
		}
	}
	return n
}

// isEmptyGroup reports whether v is a group that has no attributes.
func isEmptyGroup(v *slog.Value) bool {
	if v.Kind() != slog.KindGroup {
		return false
	}
	// We do not need to recursively examine the group's Attrs for emptiness,
	// because GroupValue removed them when the group was constructed, and
	// groups are immutable.
	return len(v.Group()) == 0
}
