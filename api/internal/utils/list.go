package utils

import (
	"cmp"
	"slices"
)

func DeduplicateInPlace[T cmp.Ordered](slice []T) []T {
	if len(slice) < 2 {
		return slice
	}

	// Sort elements so duplicates sit next to each other
	slices.Sort(slice)

	// Compact removes consecutive duplicate elements
	return slices.Compact(slice)
}
