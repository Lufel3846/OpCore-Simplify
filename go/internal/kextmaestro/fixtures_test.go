package kextmaestro

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// kextBundles describes the kext bundles used by the fixture tests. They cover
// the cases of the original's bundle handling: a bundle with no personalities,
// PCI and IO name and USB and HDA matching, a bundle with no executable, a
// Wi-Fi bundle with an empty WiFiConfig, a folder without Info.plist, and an
// unreadable Info.plist.
func kextBundles() map[string]func() *plist.Dict {
	personality := func(kv ...any) *plist.Dict {
		d := plist.NewDict()
		for i := 0; i < len(kv); i += 2 {
			d.Set(kv[i].(string), kv[i+1])
		}
		return d
	}
	return map[string]func() *plist.Dict{
		"Lilu.kext": func() *plist.Dict {
			d := plist.NewDict()
			d.Set("CFBundleIdentifier", "as.vit9696.Lilu")
			d.Set("CFBundleExecutable", "Lilu")
			d.Set("CFBundleVersion", "1.6.0")
			d.Set("OSBundleLibraries", personality("com.apple.kpi.libkern", "18.0.0"))
			d.Set("IOKitPersonalities", plist.NewDict())
			return d
		},
		"AppleALC.kext": func() *plist.Dict {
			d := plist.NewDict()
			d.Set("CFBundleIdentifier", "as.vit9696.AppleALC")
			d.Set("CFBundleExecutable", "AppleALC")
			d.Set("CFBundleVersion", "1.9.0")
			d.Set("OSBundleLibraries", personality("as.vit9696.Lilu", "1.6.0"))
			d.Set("IOKitPersonalities", personality(
				"AppleHDA", personality("IOPCIMatch", "8086 1234 1022 5678", "IOClass", "X"),
				"Pin", personality("IONameMatch", []any{"pci8086,1502", "pci8086,abcd,x"}),
				"UsbDev", personality("idVendor", int64(1452), "idProduct", int64(8208)),
				"Codec", personality("HDAConfigDefault", []any{
					personality("CodecID", int64(1234567)),
					personality("CodecID", int64(42)),
				}),
			))
			return d
		},
		"VoodooTSCSync.kext": func() *plist.Dict {
			d := plist.NewDict()
			d.Set("CFBundleIdentifier", "com.example.VoodooTSCSync")
			d.Set("CFBundleVersion", "1.0.0")
			d.Set("OSBundleLibraries", personality("as.vit9696.Lilu", "1.6.0"))
			d.Set("IOKitPersonalities", personality(
				"VoodooTSCSync", personality(
					"IOPropertyMatch", personality("IOCPUNumber", int64(9)),
					"IOClass", "VoodooTSCSync",
				),
			))
			return d
		},
		"itlwm.kext": func() *plist.Dict {
			d := plist.NewDict()
			d.Set("CFBundleIdentifier", "com.example.itlwm")
			d.Set("CFBundleExecutable", "itlwm")
			d.Set("IOKitPersonalities", personality(
				"itlwm", personality("WiFiConfig", plist.NewDict(), "IOClass", "itlwm"),
			))
			return d
		},
	}
}

// writeKextTree writes the fixture kexts under root. Broken.kext has no plist,
// and BadPlist.kext has an Info.plist that cannot be parsed.
func writeKextTree(t *testing.T, root string) {
	t.Helper()
	for name, build := range kextBundles() {
		bundle := filepath.Join(root, name)
		data, err := plist.Marshal(build())
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(bundle, "Contents", "Info.plist"), data)
		if exe, ok := build().Get("CFBundleExecutable"); ok {
			writeFile(t, filepath.Join(bundle, "Contents", "MacOS", exe.(string)), []byte("binary "+name))
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "Broken.kext"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "BadPlist.kext", "Contents", "Info.plist"), []byte("not a property list"))
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeCache lays out the downloaded cache the installer reads: one folder per
// product, holding the bundle.
func writeCache(t *testing.T, ock string) {
	t.Helper()
	for _, name := range []string{"Lilu.kext", "AppleALC.kext", "VoodooTSCSync.kext"} {
		product := name[:len(name)-len(".kext")]
		writeKextBundle(t, name, filepath.Join(ock, product, name))
	}
}

// writeKextBundle writes a fixture bundle to dest.
func writeKextBundle(t *testing.T, name, dest string) {
	t.Helper()
	build := kextBundles()[name]
	data, err := plist.Marshal(build())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dest, "Contents", "Info.plist"), data)
	if exe, ok := build().Get("CFBundleExecutable"); ok {
		writeFile(t, filepath.Join(dest, "Contents", "MacOS", exe.(string)), []byte("binary "+name))
	}
}

// fileSHA returns the sha256 of a file, or "" when it does not exist.
func fileSHA(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// treeSHA is the sorted map of relative path to sha256, matching the oracle's tree().
func treeSHA(t *testing.T, root string) *plist.Dict {
	t.Helper()
	files := map[string]string{}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = fileSHA(t, p)
		return nil
	})
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sortStrings(keys)
	d := plist.NewDict()
	for _, k := range keys {
		d.Set(k, files[k])
	}
	return d
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestParityBundleOperations compares extract_pci_id, process_kext,
// modify_kexts, install_kexts_to_efi and load_kexts on the same fixture bundles.
// Each side works on its own copy of the fixtures.
func TestParityBundleOperations(t *testing.T) {
	pyRoot := t.TempDir()
	goRoot := t.TempDir()
	for _, root := range []string{pyRoot, goRoot} {
		writeKextTree(t, filepath.Join(root, "kexts"))
		writeCache(t, filepath.Join(root, "ock"))
	}

	report := plist.NewDict()
	cpu := plist.NewDict()
	cpu.Set("Core Count", int64(6))
	report.Set("CPU", cpu)
	reportJSON, err := pyjson.Dump(report)
	if err != nil {
		t.Fatal(err)
	}
	reportValue, err := pyjson.Load([]byte(reportJSON))
	if err != nil {
		t.Fatal(err)
	}

	pyKexts := filepath.Join(pyRoot, "kexts")
	pyOck := filepath.Join(pyRoot, "ock")
	pyInstall := filepath.Join(pyRoot, "installed")
	reqs := []map[string]any{
		{"op": "pci", "path": filepath.Join(pyKexts, "AppleALC.kext")},
		{"op": "pci", "path": filepath.Join(pyKexts, "Lilu.kext")},
		{"op": "pci", "path": filepath.Join(pyKexts, "Broken.kext")},
		{"op": "pci", "path": filepath.Join(pyKexts, "BadPlist.kext")},
		{"op": "pci", "path": filepath.Join(pyKexts, "Missing.kext")},
		{"op": "process", "dir": pyKexts, "path": "AppleALC.kext"},
		{"op": "process", "dir": pyKexts, "path": "Lilu.kext"},
		{"op": "process", "dir": pyKexts, "path": "VoodooTSCSync.kext"},
		{"op": "process", "dir": pyKexts, "path": "Broken.kext"},
		{"op": "process", "dir": pyKexts, "path": "BadPlist.kext"},
		{"op": "modify", "path": filepath.Join(pyKexts, "VoodooTSCSync.kext", "Contents", "Info.plist"), "report": reportValue, "macos": "25.0.0"},
		{"op": "modify", "path": filepath.Join(pyKexts, "VoodooTSCSync.kext", "Contents", "Info.plist"), "report": reportValue, "macos": "19.0.0"},
		{"op": "modify", "path": filepath.Join(pyKexts, "itlwm.kext", "Contents", "Info.plist"), "report": reportValue, "macos": "25.0.0"},
		{"op": "modify", "path": filepath.Join(pyKexts, "Missing.kext", "Contents", "Info.plist"), "report": reportValue, "macos": "25.0.0"},
		{"op": "reset"},
		{"op": "set_checked", "names": []string{"Lilu", "AppleALC", "VoodooTSCSync"}},
		{"op": "install", "macos": "25.0.0", "dir": pyInstall, "ock": pyOck},
		{"op": "load", "report": reportValue, "macos": "25.0.0", "dir": pyInstall},
	}
	py := runKextOracle(t, reqs)

	goKexts := filepath.Join(goRoot, "kexts")
	goOck := filepath.Join(goRoot, "ock")
	goInstall := filepath.Join(goRoot, "installed")
	m := New(goOck)
	m.RequestInput = answer

	check := func(i int, label, got string, err error) {
		t.Helper()
		if py[i].Error != "" {
			if err == nil {
				t.Errorf("%s: python raised %s; go returned %s", label, py[i].Error, got)
			}
			return
		}
		if err != nil {
			t.Errorf("%s: go error %v; python %s", label, err, *py[i].OK)
			return
		}
		if got != *py[i].OK {
			t.Errorf("%s:\n go: %s\n py: %s", label, got, *py[i].OK)
		}
	}

	pciTargets := []string{"AppleALC.kext", "Lilu.kext", "Broken.kext", "BadPlist.kext", "Missing.kext"}
	for i, name := range pciTargets {
		ids, err := m.ExtractPCIIDs(filepath.Join(goKexts, name))
		check(i, "pci "+name, dumpText(t, stringsAsAny(ids)), err)
	}

	processTargets := []string{"AppleALC.kext", "Lilu.kext", "VoodooTSCSync.kext", "Broken.kext", "BadPlist.kext"}
	for i, name := range processTargets {
		entry, err := m.ProcessKext(goKexts, name)
		text := "null"
		if entry != nil {
			text = dumpText(t, entry)
		}
		check(len(pciTargets)+i, "process "+name, text, err)
	}

	modifyBase := len(pciTargets) + len(processTargets)
	modifies := []struct{ rel, macos string }{
		{filepath.Join("VoodooTSCSync.kext", "Contents", "Info.plist"), "25.0.0"},
		{filepath.Join("VoodooTSCSync.kext", "Contents", "Info.plist"), "19.0.0"},
		{filepath.Join("itlwm.kext", "Contents", "Info.plist"), "25.0.0"},
		{filepath.Join("Missing.kext", "Contents", "Info.plist"), "25.0.0"},
	}
	for i, mod := range modifies {
		path := filepath.Join(goKexts, mod.rel)
		err := m.ModifyKexts(path, reportValue, mod.macos)
		if _, statErr := os.Stat(path); err == nil && statErr != nil {
			// The oracle opens the file after modifying it, so a missing file fails there.
			err = statErr
		}
		check(modifyBase+i, "modify "+mod.rel+" "+mod.macos, dumpText(t, fileSHA(t, path)), err)
	}

	resetBase := modifyBase + len(modifies)
	m.Kexts = datasets.NewCatalog()
	check(resetBase, "reset", "null", nil)
	for _, name := range []string{"Lilu", "AppleALC", "VoodooTSCSync"} {
		m.Kexts[lookup(name)].Checked = true
	}
	check(resetBase+1, "set_checked", dumpText(t, stringsAsAny(checkedNames(m))), nil)

	installIdx := resetBase + 2
	err = m.InstallKextsToEFI("25.0.0", goInstall)
	check(installIdx, "install tree", dumpText(t, treeSHA(t, goInstall)), err)

	loaded, err := m.LoadKexts(reportValue, "25.0.0", goInstall)
	var loadedAny []any
	for _, entry := range loaded {
		loadedAny = append(loadedAny, entry)
	}
	check(installIdx+1, "load", dumpText(t, loadedAny), err)

}

// checkedNames returns the sorted names of the checked kexts, as the oracle does.
func checkedNames(m *Maestro) []string {
	var out []string
	for _, k := range m.Kexts {
		if k.Checked {
			out = append(out, k.Name)
		}
	}
	sortStrings(out)
	return out
}
