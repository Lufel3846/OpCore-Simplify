package efi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/config"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/kextmaestro"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

var serialFixture = map[string]any{
	"MLB":                "C02TESTMLB000000",
	"ROM":                "A1B2C3D4E5F6",
	"SystemProductName":  "MacPro7,1",
	"SystemSerialNumber": "C02TESTSERIAL",
	"SystemUUID":         "00000000-0000-0000-0000-000000000000",
}

// writeFile creates path with content, making the folders.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeOpenCorePkg lays out the base the installer copies: an EFI folder with the
// config, drivers and tools that are used and not used, audio and picker
// resources, and the manifest.
func writeOpenCorePkg(t *testing.T, ock string, sample []byte) {
	t.Helper()
	base := filepath.Join(ock, "OpenCorePkg")
	writeFile(t, filepath.Join(base, "EFI", "BOOT", "BOOTX64.efi"), "boot")
	writeFile(t, filepath.Join(base, "EFI", "OC", "OpenCore.efi"), "oc")
	writeFile(t, filepath.Join(base, "EFI", "OC", "config.plist"), string(sample))
	for _, name := range []string{"HfsPlus.efi", "OpenRuntime.efi", "ResetNvramEntry.efi", "Unused.efi"} {
		writeFile(t, filepath.Join(base, "EFI", "OC", "Drivers", name), "driver "+name)
	}
	writeFile(t, filepath.Join(base, "EFI", "OC", "Drivers", "Folder.efi", "inside.txt"), "dir named like a driver")
	for _, name := range []string{"OpenShell.efi", "Unused.efi"} {
		writeFile(t, filepath.Join(base, "EFI", "OC", "Tools", name), "tool "+name)
	}
	writeFile(t, filepath.Join(base, "EFI", "OC", "Resources", "Audio", "a.wav"), "audio")
	writeFile(t, filepath.Join(base, "EFI", "OC", "Resources", "Image", "Acidanthera", "GoldenGate", "Left.icns"), "icns")
	writeFile(t, filepath.Join(base, "EFI", "OC", "Resources", "Image", "Other", "Right.icns"), "icns")
	writeFile(t, filepath.Join(base, "EFI", "OC", "Resources", "Image", "Plain", "Readme.txt"), "no icon")
	writeFile(t, filepath.Join(base, "manifest.json"), "{}")
	writeFile(t, filepath.Join(base, "Docs", "Sample.plist"), string(sample))
}

// writeKextCache lays out the kext bundles the installer reads from the cache.
func writeKextCache(t *testing.T, ock string) {
	t.Helper()
	bundles := map[string]struct{ exec, identifier string }{
		"Lilu":          {"Lilu", "as.vit9696.Lilu"},
		"VirtualSMC":    {"VirtualSMC", "as.vit9696.VirtualSMC"},
		"WhateverGreen": {"WhateverGreen", "as.vit9696.WhateverGreen"},
		"AppleALC":      {"AppleALC", "as.vit9696.AppleALC"},
		"VoodooTSCSync": {"VoodooTSCSync", "com.example.VoodooTSCSync"},
	}
	for product, b := range bundles {
		info := plist.NewDict()
		info.Set("CFBundleIdentifier", b.identifier)
		info.Set("CFBundleExecutable", b.exec)
		info.Set("CFBundleVersion", "1.0.0")
		libs := plist.NewDict()
		if product != "Lilu" {
			libs.Set("as.vit9696.Lilu", "1.6.0")
		}
		info.Set("OSBundleLibraries", libs)
		personalities := plist.NewDict()
		if product == "VoodooTSCSync" {
			match := plist.NewDict()
			match.Set("IOCPUNumber", int64(9))
			entry := plist.NewDict()
			entry.Set("IOPropertyMatch", match)
			entry.Set("IOClass", "VoodooTSCSync")
			personalities.Set("VoodooTSCSync", entry)
		}
		info.Set("IOKitPersonalities", personalities)
		data, err := plist.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		bundle := filepath.Join(ock, product, product+".kext")
		writeFile(t, filepath.Join(bundle, "Contents", "Info.plist"), string(data))
		writeFile(t, filepath.Join(bundle, "Contents", "MacOS", b.exec), "binary "+product)
	}
}

// treeSHA returns the sorted map of relative path to sha256 under root.
func treeSHA(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out
}

// buildReport is a desktop Coffee Lake system with an integrated Intel GPU.
func buildReport() map[string]any {
	return map[string]any{
		"Motherboard": map[string]any{"Name": "Z690 AORUS", "Platform": "Desktop", "Chipset": "Z690"},
		"CPU": map[string]any{
			"Manufacturer": "Intel", "Codename": "Coffee Lake", "Processor Name": "Intel Core i7-8750H",
			"Core Count": 6, "CPU Count": 1, "SIMD Features": []any{"AVX2"},
		},
		"BIOS": map[string]any{"Firmware Type": "UEFI"},
		"GPU": map[string]any{"GPU 0": map[string]any{
			"Device Type": "Integrated GPU", "Manufacturer": "Intel", "Codename": "Coffee Lake",
			"Device ID": "8086-3E92", "PCI Path": "PciRoot(0x0)/Pci(0x2,0x0)", "OCLP Compatibility": false,
		}},
		"Monitor":         map[string]any{"Display": map[string]any{"Connected GPU": "GPU 0", "Connector Type": "HDMI", "Resolution": "1920x1080"}},
		"Network":         map[string]any{},
		"System Devices":  map[string]any{},
		"USB Controllers": map[string]any{},
	}
}

type efiResult struct {
	OK    map[string]string `json:"ok"`
	Error string            `json:"error"`
}

// TestParityWithPythonBuildOpenCoreEFI runs the original OCPE.build_opencore_efi and
// the Go Builder on the same cache and compares the whole result folder.
func TestParityWithPythonBuildOpenCoreEFI(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	sample, err := os.ReadFile(filepath.Join(testdata, "Sample.plist"))
	if err != nil {
		t.Fatal(err)
	}

	ock := t.TempDir()
	writeOpenCorePkg(t, ock, sample)
	writeKextCache(t, ock)
	emptyOCK := t.TempDir()

	checked := []string{"WhateverGreen", "AppleALC", "VoodooTSCSync", "RestrictEvents"}
	scenarios := []struct {
		name string
		ock  string
		ok   bool
	}{
		{"full build", ock, true},
		{"missing OpenCorePkg", emptyOCK, false},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			report := buildReport()
			request := map[string]any{
				"report": report, "disabled": map[string]any{}, "smbios_model": "MacPro7,1",
				"macos": "25.0.0", "needs_oclp": false, "checked": checked,
			}
			input, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			pyResult := filepath.Join(t.TempDir(), "Results")
			cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_efi.py"), repo, testdata, sc.ock, pyResult)
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
			}
			var py efiResult
			if err := json.Unmarshal(out, &py); err != nil {
				t.Fatal(err)
			}

			m := kextmaestro.New(sc.ock)
			for _, name := range checked {
				m.Kexts[datasets.KextIndexByName[name]].Checked = true
			}
			for _, name := range []string{"Lilu", "VirtualSMC"} {
				m.Kexts[datasets.KextIndexByName[name]].Checked = true
			}
			prodigy := &config.Prodigy{
				RequestInput:  func(string) (string, error) { return "", nil },
				Choose:        func(int) int { return 0 },
				KernelPatches: func(string, string) []any { return kernelPatches(t, testdata) },
				GenerateSMBIOS: func(string) (map[string]any, error) {
					out := map[string]any{}
					for k, v := range serialFixture {
						out[k] = v
					}
					return out, nil
				},
				AMDVanillaURL:     "amd-url",
				AquantiaURL:       "aquantia-url",
				HyperThreadingURL: "ht-url",
			}
			goResult := filepath.Join(t.TempDir(), "Results")
			b := &Builder{
				OCKDir:          sc.ock,
				ResultDir:       goResult,
				Kexts:           m,
				Config:          prodigy,
				ACPIUnavailable: true,
			}
			reportValue := loadAsJSON(t, report)
			disabledValue := loadAsJSON(t, map[string]any{})
			goErr := b.Build(reportValue, disabledValue, "25.0.0", false, "MacPro7,1")

			if py.Error != "" {
				if goErr == nil {
					t.Fatalf("python raised %s; go succeeded", py.Error)
				}
				return
			}
			if goErr != nil {
				t.Fatalf("go error %v; python ok", goErr)
			}
			got := treeSHA(t, goResult)
			t.Logf("result files (%d): %v", len(got), sortedKeys(got))
			if !reflect.DeepEqual(got, py.OK) {
				t.Errorf("result folders differ\n go: %v\n py: %v", sortedKeys(got), sortedKeys(py.OK))
				for path := range py.OK {
					if got[path] != py.OK[path] {
						t.Errorf("  %s differs", path)
					}
				}
				for path := range got {
					if _, ok := py.OK[path]; !ok {
						t.Errorf("  %s only in go", path)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(goResult, "manifest.json")); !os.IsNotExist(err) {
				t.Error("manifest.json was not removed")
			}
		})
	}
}

// loadAsJSON converts a Go value to the representation the JSON loader produces.
func loadAsJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pyjson.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// kernelPatches reads the fixture patch list fresh on each call.
func kernelPatches(t *testing.T, testdata string) []any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdata, "patches.plist"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := plist.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	kernel, _ := root.(*plist.Dict).Get("Kernel")
	patch, _ := kernel.(*plist.Dict).Get("Patch")
	return patch.([]any)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
