package kextmaestro

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"os"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// InstallKextsToEFI mirrors install_kexts_to_efi. For every checked kext it finds
// the bundle in the cache and copies it to kextsDir. Failures are ignored, except
// for the two kexts whose bundle is mandatory (the laobamac fork and SimpleHDA).
func (m *Maestro) InstallKextsToEFI(macosVersion, kextsDir string) error {
	for _, kext := range m.Kexts {
		if !kext.Checked {
			continue
		}
		err := m.installKext(kext, macosVersion, kextsDir)
		if err != nil {
			if kext.Name == "AirportItlwm-laobamac" || kext.Name == "SimpleHDA" {
				return err
			}
		}
	}
	return nil
}

func (m *Maestro) installKext(kext *datasets.KextInfo, macosVersion, kextsDir string) error {
	kextName := kext.Name
	if kextName == "Feixiao" {
		kextName = "rtw88"
	}
	searchDirectory := m.OckFilesDir
	if kext.Name == "AirportItlwm-laobamac" {
		searchDirectory = filepath.Join(m.OckFilesDir, kext.Name+macosVersion[:min(2, len(macosVersion))])
		kextName = "AirportItlwm"
	}
	matches, err := fsutil.FindMatchingPaths(searchDirectory, ".kext", kextName, "")
	if err != nil {
		return err
	}

	var source, destination string
	for _, match := range matches {
		rel := strings.ReplaceAll(match.Path, "\\", "/")
		if kext.Name == "AirportItlwm" {
			version, err := m.airportVersion(macosVersion)
			if err != nil {
				return err
			}
			if strings.Split(rel, "/")[0] == "AirportItlwm"+version && path.Base(rel) == "AirportItlwm.kext" {
				source = filepath.Join(m.OckFilesDir, match.Path)
				destination = filepath.Join(kextsDir, filepath.Base(match.Path))
				break
			}
			continue
		}

		mainKext := strings.Split(rel, "/")[0]
		mainIndex := lookup(mainKext)
		// Python: "if not main_kext_index or self.kexts[main_kext_index].checked".
		// Index 0 (Lilu) is falsy there, so it takes the first branch.
		proceed := mainIndex <= 0
		if !proceed {
			proceed = m.Kexts[mainIndex].Checked
		}
		if proceed && pySplitext(path.Base(rel)) == kextName {
			source = filepath.Join(searchDirectory, match.Path)
			destination = filepath.Join(kextsDir, filepath.Base(match.Path))
		}
	}

	if (kext.Name == "AirportItlwm-laobamac" || kext.Name == "SimpleHDA") && source == "" {
		return errors.New("FileNotFoundError: downloaded bundle not found for " + kext.Name)
	}
	if source == "" {
		// Python passes None to os.path.exists, which raises TypeError. The caller
		// ignores that for every kext except the two above.
		return errors.New("TypeError: source path is None")
	}
	if _, err := os.Stat(source); err != nil {
		return nil
	}
	return fsutil.CopyTree(source, destination)
}

// airportVersion mirrors the version suffix of AirportItlwm in install_kexts_to_efi.
func (m *Maestro) airportVersion(macosVersion string) (string, error) {
	skywalk, err := m.checkedByName("IOSkywalkFamily")
	if err != nil {
		return "", err
	}
	legacy, err := m.checkedByName("IO80211FamilyLegacy")
	if err != nil {
		return "", err
	}
	version := macosVersion[:min(2, len(macosVersion))]
	atLeast24, err := darwinAtLeast(macosVersion, "24.0.0")
	if err != nil {
		return "", err
	}
	if (skywalk && legacy) || atLeast24 {
		return "22", nil
	}
	atLeast234, err := darwinAtLeast(macosVersion, "23.4.0")
	if err != nil {
		return "", err
	}
	if atLeast234 {
		return "23.4", nil
	}
	atLeast230, err := darwinAtLeast(macosVersion, "23.0.0")
	if err != nil {
		return "", err
	}
	if atLeast230 {
		return "23.0", nil
	}
	return version, nil
}

// ProcessKext mirrors process_kext. It reads the bundle's Info.plist and returns
// the Kernel > Add entry for it, or nil when the bundle cannot be read (the
// original's bare except returns None in that case).
func (m *Maestro) ProcessKext(kextsDirectory, kextPath string) (*plist.Dict, error) {
	matches, err := fsutil.FindMatchingPaths(filepath.Join(kextsDirectory, kextPath), ".plist", "Info", "")
	if err != nil || len(matches) == 0 {
		return nil, nil
	}
	plistPath := matches[0].Path
	raw, err := fsutil.ReadFile(filepath.Join(kextsDirectory, kextPath, plistPath))
	if err != nil {
		return nil, nil
	}
	bundle, ok := raw.(*plist.Dict)
	if !ok {
		return nil, nil
	}

	execValue, err := pyrt.GetDefault(bundle, "CFBundleExecutable", "None")
	if err != nil {
		return nil, err
	}
	execName, err := pyrt.Str(execValue)
	if err != nil {
		return nil, err
	}
	executablePath := filepath.Join("Contents", "MacOS", execName)
	if _, err := os.Stat(filepath.Join(kextsDirectory, kextPath, executablePath)); err != nil {
		executablePath = ""
	}

	librariesValue, err := pyrt.GetDefault(bundle, "OSBundleLibraries", plist.NewDict())
	if err != nil {
		return nil, err
	}
	libraries := plist.NewDict()
	pairs, err := pyrt.Items(librariesValue)
	if err != nil {
		return nil, err
	}
	for _, p := range pairs {
		libraries.Set(p.Key, p.Value)
	}
	switch execName {
	case "AppleALC":
		libraries.Set("org.ChefKiss.NootedRed", "0.8.10")
	case "SimpleHDA":
		libraries.Set("as.vit9696.AppleALC", "1.9.9")
	}

	identifier, _ := bundle.Get("CFBundleIdentifier")
	version, _ := bundle.Get("CFBundleVersion")
	entry := plist.NewDict()
	entry.Set("BundlePath", strings.TrimLeft(strings.ReplaceAll(kextPath, "\\", "/"), "/"))
	entry.Set("Enabled", true)
	entry.Set("ExecutablePath", strings.TrimLeft(strings.ReplaceAll(executablePath, "\\", "/"), "/"))
	entry.Set("PlistPath", strings.TrimLeft(strings.ReplaceAll(plistPath, "\\", "/"), "/"))
	entry.Set("BundleIdentifier", identifier)
	entry.Set("BundleVersion", version)
	entry.Set("BundleLibraries", libraries)
	return entry, nil
}

// ModifyKexts mirrors modify_kexts. It adjusts the Wi-Fi, TSC synchronization
// and CPU number settings of a bundle and rewrites its Info.plist. Like the
// original, a failure to read or change the structure leaves the file untouched.
func (m *Maestro) ModifyKexts(plistPath string, report any, macosVersion string) error {
	raw, err := fsutil.ReadFile(plistPath)
	if err != nil || raw == nil {
		return nil
	}
	bundle, ok := raw.(*plist.Dict)
	if !ok {
		return nil
	}

	personalities, err := pyrt.Get(bundle, "IOKitPersonalities")
	if err != nil || personalities == nil {
		return nil
	}
	itlwm, err := pyrt.Get(personalities, "itlwm")
	if err != nil {
		return nil
	}
	wifiConfig, err := pyrt.Get(itlwm, "WiFiConfig")
	if err != nil {
		return nil
	}

	switch {
	case pyrt.Truthy(wifiConfig):
		if m.WifiProfiles == nil {
			return ErrWifiProfilesNotPorted
		}
		profiles, err := m.WifiProfiles()
		if err != nil {
			// The original's bare except returns without writing the file.
			return nil
		}
		if len(profiles) > 0 {
			configs := plist.NewDict()
			for i, p := range profiles {
				entry := plist.NewDict()
				entry.Set("password", p[1])
				entry.Set("ssid", p[0])
				configs.Set(fmt.Sprintf("WiFi_%d", i+1), entry)
			}
			itlwm.(*plist.Dict).Set("WiFiConfig", configs)
		}
	case personalityEnabled(personalities, "VoodooTSCSync"):
		if !setCPUNumber(personalities, "VoodooTSCSync", report, macosVersion) {
			return nil
		}
	case personalityEnabled(personalities, "AmdTscSync"):
		if !setCPUNumber(personalities, "AmdTscSync", report, macosVersion) {
			return nil
		}
	default:
		return nil
	}
	return fsutil.WriteFile(plistPath, bundle)
}

// personalityEnabled reports whether a personality is present and truthy.
func personalityEnabled(personalities any, name string) bool {
	v, err := pyrt.Get(personalities, name)
	return err == nil && pyrt.Truthy(v)
}

// setCPUNumber mirrors the IOCPUNumber assignment. It returns false where the
// original's bare except would stop without writing the file.
func setCPUNumber(personalities any, name string, report any, macosVersion string) bool {
	entry, err := pyrt.Index(personalities, name)
	if err != nil {
		return false
	}
	match, err := pyrt.Index(entry, "IOPropertyMatch")
	if err != nil {
		return false
	}
	atLeast21, err := darwinAtLeast(macosVersion, "21.0.0")
	if err != nil {
		return false
	}
	var value int64
	if atLeast21 {
		value = 0
	} else {
		cpu, err := pyrt.Index(report, "CPU")
		if err != nil {
			return false
		}
		coreObj, err := pyrt.Index(cpu, "Core Count")
		if err != nil {
			return false
		}
		cores, err := pyrt.IntValue(coreObj)
		if err != nil {
			return false
		}
		value = cores - 1
	}
	dict, ok := match.(*plist.Dict)
	if !ok {
		return false
	}
	dict.Set("IOCPUNumber", value)
	return true
}

// LoadKexts mirrors load_kexts. It processes every bundle in kextsDir, orders
// them so that dependencies come first, sets the kernel version limits, disables
// duplicates, and returns the Kernel > Add entries.
func (m *Maestro) LoadKexts(report any, macosVersion, kextsDirectory string) ([]*plist.Dict, error) {
	var unload []string
	ioElCap, err := m.checkedByName("IO80211ElCap")
	if err != nil {
		return nil, err
	}
	voodooSMBus, err := m.checkedByName("VoodooSMBus")
	if err != nil {
		return nil, err
	}
	voodooRMI, err := m.checkedByName("VoodooRMI")
	if err != nil {
		return nil, err
	}
	if ioElCap {
		unload = append(unload, "AirPortBrcm4331", "AppleAirPortBrcm43224")
	} else if voodooSMBus {
		unload = append(unload, "VoodooPS2Mouse")
	} else if voodooRMI {
		voodooI2C, err := m.checkedByName("VoodooI2C")
		if err != nil {
			return nil, err
		}
		if !voodooI2C {
			unload = append(unload, "RMII2C")
		} else {
			unload = append(unload, "VoodooSMBus", "RMISMBus", "VoodooI2CHID")
		}
	}

	paths, err := fsutil.FindMatchingPaths(kextsDirectory, ".kext", "", "")
	if err != nil {
		return nil, err
	}
	var bundleList []*plist.Dict
	for _, p := range paths {
		info, err := m.ProcessKext(kextsDirectory, p.Path)
		if err != nil {
			return nil, err
		}
		if info == nil {
			continue
		}
		err = m.ModifyKexts(filepath.Join(kextsDirectory, p.Path, mustString(info, "PlistPath")), report, macosVersion)
		if err != nil {
			return nil, err
		}
		bundleList = append(bundleList, info)
	}

	bundleDict := map[string]*plist.Dict{}
	for _, b := range bundleList {
		id, _ := b.Get("BundleIdentifier")
		if s, ok := id.(string); ok {
			bundleDict[s] = b
		}
	}

	latestBeta := macos.LatestDarwinVersion(true)
	latestFinal := macos.LatestDarwinVersion(false)
	lowest := macos.LowestDarwinVersion()

	var sorted []*plist.Dict
	visited := map[visitKey]bool{}
	seen := map[identKey]bool{}

	var visit func(bundle *plist.Dict) error
	visit = func(bundle *plist.Dict) error {
		bundlePath := mustString(bundle, "BundlePath")
		identifierValue, _ := bundle.Get("BundleIdentifier")
		kextName := pySplitext(path.Base(bundlePath))
		if kextName == "rtw88" {
			kextName = "Feixiao"
		} else if kextName == "AirportItlwm" {
			checked, err := m.checkedByName("AirportItlwm-laobamac")
			if err != nil {
				return err
			}
			if checked {
				kextName = "AirportItlwm-laobamac"
			}
		}
		key := visitKey{path: bundlePath, ident: identKeyOf(identifierValue)}
		if containsString(unload, kextName) || visited[key] {
			return nil
		}

		bundle.Set("MaxKernel", latestBeta)
		bundle.Set("MinKernel", lowest)

		if index := lookup(kextName); index > 0 {
			k := m.Kexts[index]
			bundle.Set("MaxKernel", k.MaxDarwinVersion)
			bundle.Set("MinKernel", k.MinDarwinVersion)
		}

		libsValue, _ := bundle.Get("BundleLibraries")
		libs, _ := libsValue.(*plist.Dict)
		if libs != nil {
			for _, dep := range libs.Keys() {
				depBundle, ok := bundleDict[dep]
				if !ok {
					continue
				}
				if err := visit(depBundle); err != nil {
					return err
				}
				// bundle["MaxKernel"] = own if own < dep else dep["MaxKernel"]
				bundleMax := mustString(bundle, "MaxKernel")
				depMax := mustStringDefault(depBundle, "MaxKernel", "99.99.99")
				c, err := cmpVersion(bundleMax, depMax)
				if err != nil {
					return err
				}
				if c >= 0 {
					bundle.Set("MaxKernel", mustString(depBundle, "MaxKernel"))
				}
				// bundle["MinKernel"] = own if own > dep else dep["MinKernel"]
				bundleMin := mustString(bundle, "MinKernel")
				depMin := mustStringDefault(depBundle, "MinKernel", "0.0.0")
				c, err = cmpVersion(bundleMin, depMin)
				if err != nil {
					return err
				}
				if c <= 0 {
					bundle.Set("MinKernel", mustString(depBundle, "MinKernel"))
				}
			}
		}

		identifier, _ := identifierStr(identifierValue)
		if identifier == "com.apple.driver.AirPort.BrcmNIC" || identifier == "com.apple.driver.AirPort.Atheros40" {
			if strings.HasSuffix(bundlePath, "-Tahoe.kext") {
				bundle.Set("MinKernel", "25.0.0")
			} else {
				bundle.Set("MaxKernel", "24.99.99")
				if identifier == "com.apple.driver.AirPort.BrcmNIC" {
					bundle.Set("MinKernel", "23.0.0")
				} else {
					bundle.Set("MinKernel", "18.0.0")
				}
			}
		}

		if kextName == "AirPortBrcm4360_Injector" {
			bundle.Set("MaxKernel", "19.99.99")
		} else if kextName == "AirportItlwm" || kextName == "AirportItlwm-laobamac" {
			maxKernel := mustString(bundle, "MaxKernel")
			bundle.Set("MaxKernel", macosVersion[:min(2, len(macosVersion))]+maxKernel[min(2, len(maxKernel)):])
			if kextName == "AirportItlwm" || strings.HasPrefix(macosVersion, "25.") {
				bundle.Set("MinKernel", macosVersion[:min(2, len(macosVersion))]+".0.0")
			}
		}

		visited[key] = true

		idKey := identKeyOf(identifierValue)
		if identifier == "com.apple.driver.AirPort.BrcmNIC" || identifier == "com.apple.driver.AirPort.Atheros40" {
			idKey = identKey{kind: 2, s: identifier, min: mustString(bundle, "MinKernel"), max: mustString(bundle, "MaxKernel")}
		}
		if seen[idKey] {
			bundle.Set("Enabled", false)
		} else {
			seen[idKey] = true
		}
		sorted = append(sorted, bundle)
		return nil
	}

	for _, b := range bundleList {
		if err := visit(b); err != nil {
			return nil, err
		}
	}

	var out []*plist.Dict
	for _, b := range sorted {
		maxKernel := mustString(b, "MaxKernel")
		minKernel := mustString(b, "MinKernel")
		if maxKernel == latestBeta || maxKernel == latestFinal {
			maxKernel = ""
		}
		if minKernel == lowest {
			minKernel = ""
		}
		enabled, _ := b.Get("Enabled")
		execPath, _ := b.Get("ExecutablePath")
		bundlePath, _ := b.Get("BundlePath")
		plistPath, _ := b.Get("PlistPath")
		entry := plist.NewDict()
		entry.Set("Arch", "Any")
		entry.Set("BundlePath", bundlePath)
		entry.Set("Comment", "")
		entry.Set("Enabled", enabled)
		entry.Set("ExecutablePath", execPath)
		entry.Set("MaxKernel", maxKernel)
		entry.Set("MinKernel", minKernel)
		entry.Set("PlistPath", plistPath)
		out = append(out, entry)
	}
	return out, nil
}

// visitKey and identKey are hashable forms of the tuples the original keeps in
// its visited and seen sets.
type visitKey struct {
	path  string
	ident identKey
}

type identKey struct {
	kind int // 0 none, 1 string, 2 tuple (identifier, min, max)
	s    string
	min  string
	max  string
}

func identKeyOf(v any) identKey {
	s, ok := v.(string)
	if !ok {
		return identKey{kind: 0}
	}
	return identKey{kind: 1, s: s}
}

// identifierStr returns the bundle identifier as text when it is a string.
func identifierStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// mustString reads a string field that the original always sets.
func mustString(d *plist.Dict, key string) string {
	v, _ := d.Get(key)
	s, _ := v.(string)
	return s
}

func mustStringDefault(d *plist.Dict, key, def string) string {
	v, found := d.Get(key)
	if !found {
		return def
	}
	s, _ := v.(string)
	return s
}

// ProcessKextAccepted adapts ProcessKext to the gathering step: the bundle is
// moved into the cache only when the original's process_kext returns a value
// (it returns None for bundles it cannot read).
func (m *Maestro) ProcessKextAccepted(kextsDirectory, kextPath string) (bool, error) {
	entry, err := m.ProcessKext(kextsDirectory, kextPath)
	return entry != nil, err
}
