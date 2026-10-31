// Package customizer is the Go port of Scripts/hardware_customizer.py.
//
// The hardware report is the *plist.Dict tree produced by pyjson. Errors are
// returned where the Python original would raise.
package customizer

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// CompatChecker is the part of compatibility_checker.CompatibilityChecker used
// by the customizer.
type CompatChecker interface {
	// ShowMacOSCompatibility mirrors show_macos_compatibility(device_compatibility).
	// The argument is nil, a []any of version strings, or any other report value.
	ShowMacOSCompatibility(deviceCompatibility any) (string, error)
}

// Customizer mirrors HardwareCustomizer.
type Customizer struct {
	// CompatibilityChecker is self.compatibility_checker.
	CompatibilityChecker CompatChecker
	// RequestInput mirrors utils.request_input.
	RequestInput func(prompt string) (string, error)
	// Log receives print/head output; nil discards it.
	Log io.Writer

	HardwareReport     any
	MacOSVersion       string
	CustomizedHardware *plist.Dict
	DisabledDevices    *plist.Dict
	SelectedDevices    *plist.Dict
}

func (c *Customizer) printf(format string, a ...any) {
	if c.Log != nil {
		fmt.Fprintf(c.Log, format, a...)
	}
}

func (c *Customizer) println(s string) { c.printf("%s\n", s) }

// head mirrors utils.head (banner only; the screen clear and the window resize
// are not reproduced).
func (c *Customizer) head(text string) {
	const width = 68
	sep := strings.Repeat("═", width-2)
	title := " " + text + " "
	if r := []rune(title); len(r) > width-2 {
		title = string(r[:width-4]) + "..."
	}
	title = center(title, width-2)
	c.printf("╔%s╗\n║%s║\n╚%s╝\n", sep, title, sep)
}

// center is str.center.
func center(s string, width int) string {
	pad := width - len([]rune(s))
	if pad <= 0 {
		return s
	}
	left := pad/2 + (pad & width & 1)
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
}

// ---- Python helpers ----------------------------------------------------------

func asDict(v any) (*plist.Dict, error) {
	d, ok := v.(*plist.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: %T has no attribute 'copy'", pyrt.ErrAttribute, v)
	}
	return d, nil
}

func copyDict(d *plist.Dict) *plist.Dict {
	n := plist.NewDict()
	for _, k := range d.Keys() {
		v, _ := d.Get(k)
		n.Set(k, v)
	}
	return n
}

// elem mirrors v[i] on a list (negative i counts from the end).
func elem(v any, i int) (any, error) {
	l, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: %T is not subscriptable", pyrt.ErrType, v)
	}
	if i < 0 {
		i += len(l)
	}
	if i < 0 || i >= len(l) {
		return nil, errors.New("IndexError: list index out of range")
	}
	return l[i], nil
}

// parseVer mirrors utils.parse_darwin_version.
func parseVer(v any) (macos.Version, error) {
	s, ok := v.(string)
	if !ok {
		return macos.Version{}, fmt.Errorf("%w: %T has no attribute 'split'", pyrt.ErrAttribute, v)
	}
	return macos.ParseVersion(s)
}

// parseAt is parse_darwin_version(v[i]).
func parseAt(v any, i int) (macos.Version, error) {
	e, err := elem(v, i)
	if err != nil {
		return macos.Version{}, err
	}
	return parseVer(e)
}

// chain mirrors parse(c[0]) >= parse(m) >= parse(c[-1]) with short-circuiting.
func chain(c any, m string) (bool, error) {
	hi, err := parseAt(c, 0)
	if err != nil {
		return false, err
	}
	mv, err := parseVer(m)
	if err != nil {
		return false, err
	}
	if hi.Compare(mv) < 0 {
		return false, nil
	}
	lo, err := parseAt(c, -1)
	if err != nil {
		return false, err
	}
	return mv.Compare(lo) >= 0, nil
}

// pyStr is str(v) / "{}".format(v) for report values.
func pyStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case int64:
		return strconv.FormatInt(x, 10)
	case *big.Int:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	return fmt.Sprint(v)
}

// padRight is "{:<n}".format(s) for a string.
func padRight(s string, n int) string {
	if d := n - len([]rune(s)); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// ---- HardwareCustomizer ------------------------------------------------------

// HardwareCustomization mirrors hardware_customization. It returns
// (customized_hardware, disabled_devices, needs_oclp): the first two are ordered
// dicts, the last a bool.
func (c *Customizer) HardwareCustomization(hardwareReport any, macosVersion string) (*plist.Dict, *plist.Dict, bool, error) {
	c.HardwareReport = hardwareReport
	c.MacOSVersion = macosVersion
	c.CustomizedHardware = plist.NewDict()
	c.DisabledDevices = plist.NewDict()
	c.SelectedDevices = plist.NewDict()
	needsOCLP := false

	c.head("Hardware Customization")

	items, err := pyrt.Items(hardwareReport)
	if err != nil {
		return nil, nil, false, err
	}
	for _, item := range items {
		deviceType, devices := item.Key, item.Value
		switch deviceType {
		case "BIOS", "GPU", "Sound", "Biometric", "Network", "Storage Controllers", "Bluetooth", "SD Controller":
		default:
			c.CustomizedHardware.Set(deviceType, devices)
			continue
		}

		c.CustomizedHardware.Set(deviceType, plist.NewDict())

		if deviceType == "BIOS" {
			dd, err := asDict(devices)
			if err != nil {
				return nil, nil, false, err
			}
			bios := copyDict(dd)
			c.CustomizedHardware.Set(deviceType, bios)
			fw, _ := dd.Get("Firmware Type")
			if !pyrt.Equal(fw, "UEFI") {
				c.println("\n*** BIOS Firmware Type is not UEFI")
				c.println("")
				c.println("Do you want to build the EFI for UEFI?")
				c.println("If yes, please make sure to update your BIOS and enable UEFI Boot Mode in your BIOS settings.")
				c.println("You can still proceed with Legacy if you prefer.")
				c.println("")

				for {
					a, err := c.RequestInput("Build EFI for UEFI? (Yes/no): ")
					if err != nil {
						return nil, nil, false, err
					}
					answer := strings.ToLower(strings.TrimSpace(a))
					if answer == "yes" {
						bios.Set("Firmware Type", "UEFI")
						break
					} else if answer == "no" {
						bios.Set("Firmware Type", "Legacy")
						break
					}
					c.println("\033[91mInvalid selection, please try again.\033[0m\n\n")
				}
			}
			continue
		}

		devs, ok := devices.(*plist.Dict)
		if !ok {
			return nil, nil, false, fmt.Errorf("%w: %T is not a dict", pyrt.ErrType, devices)
		}
		customizedType := plist.NewDict()
		c.CustomizedHardware.Set(deviceType, customizedType)

		for _, deviceName := range devs.Keys() {
			raw, _ := devs.Get(deviceName)
			rd, err := asDict(raw)
			if err != nil {
				return nil, nil, false, err
			}
			props := copyDict(rd)

			oclp, _ := props.Get("OCLP Compatibility")
			if pyrt.Truthy(oclp) {
				ok, err := chain(oclp, macosVersion)
				if err != nil {
					return nil, nil, false, err
				}
				if ok {
					customizedType.Set(deviceName, props)
					needsOCLP = true
					continue
				}
			}

			var compat any
			if v, found := props.Get("Compatibility"); found {
				compat = v
			} else {
				compat = []any{macos.LatestDarwinVersion(true), macos.LowestDarwinVersion()}
			}

			isCompatible, err := chain(compat, macosVersion)
			if err != nil {
				isCompatible = false
			}

			if isCompatible {
				customizedType.Set(deviceName, props)
			} else {
				dt, _ := props.Get("Device Type")
				prefix := deviceType
				if _, found := props.Get("Device Type"); found {
					unknown, err := pyrt.In("Unknown", dt)
					if err != nil {
						return nil, nil, false, err
					}
					if !unknown {
						prefix = pyStr(dt)
					}
				}
				suffix := ""
				if ae, _ := props.Get("Audio Endpoints"); pyrt.Truthy(ae) {
					list, ok := ae.([]any)
					if !ok {
						return nil, nil, false, fmt.Errorf("%w: cannot join %T", pyrt.ErrType, ae)
					}
					parts := make([]string, len(list))
					for i, e := range list {
						s, ok := e.(string)
						if !ok {
							return nil, nil, false, fmt.Errorf("%w: sequence item %d: expected str instance, %T found", pyrt.ErrType, i, e)
						}
						parts[i] = s
					}
					suffix = " (" + strings.Join(parts, ", ") + ")"
				}
				c.DisabledDevices.Set(prefix+": "+deviceName+suffix, props)
			}

			if cur, found := customizedType.Get(deviceName); found && pyrt.Truthy(cur) {
				cd, ok := cur.(*plist.Dict)
				if !ok {
					return nil, nil, false, fmt.Errorf("%w: %T has no attribute 'get'", pyrt.ErrAttribute, cur)
				}
				if o, _ := cd.Get("OCLP Compatibility"); pyrt.Truthy(o) {
					cd.Delete("OCLP Compatibility")
				}
			}
		}

		if customizedType.Len() == 0 {
			c.CustomizedHardware.Delete(deviceType)
		} else if deviceType == "GPU" || deviceType == "Network" || deviceType == "Bluetooth" {
			t := deviceType
			if t == "Network" {
				t = "WiFi"
			}
			if err := c.HandleDeviceSelection(t); err != nil {
				return nil, nil, false, err
			}
		}
	}

	if c.SelectedDevices.Len() > 0 {
		c.head("Device Selection Summary")
		c.println("")
		c.println("Selected devices:")
		c.println("")
		c.println("Type          Device                                     Device ID")
		c.println("------------------------------------------------------------------")
		for _, typ := range c.SelectedDevices.Keys() {
			dv, _ := c.SelectedDevices.Get(typ)
			dict := dv.(*plist.Dict)
			for _, name := range dict.Keys() {
				pv, _ := dict.Get(name)
				id, err := pyrt.GetDefault(pv, "Device ID", "Unknown")
				if err != nil {
					return nil, nil, false, err
				}
				short := name
				if r := []rune(name); len(r) > 38 {
					short = string(r[:38])
				}
				c.printf("%s %s %s\n", padRight(typ, 13), padRight(short, 42), pyStr(id))
			}
		}
		c.println("")
		c.println("All other devices of the same type have been disabled.")
		c.println("")
		if _, err := c.RequestInput("Press Enter to continue..."); err != nil {
			return nil, nil, false, err
		}
	}

	return c.CustomizedHardware, c.DisabledDevices, needsOCLP, nil
}

// GetDeviceCombinations mirrors _get_device_combinations: all non-empty
// subsets of the indices, sorted by (length, lexicographic).
func (c *Customizer) GetDeviceCombinations(deviceIndices []int) [][]int {
	devices := append([]int(nil), deviceIndices...)
	sort.Ints(devices)
	n := len(devices)
	if n == 0 {
		return nil
	}
	var all [][]int
	for i := 1; i < 1<<n; i++ {
		var cur []int
		for j := 0; j < n; j++ {
			if (i>>j)&1 == 1 {
				cur = append(cur, devices[j])
			}
		}
		all = append(all, cur)
	}
	sort.SliceStable(all, func(a, b int) bool {
		x, y := all[a], all[b]
		if len(x) != len(y) {
			return len(x) < len(y)
		}
		for k := range x {
			if x[k] != y[k] {
				return x[k] < y[k]
			}
		}
		return false
	})
	return all
}

type intSet map[int]struct{}

func union(sets ...intSet) []int {
	seen := intSet{}
	for _, s := range sets {
		for k := range s {
			seen[k] = struct{}{}
		}
	}
	out := make([]int, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// HandleDeviceSelection mirrors _handle_device_selection.
func (c *Customizer) HandleDeviceSelection(deviceType string) error {
	devices, err := c.GetCompatibleDevices(deviceType)
	if err != nil {
		return err
	}
	var deviceGroups [][]int

	if devices.Len() > 1 {
		c.printf("\n*** Multiple %s Devices Detected\n", deviceType)
		if deviceType == "WiFi" || deviceType == "Bluetooth" {
			c.printf("macOS works best with only one %s device enabled.\n", deviceType)
		} else if deviceType == "GPU" {
			apuIndex := -1 // None
			navi22, navi, intel, other := intSet{}, intSet{}, intSet{}, intSet{}

			for index, name := range devices.Keys() {
				pv, _ := devices.Get(name)
				manufacturer, err := pyrt.Get(pv, "Manufacturer")
				if err != nil {
					return err
				}
				codename, _ := pyrt.Get(pv, "Codename")
				gpuType, _ := pyrt.Get(pv, "Device Type")

				if pyrt.Equal(manufacturer, "AMD") {
					if pyrt.Equal(gpuType, "Integrated GPU") {
						apuIndex = index
						continue
					} else if pyrt.Equal(gpuType, "Discrete GPU") {
						cs, ok := codename.(string)
						if !ok {
							return fmt.Errorf("%w: %T has no attribute 'startswith'", pyrt.ErrAttribute, codename)
						}
						if strings.HasPrefix(cs, "Navi") {
							if cs == "Navi 22" {
								navi22[index] = struct{}{}
							} else {
								navi[index] = struct{}{}
							}
							continue
						}
					}
				} else if pyrt.Equal(manufacturer, "Intel") {
					intel[index] = struct{}{}
					continue
				}
				other[index] = struct{}{}
			}

			// "if _apu_index" is falsy for None and for index 0.
			apuTruthy := apuIndex > 0
			if apuTruthy || len(navi22) > 0 {
				c.println("Multiple active GPUs can cause kext conflicts in macOS.")
			}

			deviceGroups = [][]int{}
			if apuTruthy {
				deviceGroups = append(deviceGroups, union(intSet{apuIndex: {}}, other))
			}
			if len(navi22) > 0 {
				deviceGroups = append(deviceGroups, union(navi22, other))
			}
			if len(navi) > 0 || len(intel) > 0 || len(other) > 0 {
				deviceGroups = append(deviceGroups, union(navi, intel, other))
			}
		}

		selected, err := c.SelectDevice(deviceType, devices, deviceGroups)
		if err != nil {
			return err
		}
		for _, s := range selected {
			cur, found := c.SelectedDevices.Get(deviceType)
			var d *plist.Dict
			if found {
				d = cur.(*plist.Dict)
			} else {
				d = plist.NewDict()
				c.SelectedDevices.Set(deviceType, d)
			}
			v, _ := devices.Get(s)
			d.Set(s, v)
		}
	}
	return nil
}

// GetCompatibleDevices mirrors _get_compatible_devices.
func (c *Customizer) GetCompatibleDevices(deviceType string) (*plist.Dict, error) {
	compatible := plist.NewDict()
	category := deviceType
	if deviceType == "WiFi" {
		category = "Network"
	}
	cur, found := c.CustomizedHardware.Get(category)
	if !found {
		return compatible, nil
	}
	pairs, err := pyrt.Items(cur)
	if err != nil {
		return nil, err
	}
	for _, p := range pairs {
		if deviceType == "WiFi" {
			id, err := pyrt.Get(p.Value, "Device ID")
			if err != nil {
				return nil, err
			}
			if !pyrt.InStrings(id, datasets.WirelessCardIDs) {
				continue
			}
		}
		compatible.Set(p.Key, p.Value)
	}
	return compatible, nil
}

type combo struct {
	names   []string
	indices []int
	compat  any
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *Customizer) show(v any) (string, error) {
	return c.CompatibilityChecker.ShowMacOSCompatibility(v)
}

// parseChoice mirrors int(choice) for the menus: ok is false on ValueError, and
// n is clamped out of range for values that do not fit an int.
func parseChoice(s string) (n int, ok bool) {
	v, err := pyrt.Int(s)
	if err != nil {
		return 0, false
	}
	switch x := v.(type) {
	case int64:
		if x > 1<<31 || x < -(1<<31) {
			return 1 << 31, true
		}
		return int(x), true
	}
	return 1 << 31, true
}

// SelectDevice mirrors _select_device. deviceGroups is a list of index sets
// (nil/empty = the plain menu). It returns the names of the selected devices.
func (c *Customizer) SelectDevice(deviceType string, devices *plist.Dict, deviceGroups [][]int) ([]string, error) {
	c.println("")
	if len(deviceGroups) > 0 {
		c.printf("Please select a %s combination configuration:\n", deviceType)
	} else {
		c.printf("Please select which %s device you want to use:\n", deviceType)
	}
	c.println("")

	names := devices.Keys()

	if len(deviceGroups) > 0 {
		var valid []combo

		for _, group := range deviceGroups {
			for _, dc := range c.GetDeviceCombinations(group) {
				var groupDevices []string
				var groupCompat any
				var groupIdx []int
				hasOCLP := false

				for _, index := range dc {
					name := names[index]
					pv, _ := devices.Get(name)
					groupDevices = append(groupDevices, name)
					groupIdx = append(groupIdx, index)
					compat, err := pyrt.Get(pv, "Compatibility")
					if err != nil {
						return nil, err
					}
					if pyrt.Truthy(compat) {
						if groupCompat == nil {
							groupCompat = compat
						} else {
							a, err := parseAt(compat, 0)
							if err != nil {
								return nil, err
							}
							b, err := parseAt(groupCompat, 0)
							if err != nil {
								return nil, err
							}
							if a.Compare(b) < 0 {
								c0, _ := elem(compat, 0)
								g1, err := elem(groupCompat, 1)
								if err != nil {
									return nil, err
								}
								groupCompat = []any{c0, g1}
							}
							a, err = parseAt(compat, 1)
							if err != nil {
								return nil, err
							}
							b, err = parseAt(groupCompat, 1)
							if err != nil {
								return nil, err
							}
							if a.Compare(b) > 0 {
								g0, _ := elem(groupCompat, 0)
								c1, _ := elem(compat, 1)
								groupCompat = []any{g0, c1}
							}
						}
					}
					if o, _ := pyrt.Get(pv, "OCLP Compatibility"); pyrt.Truthy(o) {
						hasOCLP = true
					}
				}

				if hasOCLP && len(dc) > 1 {
					continue
				}
				if len(groupDevices) > 0 {
					dup := false
					for _, v := range valid {
						if sameInts(v.indices, groupIdx) && pyrt.Equal(v.compat, groupCompat) {
							dup = true
							break
						}
					}
					if !dup {
						valid = append(valid, combo{groupDevices, groupIdx, groupCompat})
					}
				}
			}
		}

		// sort key: (len(devices), group_compatibility[0])
		keys := make([]string, len(valid))
		for i, v := range valid {
			e, err := elem(v.compat, 0)
			if err != nil {
				return nil, err
			}
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%w: cannot order %T", pyrt.ErrType, e)
			}
			keys[i] = s
		}
		order := make([]int, len(valid))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			x, y := order[a], order[b]
			if len(valid[x].names) != len(valid[y].names) {
				return len(valid[x].names) < len(valid[y].names)
			}
			return keys[x] < keys[y]
		})
		sorted := make([]combo, len(valid))
		for i, o := range order {
			sorted[i] = valid[o]
		}
		valid = sorted

		for idx, v := range valid {
			c.printf("%d. %s\n", idx+1, strings.Join(v.names, " + "))
			if pyrt.Truthy(v.compat) {
				s, err := c.show(v.compat)
				if err != nil {
					return nil, err
				}
				c.printf("   Compatibility: %s\n", s)
			}
			if len(v.names) == 1 {
				pv, _ := devices.Get(v.names[0])
				if o, _ := pyrt.Get(pv, "OCLP Compatibility"); pyrt.Truthy(o) {
					a, err := parseAt(o, 0)
					if err != nil {
						return nil, err
					}
					b, err := parseAt(v.compat, 0)
					if err != nil {
						return nil, err
					}
					if a.Compare(b) > 0 {
						o0, _ := elem(o, 0)
						s, err := c.show([]any{o0, macos.LowestDarwinVersion()})
						if err != nil {
							return nil, err
						}
						c.printf("   OCLP Compatibility: %s\n", s)
					}
				}
			}
			c.println("")
		}

		for {
			choice, err := c.RequestInput(fmt.Sprintf("Select a %s combination (1-%d): ", deviceType, len(valid)))
			if err != nil {
				return nil, err
			}
			n, ok := parseChoice(choice)
			if !ok {
				c.println("Please enter a valid number.")
				continue
			}
			if n >= 1 && n <= len(valid) {
				selected := valid[n-1].names
				for _, d := range names {
					if !pyrt.InStrings(d, selected) {
						pv, _ := devices.Get(d)
						if err := c.DisableDevice(deviceType, d, pv); err != nil {
							return nil, err
						}
					}
				}
				return selected, nil
			}
			c.println("Invalid option. Please try again.")
		}
	}

	for i, name := range names {
		pv, _ := devices.Get(name)
		compat, err := pyrt.Get(pv, "Compatibility")
		if err != nil {
			return nil, err
		}
		id, _ := pyrt.GetDefault(pv, "Device ID", "Unknown")

		c.printf("%d. %s\n", i+1, name)
		c.printf("   Device ID: %s\n", pyStr(id))
		s, err := c.show(compat)
		if err != nil {
			return nil, err
		}
		c.printf("   Compatibility: %s\n", s)

		if o, _ := pyrt.Get(pv, "OCLP Compatibility"); pyrt.Truthy(o) {
			a, err := parseAt(o, 0)
			if err != nil {
				return nil, err
			}
			b, err := parseAt(compat, 0)
			if err != nil {
				return nil, err
			}
			if a.Compare(b) > 0 {
				o0, _ := elem(o, 0)
				s, err := c.show([]any{o0, macos.LowestDarwinVersion()})
				if err != nil {
					return nil, err
				}
				c.printf("   OCLP Compatibility: %s\n", s)
			}
		}
		c.println("")
	}

	for {
		choice, err := c.RequestInput(fmt.Sprintf("Select a %s device (1-%d): ", deviceType, len(names)))
		if err != nil {
			return nil, err
		}
		n, ok := parseChoice(choice)
		if !ok {
			c.println("Please enter a valid number.")
			continue
		}
		if n >= 1 && n <= len(names) {
			selected := names[n-1]
			for _, d := range names {
				if d != selected {
					pv, _ := devices.Get(d)
					if err := c.DisableDevice(deviceType, d, pv); err != nil {
						return nil, err
					}
				}
			}
			return []string{selected}, nil
		}
		c.println("Invalid option. Please try again.")
	}
}

// DisableDevice mirrors _disable_device.
func (c *Customizer) DisableDevice(deviceType, deviceName string, deviceProps any) error {
	category := deviceType
	if deviceType == "WiFi" {
		id, err := pyrt.Get(deviceProps, "Device ID")
		if err != nil {
			return err
		}
		if !pyrt.Truthy(id) || !pyrt.InStrings(id, datasets.WirelessCardIDs) {
			return nil
		}
		category = "Network"
	}

	if cur, found := c.CustomizedHardware.Get(category); found {
		if cd, ok := cur.(*plist.Dict); ok {
			if _, has := cd.Get(deviceName); has {
				cd.Delete(deviceName)
				if cd.Len() == 0 {
					c.CustomizedHardware.Delete(category)
				}
			}
		}
	}

	c.DisabledDevices.Set(category+": "+deviceName, deviceProps)
	return nil
}
