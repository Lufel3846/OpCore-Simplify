package acpiguru

import (
	"fmt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/term"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// chain mirrors obj.get(k1).get(k2)... on the hardware report.
func chain(obj any, keys ...string) (any, error) {
	cur := obj
	for _, k := range keys {
		next, err := pyrt.Get(cur, k)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// selector accumulates the first error, so the long condition list below reads
// like the original while every Python exception is still reported.
type selector struct {
	report any
	err    error
}

func (s *selector) get(keys ...string) any {
	if s.err != nil {
		return nil
	}
	v, err := chain(s.report, keys...)
	if err != nil {
		s.err = err
	}
	return v
}

// in is "needle in haystack".
func (s *selector) in(needle, haystack any) bool {
	if s.err != nil {
		return false
	}
	ok, err := pyrt.In(needle, haystack)
	if err != nil {
		s.err = err
	}
	return ok
}

// inList is "value in <list of str>".
func (s *selector) inList(value any, list []string) bool {
	if s.err != nil {
		return false
	}
	return pyrt.InStrings(value, list)
}

// IsIntelHEDTCPU mirrors is_intel_hedt_cpu.
func IsIntelHEDTCPU(processorName, codename any) (bool, error) {
	gens := datasets.IntelCPUGenerations
	if pyrt.InStrings(codename, pySliceStrings(gens, 45, 66)) {
		c := codename.(string)
		for _, suffix := range []string{"-X", "-P", "-W", "-E", "-EP", "-EX"} {
			if strings.HasSuffix(c, suffix) {
				return true, nil
			}
		}
		return false, nil
	}
	if pyrt.InStrings(codename, pySliceStrings(gens, 66, len(gens))) {
		return pyrt.In("Xeon", processorName)
	}
	return false, nil
}

// SelectACPIPatches mirrors select_acpi_patches: it sets Checked on every patch
// from the hardware report and the disabled devices.
func (g *Guru) SelectACPIPatches(hardwareReport, disabledDevices any) error {
	s := &selector{report: hardwareReport}
	var selected []string
	add := func(names ...string) { selected = append(selected, names...) }
	chipsets := datasets.IntelChipsets
	gens := datasets.IntelCPUGenerations
	cat := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	platform := func() any { return s.get("Motherboard", "Platform") }
	mbName := func() any { return s.get("Motherboard", "Name") }
	chipset := func() any { return s.get("Motherboard", "Chipset") }
	codename := func() any { return s.get("CPU", "Codename") }
	manufacturer := func() any { return s.get("CPU", "Manufacturer") }

	// Laptop with an integrated GPU (the last GPU entry) that is not a Surface.
	if s.in("Laptop", platform()) {
		gpus := s.get("GPU")
		if s.err == nil {
			items, err := pyrt.Items(gpus)
			if err != nil {
				s.err = err
			} else if len(items) == 0 {
				s.err = fmt.Errorf("IndexError: list index out of range")
			} else {
				dt, err := pyrt.Get(items[len(items)-1].Value, "Device Type")
				if err != nil {
					s.err = err
				} else if s.in("Integrated GPU", dt) && !s.in("SURFACE", mbName()) {
					add("ALS", "PNLF")
				}
			}
		}
	}
	if s.err != nil {
		return s.err
	}

	hedt, err := IsIntelHEDTCPU(s.get("CPU", "Processor Name"), codename())
	if s.err != nil {
		return s.err
	}
	if err != nil {
		return err
	}
	if hedt {
		add("APIC")
	}

	disabled, err := pyrt.Items(disabledDevices)
	if err != nil {
		return err
	}
	for _, d := range disabled {
		bus, err := pyrt.GetDefault(d.Value, "Bus Type", "PCI")
		if err != nil {
			return err
		}
		if s.in("PCI", bus) {
			add("Disable Devices")
		}
		if s.err != nil {
			return s.err
		}
	}
	add("FakeEC")

	if s.in("HP ", mbName()) {
		add("CMOS")
	}
	if s.inList(chipset(), pySliceStrings(chipsets, -7, len(chipsets))) {
		add("RCSP")
	}
	if s.in("Laptop", platform()) && s.inList(codename(), pySliceStrings(gens, 50, len(gens))) {
		add("FixHPET")
	}
	if s.err != nil {
		return s.err
	}

	sysDevices, err := pyrt.GetDefault(hardwareReport, "System Devices", plist.NewDict())
	if err != nil {
		return err
	}
	devs, err := pyrt.Items(sysDevices)
	if err != nil {
		return err
	}
	for _, d := range devs {
		id, err := pyrt.Get(d.Value, "Device ID")
		if err != nil {
			return err
		}
		if !(pyrt.Equal(id, "8086-1C3A") || pyrt.Equal(id, "8086-1E3A")) {
			continue
		}
		// "X in cond and id in '8086-1E3A' or Y in cond and id in '8086-1C3A'"
		code := codename()
		if s.err != nil {
			return s.err
		}
		sandy := s.in("Sandy Bridge", code)
		if sandy && s.in(id, "8086-1E3A") {
			add("IMEI")
			continue
		}
		if s.in("Ivy Bridge", code) && s.in(id, "8086-1C3A") {
			add("IMEI")
		}
		if s.err != nil {
			return s.err
		}
	}
	if s.err != nil {
		return s.err
	}

	if s.inList(chipset(), pySliceStrings(chipsets, 100, 112)) {
		add("PMC")
	}
	if s.in("Sandy Bridge", codename()) || s.in("Ivy Bridge", codename()) {
		add("PM (Legacy)")
	} else {
		add("PLUG")
	}
	if s.err != nil {
		return s.err
	}

	networks, err := pyrt.GetDefault(hardwareReport, "Network", plist.NewDict())
	if err != nil {
		return err
	}
	nets, err := pyrt.Items(networks)
	if err != nil {
		return err
	}
	allUSB := true
	for _, n := range nets {
		bus, err := pyrt.Get(n.Value, "Bus Type")
		if err != nil {
			return err
		}
		if !pyrt.Equal(bus, "USB") {
			allUSB = false
			break
		}
	}
	if allUSB {
		add("RMNE")
	}

	if s.inList(chipset(), cat(pySliceStrings(chipsets, 62, 64), pySliceStrings(chipsets, 90, 100))) {
		add("RTC0")
	}
	if s.in("AMD", manufacturer()) || s.inList(codename(), pySliceStrings(gens, 0, 40)) {
		add("RTCAWAC")
	}
	if s.in("Intel", manufacturer()) {
		add("BUS0")
	}
	if s.err != nil {
		return s.err
	}

	if s.in("SURFACE", mbName()) {
		add("Surface Patch")
	} else if s.in("Intel", manufacturer()) {
		inputs, err := pyrt.GetDefault(hardwareReport, "Input", plist.NewDict())
		if err != nil {
			return err
		}
		items, err := pyrt.Items(inputs)
		if err != nil {
			return err
		}
		for _, it := range items {
			dt, err := pyrt.GetDefault(it.Value, "Device Type", "None")
			if err != nil {
				return err
			}
			if s.in("I2C", dt) {
				add("GPI0")
			}
			if s.err != nil {
				return s.err
			}
		}
	}
	if s.err != nil {
		return s.err
	}

	if s.inList(chipset(), cat(pySliceStrings(chipsets, 27, 28), pySliceStrings(chipsets, 62, 64))) {
		add("UNC")
	}
	if s.in("AMD", manufacturer()) || s.inList(chipset(), pySliceStrings(chipsets, 112, len(chipsets))) {
		add("USB Reset")
	}
	add("USBX")
	if s.in("Laptop", platform()) {
		add("BATP", "XOSI")
	}
	if s.err != nil {
		return s.err
	}

	for _, d := range devs {
		bus, err := pyrt.Get(d.Value, "Bus Type")
		if err != nil {
			return err
		}
		if !pyrt.Equal(bus, "ACPI") {
			continue
		}
		dev, err := pyrt.Get(d.Value, "Device")
		if err != nil {
			return err
		}
		if pyrt.InStrings(dev, datasets.YogaHIDs) {
			add("WMIS")
		}
	}

	for _, p := range g.Patches {
		p.Checked = hasString(selected, p.Name)
	}
	return nil
}

// CustomizePatchSelection mirrors customize_patch_selection. Indices accept
// decimal digits of any script, as int() does. Characters that Python's isdigit
// accepts but int() rejects (superscripts, circled digits) are not special-cased:
// there Python raises ValueError and this port skips the item.
func (g *Guru) CustomizePatchSelection() error {
	for {
		contents := []string{"", "List of available patches:", ""}
		for i, p := range g.Patches {
			box := "[ ]"
			if p.Checked {
				box = "[*]"
			}
			line := fmt.Sprintf("%s %2d. %-15s - %-60s", box, i+1, p.Name, p.Description)
			if p.Checked {
				line = "\033[1;32m" + line + "\033[0m"
			}
			contents = append(contents, line)
		}
		contents = append(contents, "", "\033[1;93mNote:\033[0m You can select multiple kexts by entering their indices separated by commas (e.g., '1, 2, 3').", "", "B. Back", "Q. Quit", "")
		content := strings.Join(contents, "\n")
		term.AdjustWindowSize(g.Log, content)
		term.Head(g.Log, "Customize ACPI Patch Selections", 68, false)
		g.logf("%s\n", content)
		option, err := g.RequestInput("Select your option: ")
		if err != nil {
			return err
		}
		switch strings.ToLower(option) {
		case "q":
			return g.ExitProgram()
		case "b":
			return nil
		}
		var indices []int
		for _, part := range strings.Split(option, ",") {
			part = pyStrip(part)
			n, ok := decimalValue(part)
			if !ok {
				continue
			}
			if !n.IsInt64() || n.Int64() > int64(len(g.Patches)) {
				n = big.NewInt(int64(len(g.Patches)) + 1) // out of range: ignored below
			}
			indices = append(indices, int(n.Int64())-1)
		}
		for _, i := range indices {
			if i >= 0 && i < len(g.Patches) {
				g.Patches[i].Checked = !g.Patches[i].Checked
			}
		}
	}
}

// decimalValue parses a non-empty string made only of Unicode decimal digits
// (category Nd), like int() on a string that isdigit() accepted.
func decimalValue(s string) (*big.Int, bool) {
	if s == "" {
		return nil, false
	}
	n := new(big.Int)
	ten := big.NewInt(10)
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return nil, false
		}
		start := r
		for unicode.IsDigit(start - 1) {
			start--
		}
		n.Mul(n, ten)
		n.Add(n, big.NewInt(int64((r-start)%10)))
	}
	return n, true
}

var quoteLead = regexp.MustCompile(`^['"]+`)

// NormalizePath mirrors utils.normalize_path on POSIX systems: it strips quotes
// and spaces, expands "~", removes backslashes, normalizes and resolves symlinks.
func NormalizePath(path string) (string, error) {
	path = quoteLead.ReplaceAllString(path, "")
	// "['\"]+$" also matches just before one trailing newline.
	tail := ""
	if strings.HasSuffix(path, "\n") {
		path, tail = path[:len(path)-1], "\n"
	}
	path = strings.TrimRight(path, `'"`) + tail
	path = pyStrip(path)
	path = expandUser(path)
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, `\`, "/")
		path = regexp.MustCompile(`/+`).ReplaceAllString(path, "/")
	} else {
		path = strings.ReplaceAll(path, `\`, "")
	}
	path = normPath(path)
	return resolve(path)
}

func expandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	i := strings.Index(path, "/")
	if i < 0 {
		i = len(path)
	}
	var home string
	if i == 1 {
		if h, ok := os.LookupEnv("HOME"); ok {
			home = h
		} else if u, err := user.Current(); err == nil {
			home = u.HomeDir
		} else {
			return path
		}
	} else {
		u, err := user.Lookup(path[1:i])
		if err != nil {
			return path
		}
		home = u.HomeDir
	}
	home = strings.TrimRight(home, "/")
	if r := home + path[i:]; r != "" {
		return r
	}
	return "/"
}

// normPath is posixpath.normpath: filepath.Clean, except that exactly two
// leading slashes are kept.
func normPath(p string) string {
	if p == "" {
		return "."
	}
	c := filepath.Clean(p)
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") && !strings.HasPrefix(c, "//") {
		c = "/" + c
	}
	return c
}

// resolve is pathlib.Path(path).resolve() (non-strict): an absolute path with
// the symlinks of its existing part resolved.
func resolve(path string) (string, error) {
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(wd, path)
	}
	path = filepath.Clean(path)
	rest := ""
	cur := path
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return real, nil
			}
			return filepath.Join(real, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
