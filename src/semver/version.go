package semver

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// Version is a release version in the MAJOR.MINOR.PATCH form of semantic versioning. Pre-release and build suffixes
// are not accepted, so pre-release tags are never taken for a release.
type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
}

// Parse reads a version such as 1.2.3, with or without the leading v that release tags carry (v1.2.3)
func Parse(value string) (Version, error) {
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")

	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%q is not a MAJOR.MINOR.PATCH version", value)
	}

	var numbers [3]uint64

	for i, part := range parts {
		number, err := strconv.ParseUint(part, 10, 64)

		if err != nil {
			return Version{}, fmt.Errorf("%q is not a MAJOR.MINOR.PATCH version", value)
		}

		numbers[i] = number
	}

	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

// Compare returns -1 when v is older than other, 1 when it is newer, and 0 when they are the same
func (v Version) Compare(other Version) int {
	return cmp.Or(cmp.Compare(v.Major, other.Major), cmp.Compare(v.Minor, other.Minor), cmp.Compare(v.Patch, other.Patch))
}

// Change names the most significant part that differs between v and other: "major", "minor" or "patch", or an empty
// string when they are the same
func (v Version) Change(other Version) string {
	switch {
	case v.Major != other.Major:
		return "major"
	case v.Minor != other.Minor:
		return "minor"
	case v.Patch != other.Patch:
		return "patch"
	}

	return ""
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}
