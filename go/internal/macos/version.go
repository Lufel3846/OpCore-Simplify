// Package macos is the Go port of the macOS/Darwin version helpers found in
// Scripts/datasets/os_data.py and Scripts/utils.py (parse_darwin_version) of
// OpCore Simplify. Behavior is kept equivalent to the Python original; the
// differences that remain are listed in go/README.md.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2601). See go/LICENSE.
package macos

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a Darwin kernel version such as 25.0.0 (macOS Tahoe).
type Version struct {
	Major, Minor, Patch int
}

// ParseVersion mirrors Utils.parse_darwin_version. The input must contain
// exactly three dot-separated integers; each one is read with parsePyInt.
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("darwin version %q: expected 3 components, got %d", s, len(parts))
	}
	var nums [3]int
	for i, p := range parts {
		n, err := parsePyInt(p)
		if err != nil {
			return Version{}, fmt.Errorf("darwin version %q: %w", s, err)
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
}

// Compare returns -1, 0 or +1. It orders versions the same way Python compares
// the tuples returned by parse_darwin_version.
func (v Version) Compare(o Version) int {
	a := [3]int{v.Major, v.Minor, v.Patch}
	b := [3]int{o.Major, o.Minor, o.Patch}
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// String formats the version as "major.minor.patch".
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// parsePyInt parses s following Python's int(s) for base 10 with ASCII digits:
// surrounding whitespace is ignored, an optional sign is accepted, and single
// underscores may separate digits ("2_5" is 25). Non-ASCII digits are rejected.
func parsePyInt(s string) (int, error) {
	t := strings.TrimSpace(s)
	sign := 1
	if strings.HasPrefix(t, "+") {
		t = t[1:]
	} else if strings.HasPrefix(t, "-") {
		sign = -1
		t = t[1:]
	}
	if t == "" || t[0] == '_' || t[len(t)-1] == '_' || strings.Contains(t, "__") {
		return 0, fmt.Errorf("invalid integer literal %q", s)
	}
	digits := strings.ReplaceAll(t, "_", "")
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid integer literal %q", s)
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("integer literal out of range %q", s)
	}
	return sign * n, nil
}
