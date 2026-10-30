// Package kextmaestro ports Scripts/kext_maestro.py: it decides which kexts a
// hardware report needs, checks their dependencies and conflicts, installs them
// into the EFI, and builds the Kernel > Add entries of config.plist.
//
// The interactive terminal menu (kext_configuration_menu) is not ported; its
// prompts are answered through RequestInput. Terminal output is not reproduced.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2601). See go/LICENSE.
package kextmaestro

import (
	"bufio"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// ErrWifiProfilesNotPorted is returned when a WiFiConfig must be filled from the
// stored Wi-Fi profiles, which need wifi_profile_extractor.py (not ported yet).
var ErrWifiProfilesNotPorted = errors.New("Wi-Fi profile extraction (wifi_profile_extractor) is not ported yet")

// ACPIPatch is the part of an ACPI patch used here: its name and checked state.
type ACPIPatch struct {
	Name    string
	Checked bool
}

// Maestro holds the kext catalog and the selection state, like KextMaestro.
type Maestro struct {
	// Kexts is the catalog, in kext_data order. Index -1 stands for Python's None.
	Kexts []*datasets.KextInfo
	// OckFilesDir is the cache folder that holds downloaded kexts.
	OckFilesDir string
	// RequestInput answers an interactive prompt. Nil reads a line from stdin.
	RequestInput func(prompt string) (string, error)
	// WifiProfiles returns the stored Wi-Fi profiles as (ssid, password) pairs.
	// Nil makes WiFiConfig handling fail with ErrWifiProfilesNotPorted.
	WifiProfiles func() ([][2]string, error)

	matchingKeys []string
}

// New returns a Maestro with its own copy of the catalog.
func New(ockFilesDir string) *Maestro {
	return &Maestro{
		Kexts:        datasets.NewCatalog(),
		OckFilesDir:  ockFilesDir,
		matchingKeys: []string{"IOPCIMatch", "IONameMatch", "IOPCIPrimaryMatch", "idProduct", "idVendor", "HDAConfigDefault"},
	}
}

func (m *Maestro) input(prompt string) (string, error) {
	if m.RequestInput != nil {
		return m.RequestInput(prompt)
	}
	fmt.Fprint(os.Stdout, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("EOFError: EOF when reading a line")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// lookup mirrors kext_data.kext_index_by_name.get(name): -1 when absent.
func lookup(name string) int {
	if i, ok := datasets.KextIndexByName[name]; ok {
		return i
	}
	return -1
}

// kextAt returns the catalog entry at index, or an error where Python would raise
// (a None index is a TypeError in self.kexts[None]).
func (m *Maestro) kextAt(index int) (*datasets.KextInfo, error) {
	if index < 0 || index >= len(m.Kexts) {
		return nil, errors.New("TypeError: kexts index is None or out of range")
	}
	return m.Kexts[index], nil
}

// checkedByName mirrors self.kexts[kext_data.kext_index_by_name[name]].checked.
func (m *Maestro) checkedByName(name string) (bool, error) {
	k, err := m.kextAt(lookup(name))
	if err != nil {
		return false, err
	}
	return k.Checked, nil
}

// pySlice mirrors Python slicing of a string by code points, with clamping.
func pySlice(s string, start, end int) string {
	r := []rune(s)
	a, b := clampSlice(start, end, len(r))
	return string(r[a:b])
}

// clampSlice applies Python's rules for a[start:end] to a sequence of length n.
func clampSlice(start, end, n int) (int, int) {
	norm := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	a, b := norm(start), norm(end)
	if b < a {
		b = a
	}
	return a, b
}

// strSlice is a[start:end] for a Go slice, with Python clamping.
func strSlice(list []string, start, end int) []string {
	a, b := clampSlice(start, end, len(list))
	return list[a:b]
}

// zfill mirrors str.zfill(width).
func zfill(s string, width int) string {
	if len(s) >= width {
		return s
	}
	pad := strings.Repeat("0", width-len(s))
	if s != "" && (s[0] == '+' || s[0] == '-') {
		return s[:1] + pad + s[1:]
	}
	return pad + s
}

// intToHex mirrors utils.int_to_hex: format(number, '02X').
func intToHex(v any) (string, error) {
	switch x := v.(type) {
	case int64:
		if x < 0 {
			return fmt.Sprintf("-%02X", -x), nil
		}
		return fmt.Sprintf("%02X", x), nil
	case *big.Int:
		return fmt.Sprintf("%02X", x), nil
	case bool:
		if x {
			return "01", nil
		}
		return "00", nil
	}
	return "", fmt.Errorf("format %T with 'X' is not supported", v)
}

// pySplitext mirrors os.path.splitext for a base name: leading dots do not
// start an extension.
func pySplitext(base string) string {
	dot := strings.LastIndex(base, ".")
	if dot < 0 {
		return base
	}
	for i := 0; i < dot; i++ {
		if base[i] != '.' {
			return base[:dot]
		}
	}
	return base
}

// cmpVersion parses two darwin versions and compares them, evaluating the parses
// in the order Python does.
func cmpVersion(a, b string) (int, error) {
	va, err := macos.ParseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := macos.ParseVersion(b)
	if err != nil {
		return 0, err
	}
	return va.Compare(vb), nil
}

// IsKextCompatible mirrors is_kext_compatible: min <= target <= max. The
// maximum is parsed only when the lower bound holds, as in the chained comparison.
func (m *Maestro) IsKextCompatible(kext *datasets.KextInfo, target string) (bool, error) {
	minTarget, err := cmpVersion(kext.MinDarwinVersion, target)
	if err != nil {
		return false, err
	}
	if minTarget > 0 {
		return false, nil
	}
	targetMax, err := cmpVersion(target, kext.MaxDarwinVersion)
	if err != nil {
		return false, err
	}
	return targetMax <= 0, nil
}

// GetKextStatus mirrors get_kext_status.
func (m *Maestro) GetKextStatus(kext *datasets.KextInfo, target string) (string, error) {
	if kext.Required {
		return "required", nil
	}
	compatible, err := m.IsKextCompatible(kext, target)
	if err != nil {
		return "", err
	}
	if compatible {
		if kext.Checked {
			return "selected", nil
		}
		return "available", nil
	}
	if kext.Checked {
		return "force_loaded", nil
	}
	if kext.AllowForceLoad {
		return "force_available", nil
	}
	c, err := cmpVersion(target, kext.MaxDarwinVersion)
	if err != nil {
		return "", err
	}
	if c > 0 {
		return "not_needed", nil
	}
	return "requires_newer", nil
}

// CheckKext mirrors check_kext. checking tracks the kexts being resolved, so a
// dependency cycle counts as satisfied, as in the original. index -1 is None.
func (m *Maestro) CheckKext(index int, target string, forceLoad bool, checking map[int]bool) (bool, error) {
	if index < 0 || index >= len(m.Kexts) {
		return false, nil
	}
	if checking == nil {
		checking = map[int]bool{}
	}
	kext := m.Kexts[index]
	if kext.Checked {
		return true, nil
	}
	if checking[index] {
		return true, nil
	}
	compatible, err := m.IsKextCompatible(kext, target)
	if err != nil {
		return false, err
	}
	if !compatible && !(forceLoad && kext.AllowForceLoad) {
		return false, nil
	}

	checking[index] = true
	for _, requiredName := range kext.RequiresKexts {
		ok, err := m.CheckKext(lookup(requiredName), target, forceLoad, checking)
		if err != nil {
			delete(checking, index)
			return false, err
		}
		if !ok {
			delete(checking, index)
			return false, nil
		}
	}
	delete(checking, index)

	kext.Checked = true
	if kext.ConflictGroupID != "" {
		for otherIndex, other := range m.Kexts {
			if other.ConflictGroupID == kext.ConflictGroupID && other.Name != kext.Name {
				if _, err := m.UncheckKext(otherIndex); err != nil {
					return false, err
				}
			}
		}
	}
	return true, nil
}

// UncheckKext mirrors uncheck_kext.
func (m *Maestro) UncheckKext(index int) (bool, error) {
	if index < 0 || index >= len(m.Kexts) {
		return false, nil
	}
	kext := m.Kexts[index]
	if !kext.Checked {
		return true, nil
	}

	type dependent struct {
		index int
		kext  *datasets.KextInfo
	}
	var dependents []dependent
	for i, other := range m.Kexts {
		if other.Checked && containsString(other.RequiresKexts, kext.Name) {
			dependents = append(dependents, dependent{i, other})
		}
	}
	anyRequired := false
	for _, d := range dependents {
		if d.kext.Required {
			anyRequired = true
		}
	}
	if kext.Required || anyRequired {
		return false, nil
	}

	kext.Checked = false
	for _, d := range dependents {
		if _, err := m.UncheckKext(d.index); err != nil {
			return false, err
		}
	}

	for _, dependencyName := range kext.RequiresKexts {
		dependencyIndex := lookup(dependencyName)
		dependency, err := m.kextAt(dependencyIndex)
		if err != nil {
			return false, err
		}
		if dependency.Required {
			continue
		}
		stillNeeded := false
		for _, other := range m.Kexts {
			if other.Checked && containsString(other.RequiresKexts, dependencyName) {
				stillNeeded = true
				break
			}
		}
		if !stillNeeded {
			if _, err := m.UncheckKext(dependencyIndex); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// VerifyKextCompatibility mirrors verify_kext_compatibility. It returns the kexts
// the user agreed to force load. Kexts that are unsupported on the target and
// allow force loading are offered; the prompt repeats until yes or no is given.
func (m *Maestro) VerifyKextCompatibility(kextNames []string, target string) (map[string]bool, error) {
	type incompatible struct {
		name string
	}
	var found []incompatible
	seen := map[string]bool{}
	for _, name := range kextNames {
		if seen[name] {
			continue
		}
		seen[name] = true
		index := lookup(name)
		if index < 0 {
			continue
		}
		kext := m.Kexts[index]
		compatible, err := m.IsKextCompatible(kext, target)
		if err != nil {
			return nil, err
		}
		if !compatible && kext.AllowForceLoad {
			found = append(found, incompatible{kext.Name})
		}
	}
	if len(found) == 0 {
		return map[string]bool{}, nil
	}

	noun := "this kext"
	if len(found) > 1 {
		noun = "these kexts"
	}
	for {
		option, err := m.input(fmt.Sprintf("Do you want to force load %s on the unsupported macOS version? (yes/No): ", noun))
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(option) {
		case "yes":
			out := map[string]bool{}
			for _, f := range found {
				out[f.name] = true
			}
			return out, nil
		case "no":
			return map[string]bool{}, nil
		}
	}
}

// ExtractPCIIDs mirrors extract_pci_id: the vendor-device pairs that a kext
// matches, read from its Info.plist personalities.
func (m *Maestro) ExtractPCIIDs(kextPath string) ([]string, error) {
	if _, err := os.Stat(kextPath); err != nil {
		return []string{}, nil
	}
	data, err := fsutil.ReadFile(filepath.Join(kextPath, "Contents", "Info.plist"))
	if err != nil {
		return nil, err
	}
	bundle, err := pyrt.Get(data, "IOKitPersonalities")
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		bundle = plist.NewDict()
	}
	personalities, err := pyrt.Items(bundle)
	if err != nil {
		return nil, err
	}

	pciIDs := []string{}
	for _, personality := range personalities {
		properties := personality.Value
		var matching []string
		for _, key := range m.matchingKeys {
			in, err := pyrt.In(key, properties)
			if err != nil {
				return nil, err
			}
			if in {
				matching = append(matching, key)
			}
		}
		if len(matching) == 0 {
			continue
		}
		matchKey := matching[0]
		value, err := pyrt.Index(properties, matchKey)
		if err != nil {
			return nil, err
		}

		switch matchKey {
		case "IOPCIMatch", "IOPCIPrimaryMatch":
			text, err := pyrt.Str(value)
			if err != nil {
				return nil, err
			}
			for _, pciID := range strings.Split(text, " ") {
				vendor := pySlice(pciID, -4, len([]rune(pciID)))
				device := pySlice(pciID, 2, 6)
				pciIDs = append(pciIDs, strings.ToUpper(vendor+"-"+device))
			}
		case "IONameMatch":
			items, err := iterate(value)
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				pciID, err := pyrt.Str(item)
				if err != nil {
					return nil, err
				}
				vendor := pySlice(pciID, 3, 7)
				parts := strings.Split(pciID, ",")
				if len(parts) < 2 {
					return nil, errors.New("IndexError: list index out of range")
				}
				device := zfill(parts[1], 4)
				pciIDs = append(pciIDs, strings.ToUpper(vendor+"-"+device))
			}
		case "idProduct":
			idVendor, err := pyrt.Index(properties, "idVendor")
			if err != nil {
				return nil, err
			}
			vendorHex, err := intToHex(idVendor)
			if err != nil {
				return nil, err
			}
			idProduct, err := pyrt.Index(properties, "idProduct")
			if err != nil {
				return nil, err
			}
			deviceHex, err := intToHex(idProduct)
			if err != nil {
				return nil, err
			}
			pciIDs = append(pciIDs, strings.ToUpper(zfill(vendorHex, 4)+"-"+zfill(deviceHex, 4)))
		case "HDAConfigDefault":
			layouts, err := iterate(value)
			if err != nil {
				return nil, err
			}
			for _, layout := range layouts {
				codecID, err := pyrt.Get(layout, "CodecID")
				if err != nil {
					return nil, err
				}
				codecHex, err := intToHex(codecID)
				if err != nil {
					return nil, err
				}
				codec := zfill(codecHex, 8)
				pciIDs = append(pciIDs, codec[:4]+"-"+codec[len(codec)-4:])
			}
			seen := map[string]bool{}
			var unique []string
			for _, id := range pciIDs {
				if !seen[id] {
					seen[id] = true
					unique = append(unique, id)
				}
			}
			sort.Strings(unique)
			pciIDs = unique
		}
	}
	return pciIDs, nil
}

// iterate mirrors iterating a Python value in a for loop: a list yields its
// items, a str yields its characters, and a dict yields its keys.
func iterate(v any) ([]any, error) {
	switch x := v.(type) {
	case []any:
		return x, nil
	case string:
		var out []any
		for _, r := range x {
			out = append(out, string(r))
		}
		return out, nil
	case *plist.Dict:
		var out []any
		for _, k := range x.Keys() {
			out = append(out, k)
		}
		return out, nil
	}
	return nil, fmt.Errorf("TypeError: %T object is not iterable", v)
}

// IsIntelHEDTCPU mirrors is_intel_hedt_cpu.
func IsIntelHEDTCPU(processorName, cpuCodename any) (bool, error) {
	gen := datasets.IntelCPUGenerations
	if pyrt.InStrings(cpuCodename, strSlice(gen, 45, 66)) {
		codename, err := pyrt.Str(cpuCodename)
		if err != nil {
			return false, err
		}
		for _, suffix := range []string{"-X", "-P", "-W", "-E", "-EP", "-EX"} {
			if strings.HasSuffix(codename, suffix) {
				return true, nil
			}
		}
		return false, nil
	}
	if pyrt.InStrings(cpuCodename, strSlice(gen, 66, len(gen))) {
		in, err := pyrt.In("Xeon", processorName)
		if err != nil {
			return false, err
		}
		return in, nil
	}
	return false, nil
}
