package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// serialFixture is the SMBIOS output used by both sides (pyref_config.py SERIAL).
var serialFixture = map[string]any{
	"MLB":                "C02TESTMLB000000",
	"ROM":                "A1B2C3D4E5F6",
	"SystemProductName":  "MacPro7,1",
	"SystemSerialNumber": "C02TESTSERIAL",
	"SystemUUID":         "00000000-0000-0000-0000-000000000000",
}

// fixtureProfile returns the kernel patch list of testdata/patches.plist, read
// fresh on every call because genarate modifies the entries.
func fixtureProfile(t *testing.T, testdata string) []any {
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

// reportCase is one generated input shared by both implementations.
type reportCase struct {
	Report     map[string]any
	Disabled   map[string]any
	Model      string
	Macos      string
	NeedsOCLP  bool
	CheckedKex []string
}

var kextChoices = []string{
	"WhateverGreen", "AppleALC", "RestrictEvents", "BlueToolFixup", "IOSkywalkFamily",
	"CpuTopologyRebuild", "ForgedInvariant", "CatalinaBCM5701Ethernet", "AirportItlwm",
	"IO80211FamilyLegacy", "VoodooI2C", "CpuTscSync", "AirportItlwm-laobamac", "NootedRed",
}

// generateCases builds deterministic hardware reports. The pools come from the
// generated datasets, so the inputs stay aligned with the Python data.
func generateCases(seed int64, count int) []reportCase {
	r := rand.New(rand.NewSource(seed))
	pick := func(xs []string) string { return xs[r.Intn(len(xs))] }
	maybe := func(p float64) bool { return r.Float64() < p }

	boards := []string{"Z690 AORUS", "GOOGLE Pixelbook", "ASUS ROG", "HP Pavilion", "Surface Laptop", "B550 Board"}
	platforms := []string{"Desktop", "Laptop", "NUC"}
	intelChipsets := append(append([]string{}, datasets.IntelChipsets...), "B650", "X670")
	amdChipsets := append(append([]string{}, datasets.AMDChipsets...), "TRX40")
	codenames := append([]string{"Tiger Lake", "Haswell", "Broadwell", "Sandy Bridge", "Ivy Bridge", "Coffee Lake", "Raphael", "Storm Peak", "Unknown"}, datasets.IntelCPUGenerations...)
	processors := []string{"Intel Core i7-8750H", "Intel Core i9-9900K", "Intel Xeon W-3245", "Intel Core i7-8559U", "Intel Celeron G3900", "Intel Pentium G4560", "AMD Ryzen 9 5900X", "Intel Core i5-7300U"}
	integratedIDs := []string{"8086-3E92", "8086-9B41", "8086-5912", "8086-1916", "8086-0412", "8086-0A26", "8086-1606", "8086-0102", "8086-0166", "8086-8A52", "8086-0BD1", "8086-0042", "8086-0046", "8086-1234"}
	discreteIDs := []string{"1002-6610", "1002-682B", "1002-67B0", "10DE-1234"}
	for k := range datasets.SpoofGPUIDs {
		discreteIDs = append(discreteIDs, datasets.SpoofGPUIDs[k].Key)
	}
	gpuCodenames := []string{"Navi 21", "Navi 22", "Navi 23", "Kepler", "Polaris", "Ice Lake", "Sandy Bridge", "Ivy Bridge", "Haswell", "Skylake", "Kaby Lake", "Broadwell", "Coffee Lake", "Tiger Lake"}
	pciPaths := []string{"PciRoot(0x0)/Pci(0x2,0x0)", "PciRoot(0x0)/Pci(0x1,0x0)/Pci(0x0,0x0)", "PciRoot(0x0)/Pci(0x1c,0x0)"}
	wifiIDs := []string{"168C-002A", "168C-0030", "14E4-43A0", "14E4-4311", "14E4-4312", "14E4-43BA", "14E4-16B4", "1D6A-91B1", "8086-0060", "8086-0082", "8086-0100", "8086-0030", "1234-5678"}
	codecIDs := []string{"10EC-0295", "10EC-0298", "1234-5678"}

	cases := make([]reportCase, 0, count)
	for i := 0; i < count; i++ {
		platform := pick(platforms)
		board := map[string]any{"Name": pick(boards), "Platform": platform, "Chipset": ""}
		cpuMfr := pick([]string{"Intel", "Intel", "AMD"})
		chipset := pick(intelChipsets)
		if cpuMfr == "AMD" {
			chipset = pick(amdChipsets)
		}
		board["Chipset"] = chipset
		if board["Name"] == "GOOGLE Pixelbook" && maybe(0.5) {
			board["Name"] = "HP Pavilion"
		}
		cpu := map[string]any{
			"Manufacturer":   cpuMfr,
			"Codename":       pick(codenames),
			"Processor Name": pick(processors),
			"Core Count":     []int{2, 4, 6, 8, 10, 16}[r.Intn(6)],
			"CPU Count":      1,
			"SIMD Features":  []any{"AVX2"},
		}
		report := map[string]any{
			"Motherboard": board,
			"CPU":         cpu,
			"BIOS":        map[string]any{"Firmware Type": pick([]string{"UEFI", "UEFI", "Legacy", "Unknown"})},
		}

		gpus := map[string]any{}
		for g := 0; g < 1+r.Intn(2); g++ {
			name := fmt.Sprintf("GPU %d", g)
			if maybe(0.6) {
				gpus[name] = map[string]any{
					"Device Type":        "Integrated GPU",
					"Manufacturer":       pick([]string{"Intel", "AMD"}),
					"Codename":           pick(gpuCodenames),
					"Device ID":          pick(integratedIDs),
					"PCI Path":           pick(pciPaths),
					"OCLP Compatibility": maybe(0.3),
					"Resizable BAR":      pick([]string{"Enabled", "Disabled"}),
				}
			} else {
				gpus[name] = map[string]any{
					"Device Type":        "Discrete GPU",
					"Manufacturer":       pick([]string{"AMD", "NVIDIA", "Intel"}),
					"Codename":           pick(gpuCodenames),
					"Device ID":          pick(discreteIDs),
					"PCI Path":           pick(pciPaths),
					"OCLP Compatibility": maybe(0.4),
					"Resizable BAR":      pick([]string{"Enabled", "Disabled"}),
				}
			}
		}
		report["GPU"] = gpus

		if maybe(0.7) {
			monitors := map[string]any{}
			for m := 0; m < 1+r.Intn(2); m++ {
				monitors[fmt.Sprintf("Monitor %d", m)] = map[string]any{
					"Connected GPU":  pick([]string{"GPU 0", "GPU 1", "GPU 9"}),
					"Connector Type": pick([]string{"HDMI", "DVI", "VGA", "DisplayPort"}),
					"Resolution":     pick([]string{"1920x1080", "3840x2160", "1600x900", "1366x768", "2560x1440"}),
				}
			}
			report["Monitor"] = monitors
		}
		if maybe(0.8) {
			network := map[string]any{}
			for n := 0; n < 1+r.Intn(2); n++ {
				entry := map[string]any{"Device ID": pick(wifiIDs), "PCI Path": pick(pciPaths)}
				if maybe(0.5) {
					entry["ACPI Path"] = "_SB.PCI0.RP01"
				}
				network[fmt.Sprintf("Network %d", n)] = entry
			}
			report["Network"] = network
		}
		if maybe(0.6) {
			storage := map[string]any{}
			for s := 0; s < 1+r.Intn(2); s++ {
				entry := map[string]any{"PCI Path": pick(pciPaths)}
				if maybe(0.5) {
					entry["ACPI Path"] = "_SB.PCI0.SAT0"
				}
				storage[fmt.Sprintf("Storage %d", s)] = entry
			}
			report["Storage Controllers"] = storage
		}
		if maybe(0.6) {
			devices := map[string]any{}
			type sysDev struct{ id, sub string }
			options := []sysDev{
				{"1234-0001", ""},
				{"8086-1C3A", ""}, {"8086-1E3A", ""},
				{"144D-A808", "A811144D"}, {"1022-790E", "15101022"},
				{"8086-1E55", "21F317AA"}, {"8086-1E58", "500C17AA"},
			}
			for d := 0; d < 1+r.Intn(3); d++ {
				opt := options[r.Intn(len(options))]
				entry := map[string]any{"Device ID": opt.id, "PCI Path": pick(pciPaths), "Bus Type": "PCI"}
				if opt.sub != "" {
					entry["Subsystem ID"] = opt.sub
				}
				devices[fmt.Sprintf("Device %d", d)] = entry
			}
			report["System Devices"] = devices
		}
		if maybe(0.5) {
			sound := map[string]any{"HDA": map[string]any{"Device ID": pick(codecIDs)}}
			if maybe(0.5) {
				sound["HDA"].(map[string]any)["Controller Device ID"] = "8086-1C3A"
			}
			report["Sound"] = sound
		}

		disabled := map[string]any{}
		if maybe(0.3) {
			disabled["GPU 1"] = map[string]any{"PCI Path": pick(pciPaths), "Disabled": maybe(0.5)}
		}

		checked := []string{}
		for _, name := range kextChoices {
			if maybe(0.4) {
				checked = append(checked, name)
			}
		}
		macos := pick([]string{"19.0.0", "20.0.0", "21.4.0", "22.0.0", "22.5.0", "23.0.0", "24.0.0", "25.0.0", "bad"})
		cases = append(cases, reportCase{
			Report: report, Disabled: disabled, Model: pick([]string{"MacPro7,1", "iMacPro1,1", "MacBookPro16,2"}),
			Macos: macos, NeedsOCLP: maybe(0.2), CheckedKex: checked,
		})
	}
	return cases
}

// TestParityWithPythonGenarate compares the complete config.plist produced by
// ConfigProdigy.genarate. The result is compared byte for byte with plistlib.
func TestParityWithPythonGenarate(t *testing.T) {
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

	cases := generateCases(20260315, 300)
	reqs := make([]map[string]any, 0, len(cases))
	for _, c := range cases {
		reqs = append(reqs, map[string]any{
			"report": c.Report, "disabled": c.Disabled, "smbios_model": c.Model,
			"macos": c.Macos, "needs_oclp": c.NeedsOCLP, "checked": c.CheckedKex,
		})
	}
	input, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_config.py"), repo, testdata)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
	}
	var py []struct {
		OK    *string `json:"ok"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal(out, &py); err != nil {
		t.Fatal(err)
	}
	if len(py) != len(cases) {
		t.Fatalf("python returned %d results for %d cases", len(py), len(cases))
	}

	sample, err := os.ReadFile(filepath.Join(testdata, "Sample.plist"))
	if err != nil {
		t.Fatal(err)
	}
	stats := map[string]int{}
	failures := 0
	for i, c := range cases {
		stats[py[i].Error+"|ok"]++
		if py[i].Error != "" {
			stats["python "+py[i].Error]++
		}
		goText, goErr := runGo(t, c, sample, testdata)
		if py[i].Error != "" {
			if goErr == nil {
				failures++
				t.Errorf("case %d: python raised %s; go produced %d bytes", i, py[i].Error, len(goText))
			}
			continue
		}
		if goErr != nil {
			failures++
			t.Errorf("case %d: go error %v; python ok", i, goErr)
			continue
		}
		if goText != *py[i].OK {
			failures++
			t.Errorf("case %d (%s): config.plist differs (go %d bytes, python %d bytes)", i, c.Macos, len(goText), len(*py[i].OK))
		}
		if failures >= 5 {
			t.Fatalf("stopping after %d mismatches", failures)
		}
	}
	t.Logf("compared %d cases: %d ok in python, %d raise", len(cases), len(cases)-countErrors(py), countErrors(py))
}

func countErrors(py []struct {
	OK    *string `json:"ok"`
	Error string  `json:"error"`
}) int {
	n := 0
	for _, p := range py {
		if p.Error != "" {
			n++
		}
	}
	return n
}

// runGo runs one case with the same fixed hooks as the Python oracle.
func runGo(t *testing.T, c reportCase, sample []byte, testdata string) (string, error) {
	t.Helper()
	reportText, err := json.Marshal(c.Report)
	if err != nil {
		t.Fatal(err)
	}
	report, err := pyjson.Load(reportText)
	if err != nil {
		t.Fatal(err)
	}
	disabledText, err := json.Marshal(c.Disabled)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := pyjson.Load(disabledText)
	if err != nil {
		t.Fatal(err)
	}
	root, err := plist.Unmarshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	config := root.(*plist.Dict)

	kexts := datasets.NewCatalog()
	for _, name := range c.CheckedKex {
		kexts[datasets.KextIndexByName[name]].Checked = true
	}

	p := &Prodigy{
		RequestInput:  func(string) (string, error) { return "", nil },
		Choose:        func(int) int { return 0 },
		KernelPatches: func(string, string) []any { return fixtureProfile(t, testdata) },
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

	result, err := p.Genarate(report, disabled, c.Model, c.Macos, c.NeedsOCLP, kexts, config)
	if err != nil {
		return "", err
	}
	data, err := plist.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
