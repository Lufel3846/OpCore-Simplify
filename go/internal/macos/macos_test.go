package macos

import (
	"errors"
	"testing"
)

func TestParsePyInt(t *testing.T) {
	valid := map[string]int{
		"0":    0,
		"25":   25,
		"+25":  25,
		"-1":   -1,
		" 25 ": 25,
		"2_5":  25,
		"0_0":  0,
	}
	for in, want := range valid {
		got, err := parsePyInt(in)
		if err != nil || got != want {
			t.Errorf("parsePyInt(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", " ", "_5", "5_", "2__5", "+-5", "- 5", "2.5", "x", "٢٥"} {
		if got, err := parsePyInt(in); err == nil {
			t.Errorf("parsePyInt(%q) = %d; want error", in, got)
		}
	}
}

func TestParseVersion(t *testing.T) {
	got, err := ParseVersion("25.0.0")
	if err != nil || got != (Version{25, 0, 0}) {
		t.Fatalf("ParseVersion(25.0.0) = %v, %v", got, err)
	}
	for _, in := range []string{"25.0", "25.0.0.0", "", "a.b.c", "25..0"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) succeeded; want error", in)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b Version
		want int
	}{
		{Version{17, 99, 99}, Version{18, 0, 0}, -1},
		{Version{19, 6, 0}, Version{19, 6, 0}, 0},
		{Version{19, 7, 0}, Version{19, 6, 9}, 1},
		{Version{25, 0, 1}, Version{25, 0, 0}, 1},
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Errorf("%v.Compare(%v) = %d; want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCatalogDarwinMapping(t *testing.T) {
	want := map[string]int{
		"High Sierra": 17,
		"Mojave":      18,
		"Catalina":    19,
		"Big Sur":     20,
		"Monterey":    21,
		"Ventura":     22,
		"Sonoma":      23,
		"Sequoia":     24,
		"Tahoe":       25,
	}
	rels := Releases()
	if len(rels) != len(want) {
		t.Fatalf("catalog has %d releases; want %d", len(rels), len(want))
	}
	for _, r := range rels {
		if want[r.Name] != r.DarwinVersion {
			t.Errorf("%s: darwin %d; want %d", r.Name, r.DarwinVersion, want[r.Name])
		}
	}
}

func TestLatestAndLowestDarwinVersion(t *testing.T) {
	if got := LatestDarwinVersion(true); got != "25.99.99" {
		t.Errorf("LatestDarwinVersion(true) = %q", got)
	}
	if got := LatestDarwinVersion(false); got != "25.99.99" {
		t.Errorf("LatestDarwinVersion(false) = %q", got)
	}
	if got := LowestDarwinVersion(); got != "17.0.0" {
		t.Errorf("LowestDarwinVersion() = %q", got)
	}
}

func TestMacOSName(t *testing.T) {
	cases := map[string]string{
		"25.0.0":   "macOS Tahoe 26",
		"25":       "macOS Tahoe 26",
		"19.6.0":   "macOS Catalina 10.15",
		"17.99.99": "macOS High Sierra 10.13",
		"20.1.0":   "macOS Big Sur 11",
	}
	for in, want := range cases {
		got, err := MacOSName(in)
		if err != nil || got != want {
			t.Errorf("MacOSName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := MacOSName("16.5.0"); !errors.Is(err, ErrUnknownRelease) {
		t.Errorf("MacOSName(16.5.0) error = %v; want ErrUnknownRelease", err)
	}
	if _, err := MacOSName("1."); err == nil || errors.Is(err, ErrUnknownRelease) {
		t.Errorf("MacOSName(1.) error = %v; want parse error", err)
	}
}
