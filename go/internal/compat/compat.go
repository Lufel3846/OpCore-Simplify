// Package compat is the Go port of Scripts/compatibility_checker.py. It checks
// every device of the hardware report against the macOS versions and records
// the result in the report, exactly as the Python CompatibilityChecker does.
//
// A "compatibility" value is the Python tuple (max_version, min_version). In the
// report it is stored as a two element []any whose items are a Darwin version
// string such as "22.99.99" or nil (Python None).
package compat

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// ErrExit stands for Python's utils.exit_program() (sys.exit). The checker
// returns it after printing the same messages and requesting input.
var ErrExit = errors.New("exit program")

// Checker mirrors CompatibilityChecker.
type Checker struct {
	// Log receives everything Python prints (nil discards it).
	Log io.Writer
	// RequestInput mirrors utils.request_input(prompt); nil skips the prompt.
	RequestInput func(prompt string) (string, error)
	// Delay is the pause after each step header (Python: time.sleep(0.25)).
	Delay time.Duration

	hardwareReport *plist.Dict

	maxNative string
	minNative string
	// oclpMax/oclpMin hold ocl_patched_macos_version; oclpSet is false for None.
	oclpMax, oclpMin string
	oclpSet          bool
}

// NewChecker returns a Checker with the original 0.25 s step delay.
func NewChecker() *Checker {
	return &Checker{Delay: 250 * time.Millisecond}
}

type failure struct{ err error }

func check(err error) {
	if err != nil {
		panic(failure{err})
	}
}

func recoverErr(err *error) {
	if r := recover(); r != nil {
		if f, ok := r.(failure); ok {
			*err = f.err
			return
		}
		panic(r)
	}
}

func get(obj any, key string) any {
	v, err := pyrt.Get(obj, key)
	check(err)
	return v
}

func getD(obj any, key string, def any) any {
	v, err := pyrt.GetDefault(obj, key, def)
	check(err)
	return v
}

func items(obj any) []pyrt.Pair {
	p, err := pyrt.Items(obj)
	check(err)
	return p
}

func values(obj any) []any {
	v, err := pyrt.Values(obj)
	check(err)
	return v
}

func in(needle, hay any) bool {
	r, err := pyrt.In(needle, hay)
	check(err)
	return r
}

func str(v any) string {
	s, err := pyrt.Str(v)
	check(err)
	return s
}

func asDict(v any) *plist.Dict {
	d, ok := v.(*plist.Dict)
	if !ok {
		check(fmt.Errorf("%w: %T is not a dict", pyrt.ErrType, v))
	}
	return d
}

func parse(v any) macos.Version {
	ver, err := macos.ParseVersion(str(v))
	check(err)
	return ver
}

func less(a, b any) bool { return parse(a).Compare(parse(b)) < 0 }

func greater(a, b any) bool { return parse(a).Compare(parse(b)) > 0 }

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func inList(s string, list ...string) bool { return pyrt.InStrings(s, list) }

// ver converts a version string ("" = None) to its report value.
func ver(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func pair(maxV, minV string) []any { return []any{ver(maxV), ver(minV)} }

func isNoneNone(v any) bool {
	l, ok := v.([]any)
	return ok && len(l) == 2 && l[0] == nil && l[1] == nil
}

func (c *Checker) printf(format string, a ...any) {
	if c.Log != nil {
		fmt.Fprintf(c.Log, format, a...)
	}
}

func (c *Checker) println(s string) { c.printf("%s\n", s) }

func (c *Checker) requestInput() {
	if c.RequestInput != nil {
		_, err := c.RequestInput("Press Enter to continue...")
		check(err)
	}
}

// requestInputExit is request_input() followed by exit_program().
func (c *Checker) requestInputExit() {
	c.requestInput()
	check(ErrExit)
}

func center(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		return s
	}
	marg := width - n
	left := marg/2 + (marg & width & 1)
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", marg-left)
}

// head mirrors utils.head (screen clear and title box).
func (c *Checker) head(text string) {
	const width = 68
	if c.Log == nil {
		return
	}
	c.printf("\033[H\033[2J")
	sep := strings.Repeat("═", width-2)
	title := " " + text + " "
	if r := []rune(title); len(r) > width-2 {
		title = string(r[:width-4]) + "..."
	}
	c.printf("╔%s╗\n║%s║\n╚%s╝\n", sep, center(title, width-2), sep)
}

func pyStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	}
	return fmt.Sprint(v)
}

func (c *Checker) macosName(darwin any) string {
	name, err := macos.MacOSName(str(darwin))
	if errors.Is(err, macos.ErrUnknownRelease) {
		return "None"
	}
	check(err)
	return name
}

// widestCompatibility mirrors _widest_compatibility: items are compatibility
// pairs (or nil); the result is the (max, min) pair, ("", "") for (None, None).
func (c *Checker) widestCompatibility(compats []any) []any {
	var kept [][]any
	for _, v := range compats {
		if pyrt.Truthy(v) && !isNoneNone(v) {
			l, ok := v.([]any)
			if !ok {
				check(fmt.Errorf("%w: compatibility is not a tuple", pyrt.ErrType))
			}
			kept = append(kept, l)
		}
	}
	if len(kept) == 0 {
		return []any{nil, nil}
	}
	best, worst := kept[0], kept[0]
	for _, k := range kept[1:] {
		if greater(k[0], best[0]) {
			best = k
		}
		if less(k[1], worst[1]) {
			worst = k
		}
	}
	return []any{best[0], worst[1]}
}

// restrictNativeCompatibility mirrors _restrict_native_compatibility.
func (c *Checker) restrictNativeCompatibility(compat []any) {
	if compat[0] == nil {
		return
	}
	if c.maxNative == "" || c.minNative == "" {
		check(errors.New("AttributeError: native macOS versions are not set"))
	}
	if less(compat[0], c.maxNative) {
		c.maxNative = str(compat[0])
	}
	if greater(compat[1], c.minNative) {
		c.minNative = str(compat[1])
	}
}

// updateOCLP mirrors the repeated ocl_patched_macos_version assignment:
// (newMax, existing[-1] if existing and parse(existing[-1]) < parse(last) else last).
func (c *Checker) updateOCLP(newMax, last any) {
	minV := last
	if c.oclpSet && less(c.oclpMin, last) {
		minV = c.oclpMin
	}
	c.oclpMax, c.oclpMin, c.oclpSet = str(newMax), str(minV), true
}

// ShowMacOSCompatibility mirrors show_macos_compatibility; deviceCompatibility
// is nil or the []any pair stored in the report.
func (c *Checker) ShowMacOSCompatibility(deviceCompatibility any) (out string, err error) {
	defer recoverErr(&err)
	return c.showMacOSCompatibility(deviceCompatibility), nil
}

func (c *Checker) showMacOSCompatibility(dc any) string {
	if !pyrt.Truthy(dc) {
		return "\033[90mUnchecked\033[0m"
	}
	l, ok := dc.([]any)
	if !ok {
		check(fmt.Errorf("%w: compatibility is not a tuple", pyrt.ErrType))
	}
	if !pyrt.Truthy(l[0]) {
		return "\033[0;31mUnsupported\033[0m"
	}
	first, last := l[0], l[len(l)-1]
	maxCompat := parse(first).Major
	minCompat := parse(last).Major
	maxVersion := parse(macos.LatestDarwinVersion(true)).Major
	minVersion := parse(macos.LowestDarwinVersion()).Major

	if maxCompat == minVersion {
		return fmt.Sprintf("\033[1;36mMaximum support up to %s\033[0m", c.macosName(last))
	}
	if minVersion < minCompat || maxCompat < maxVersion {
		return fmt.Sprintf("\033[1;32m%s to %s\033[0m", c.macosName(last), c.macosName(first))
	}
	return fmt.Sprintf("\033[1;36mUp to %s\033[0m", c.macosName(first))
}

// IsLowEndIntelCPU mirrors is_low_end_intel_cpu.
func (c *Checker) IsLowEndIntelCPU(processorName any) (res bool, err error) {
	defer recoverErr(&err)
	return c.isLowEndIntelCPU(processorName), nil
}

func (c *Checker) isLowEndIntelCPU(name any) bool {
	for _, b := range []string{"Celeron", "Pentium"} {
		if in(b, name) {
			return true
		}
	}
	return false
}

func (c *Checker) status(name string, compat any) {
	c.printf("%s- %s: %s\n", "   ", name, c.showMacOSCompatibility(compat))
}
