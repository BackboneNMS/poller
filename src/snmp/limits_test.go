package snmp

import (
	"slices"
	"testing"
)

func TestSplitEvenly(t *testing.T) {
	for _, test := range []struct {
		count    int
		size     int
		expected []int
	}{
		{0, 10, []int{}},
		{7, 10, []int{7}},
		{10, 10, []int{10}},
		{11, 10, []int{6, 5}},
		{14, 10, []int{7, 7}},
		{5, 1, []int{1, 1, 1, 1, 1}},
	} {
		items := make([]int, test.count)
		sizes := make([]int, 0)

		for _, group := range splitEvenly(items, test.size) {
			sizes = append(sizes, len(group))
		}

		if !slices.Equal(sizes, test.expected) {
			t.Errorf("%d items in groups of %d: expected %v, got %v", test.count, test.size, test.expected, sizes)
		}
	}
}
