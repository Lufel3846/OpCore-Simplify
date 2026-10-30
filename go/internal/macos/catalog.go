package macos

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Release is one macOS release, mirroring macOSVersionInfo in os_data.py.
type Release struct {
	Name          string
	MacOSVersion  string
	DarwinVersion int
	ReleaseStatus string
}

// ErrUnknownRelease is returned by MacOSName when no release uses the given
// Darwin major. The Python original returns None in that case.
var ErrUnknownRelease = errors.New("no macOS release matches this Darwin version")

// newRelease derives the Darwin major from the macOS version with the same
// rules as os_data.py: "10.x" maps to x+4, versions starting with "1" map to
// major+9 (Big Sur 11 → 20), and everything else maps to major-1 (Tahoe 26 → 25).
func newRelease(name, macosVersion string) Release {
	parts := strings.Split(macosVersion, ".")
	var darwin int
	switch {
	case strings.Contains(macosVersion, "10."):
		darwin = mustAtoi(parts[1]) + 4
	case strings.HasPrefix(macosVersion, "1"):
		darwin = mustAtoi(parts[0]) + 9
	default:
		darwin = mustAtoi(parts[0]) - 1
	}
	return Release{Name: name, MacOSVersion: macosVersion, DarwinVersion: darwin, ReleaseStatus: "final"}
}

// mustAtoi is only used on the static release table above, so a failure is a
// programming error and panics at package initialization.
func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("macos: invalid static release data %q: %v", s, err))
	}
	return n
}

// releases is ordered from oldest to newest, as in macos_versions in os_data.py.
var releases = []Release{
	newRelease("High Sierra", "10.13"),
	newRelease("Mojave", "10.14"),
	newRelease("Catalina", "10.15"),
	newRelease("Big Sur", "11"),
	newRelease("Monterey", "12"),
	newRelease("Ventura", "13"),
	newRelease("Sonoma", "14"),
	newRelease("Sequoia", "15"),
	newRelease("Tahoe", "26"),
}

// Releases returns a copy of the release catalog, oldest first.
func Releases() []Release {
	out := make([]Release, len(releases))
	copy(out, releases)
	return out
}

// LatestDarwinVersion mirrors get_latest_darwin_version: it returns
// "<darwin>.99.99" for the newest release. With includeBeta false only final
// releases are considered. Python returns None when no release qualifies; this
// returns "" in that case, which cannot happen with the current catalog.
func LatestDarwinVersion(includeBeta bool) string {
	for i := len(releases) - 1; i >= 0; i-- {
		r := releases[i]
		if includeBeta || r.ReleaseStatus == "final" {
			return fmt.Sprintf("%d.99.99", r.DarwinVersion)
		}
	}
	return ""
}

// LowestDarwinVersion mirrors get_lowest_darwin_version: "<darwin>.0.0" of the
// oldest release.
func LowestDarwinVersion() string {
	return fmt.Sprintf("%d.0.0", releases[0].DarwinVersion)
}

// MacOSName mirrors get_macos_name_by_darwin. Like the original, only the first
// two characters of darwin are used as the Darwin major, so "25.0.0" and "25"
// both resolve to Tahoe. A prefix that is not an integer returns an error (the
// Python original raises ValueError); a major with no release returns
// ErrUnknownRelease.
func MacOSName(darwin string) (string, error) {
	prefix := []rune(darwin)
	if len(prefix) > 2 {
		prefix = prefix[:2]
	}
	major, err := parsePyInt(string(prefix))
	if err != nil {
		return "", fmt.Errorf("darwin version %q: %w", darwin, err)
	}
	for _, r := range releases {
		if r.DarwinVersion == major {
			suffix := ""
			if r.ReleaseStatus != "final" {
				suffix = " (Beta)"
			}
			return fmt.Sprintf("macOS %s %s%s", r.Name, r.MacOSVersion, suffix), nil
		}
	}
	return "", ErrUnknownRelease
}
