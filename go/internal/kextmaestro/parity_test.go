package kextmaestro

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// answer answers the interactive prompts. It must match answer() in
// testdata/pyref_kextmaestro.py exactly.
func answer(prompt string) (string, error) {
	switch {
	case strings.Contains(prompt, "audio kext"):
		return "2", nil
	case strings.Contains(prompt, "AMD") && strings.Contains(prompt, "GPU"):
		return "1", nil
	case strings.Contains(prompt, "Intel Wi-Fi"):
		return "2", nil
	case strings.Contains(prompt, "force load"):
		return "yes", nil
	}
	return "", nil
}

type oracleOut struct {
	OK    *string `json:"ok"`
	Error string  `json:"error"`
}

func runKextOracle(t *testing.T, reqs []map[string]any) []oracleOut {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_kextmaestro.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
	}
	var results []oracleOut
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("decoding python output: %v\n%s", err, out)
	}
	if len(results) != len(reqs) {
		t.Fatalf("python returned %d results for %d requests", len(results), len(reqs))
	}
	return results
}

// dumpText renders a Go result the way the oracle does (json.dumps indent=4).
func dumpText(t *testing.T, v any) string {
	t.Helper()
	s, err := pyjson.Dump(v)
	if err != nil {
		t.Fatalf("dump %T: %v", v, err)
	}
	return s
}

// reportMatrix generates hardware reports covering the branches of
// select_required_kexts. The values come from the generated datasets, so the
// reports stay in sync with the Python data.
func reportMatrix(seed int64, count int) []map[string]any {
	r := rand.New(rand.NewSource(seed))
	pick := func(xs []string) string { return xs[r.Intn(len(xs))] }
	maybe := func(p float64) bool { return r.Float64() < p }

	names := []string{"Z690 AORUS", "Surface Laptop 4", "XPS DELL 9300", "ASUS ROG Strix", "Intel NUC 11", "B550 Board"}
	platforms := []string{"Desktop", "Laptop", "Server"}
	processors := []string{"Intel Core i7-8750H", "Intel Core i9-12900K", "Intel Xeon W-3245", "Intel Core i5-7300U", "AMD Ryzen 7 5800X"}
	codenames := append([]string{"Sandy Bridge", "Ivy Bridge", "Haswell", "Skylake", "Coffee Lake", "Lunar Lake-M", "Meteor Lake-P", "Zen 3"}, datasets.IntelCPUGenerations...)
	simd := [][]any{{"AVX2", "SSE4.2"}, {"SSE4.2"}}
	gpuTypes := []string{"Integrated GPU", "Discrete GPU"}
	gpuMfr := []string{"Intel", "AMD", "NVIDIA"}
	gpuCodenames := []string{"Navi 21", "Navi 22", "Navi 23", "Polaris", "Iris Plus"}
	usbSubsystems := []string{"0A111028", "00000000"}

	wifiIDs := append(append([]string{}, datasets.BroadcomWiFiIDs...), datasets.IntelWiFiIDs...)
	wifiIDs = append(wifiIDs, datasets.AtherosWiFiIDs...)
	wifiIDs = append(wifiIDs, datasets.Rtw88WiFiIDs...)
	wifiIDs = append(wifiIDs, datasets.IntelI22XIDs...)
	wifiIDs = append(wifiIDs, datasets.AtherosE2200IDs...)
	wifiIDs = append(wifiIDs, datasets.IntelMausiIDs...)
	wifiIDs = append(wifiIDs, datasets.RealtekRTL8100IDs...)
	wifiIDs = append(wifiIDs, datasets.RealtekRTL8111IDs...)
	wifiIDs = append(wifiIDs, datasets.AppleIGBIDs...)
	wifiIDs = append(wifiIDs, datasets.BroadcomBCM57XXIDs...)
	wifiIDs = append(wifiIDs, datasets.IntelX500IDs...)
	wifiIDs = append(wifiIDs, datasets.RealtekRTL812xIDs...)
	wifiIDs = append(wifiIDs, "0000-0000")

	btIDs := append([]string{}, datasets.AtherosBluetoothIDs...)
	btIDs = append(btIDs, datasets.BroadcomBluetoothIDs...)
	btIDs = append(btIDs, datasets.IntelBluetoothIDs...)
	btIDs = append(btIDs, datasets.RealtekBluetoothIDs...)
	btIDs = append(btIDs, datasets.BluetoothIDs[len(datasets.BluetoothIDs)-1], "FFFF-FFFF")

	inputIDs := append([]string{"SYN0001"}, datasets.InputIDs...)
	usbDevices := make([]string, 0, len(datasets.UnsupportedUSBControllerIDs)+1)
	for _, e := range datasets.UnsupportedUSBControllerIDs {
		usbDevices = append(usbDevices, e.Device)
	}
	usbDevices = append(usbDevices, "8086-1234")
	macosVersions := []string{"16.7.0", "19.0.0", "20.0.0", "21.4.0", "22.0.0", "23.0.0", "24.0.0", "25.0.0", "bad"}

	reports := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		board := map[string]any{"Name": pick(names), "Platform": pick(platforms)}
		cpu := map[string]any{
			"Manufacturer":   pick([]string{"Intel", "AMD"}),
			"Codename":       pick(codenames),
			"Processor Name": pick(processors),
			"Core Count":     []int{2, 4, 6, 8, 10, 12}[r.Intn(6)],
			"CPU Count":      []int{1, 2}[r.Intn(2)],
			"SIMD Features":  simd[r.Intn(2)],
		}
		report := map[string]any{"Motherboard": board, "CPU": cpu, "USB Controllers": map[string]any{}}

		if maybe(0.8) {
			gpus := map[string]any{}
			for g := 0; g < 1+r.Intn(2); g++ {
				gpus[fmt.Sprintf("GPU %d", g)] = map[string]any{
					"Device Type":  pick(gpuTypes),
					"Manufacturer": pick(gpuMfr),
					"Codename":     pick(gpuCodenames),
				}
			}
			report["GPU"] = gpus
		}
		if maybe(0.7) {
			network := map[string]any{}
			for n := 0; n < r.Intn(3); n++ {
				network[fmt.Sprintf("Network %d", n)] = map[string]any{
					"Device ID": pick(wifiIDs),
					"Bus Type":  pick([]string{"PCI", "USB"}),
				}
			}
			report["Network"] = network
		}
		if maybe(0.5) {
			bt := map[string]any{}
			for b := 0; b < r.Intn(2); b++ {
				bt[fmt.Sprintf("BT %d", b)] = map[string]any{"Device ID": pick(btIDs)}
			}
			report["Bluetooth"] = bt
		}
		if board["Platform"] == "Laptop" && maybe(0.8) {
			inputs := map[string]any{}
			for k := 0; k < 1+r.Intn(3); k++ {
				inputs[fmt.Sprintf("Input %d", k)] = map[string]any{
					"Device":      pick(inputIDs),
					"Device Type": pick([]string{"PS/2", "I2C", "USB"}),
				}
			}
			report["Input"] = inputs
		}
		if maybe(0.4) {
			report["Sound"] = map[string]any{"HDA": map[string]any{"Device ID": pick([]string{"10EC-0295", "1234-5678"})}}
		}
		if maybe(0.3) {
			report["System Devices"] = map[string]any{"Hub": map[string]any{
				"Bus Type": pick([]string{"ACPI", "PCI"}),
				"Device":   pick(append([]string{"XYZ"}, datasets.YogaHIDs...)),
			}}
		}
		if maybe(0.4) {
			report["SD Controller"] = map[string]any{"SD": map[string]any{"Device ID": pick(datasets.RealtekCardReaderIDs)}}
		}
		if maybe(0.6) {
			report["Storage Controllers"] = map[string]any{
				pick([]string{"NVMe Controller", "AHCI SATA", "NVM Express", "Intel RAID"}): map[string]any{
					"Device ID": pick(append([]string{"1C5C-174A"}, datasets.UnsupportedSATAControllerIDs...)),
				},
			}
		}
		if maybe(0.6) {
			usb := map[string]any{}
			for u := 0; u < 1+r.Intn(2); u++ {
				entry := map[string]any{"Device ID": pick(usbDevices)}
				if maybe(0.6) {
					entry["Subsystem ID"] = pick(usbSubsystems)
				}
				usb[fmt.Sprintf("USB %d", u)] = entry
			}
			report["USB Controllers"] = usb
		}

		acpi := []any{}
		if maybe(0.5) {
			acpi = append(acpi, map[string]any{"name": "BATP", "checked": maybe(0.5)})
		}
		reports = append(reports, map[string]any{
			"report":     report,
			"macos":      pick(macosVersions),
			"needs_oclp": maybe(0.2),
			"acpi":       acpi,
		})
	}
	return reports
}

// TestParitySelectRequiredKexts compares select_required_kexts over a matrix of
// generated hardware reports: the needs_oclp result and the checked kexts.
func TestParitySelectRequiredKexts(t *testing.T) {
	cases := reportMatrix(20260101, 400)
	var reqs []map[string]any
	for _, c := range cases {
		reqs = append(reqs, map[string]any{
			"op": "select", "report": c["report"], "macos": c["macos"],
			"needs_oclp": c["needs_oclp"], "acpi": c["acpi"],
		})
	}
	py := runKextOracle(t, reqs)

	failures := 0
	errors := 0
	for i, c := range cases {
		reportText, _ := json.Marshal(c["report"])
		report, err := pyjson.Load(reportText)
		if err != nil {
			t.Fatal(err)
		}
		var patches []ACPIPatch
		for _, a := range c["acpi"].([]any) {
			p := a.(map[string]any)
			patches = append(patches, ACPIPatch{Name: p["name"].(string), Checked: p["checked"].(bool)})
		}

		m := New(t.TempDir())
		m.RequestInput = answer
		needs, goErr := m.SelectRequiredKexts(report, c["macos"].(string), c["needs_oclp"].(bool), patches)

		if py[i].Error != "" {
			errors++
			if goErr == nil {
				failures++
				t.Errorf("case %d: python raised %s; go returned needs=%v", i, py[i].Error, needs)
			}
			continue
		}
		if goErr != nil {
			failures++
			t.Errorf("case %d: go error %v; python ok", i, goErr)
			continue
		}
		var checked []string
		for _, k := range m.Kexts {
			if k.Checked {
				checked = append(checked, k.Name)
			}
		}
		sort.Strings(checked)
		result := plist.NewDict()
		result.Set("needs_oclp", needs)
		result.Set("checked", stringsAsAny(checked))
		want := dumpText(t, result)
		if want != *py[i].OK {
			failures++
			t.Errorf("case %d (%s):\n go: %s\n py: %s", i, c["macos"], want, *py[i].OK)
		}
		if failures >= 10 {
			t.Fatalf("stopping after %d mismatches", failures)
		}
	}
	t.Logf("compared %d select cases (%d raise in the original too)", len(cases), errors)
}

func stringsAsAny(in []string) []any {
	out := []any{}
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// TestParityCheckSequences compares check_kext, uncheck_kext, get_kext_status
// and is_kext_compatible over random sequences, which exercise dependencies,
// conflicts and force loading.
func TestParityCheckSequences(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	names := make([]string, 0, len(datasets.NewCatalog()))
	for _, k := range datasets.NewCatalog() {
		names = append(names, k.Name)
	}
	targets := []string{"16.7.0", "19.0.0", "21.4.0", "23.0.0", "25.0.0"}

	var reqs []map[string]any
	var plans [][]map[string]any
	const sequences = 60
	for s := 0; s < sequences; s++ {
		reqs = append(reqs, map[string]any{"op": "reset"})
		var plan []map[string]any
		for step := 0; step < 8; step++ {
			name := names[r.Intn(len(names))]
			target := targets[r.Intn(len(targets))]
			var op map[string]any
			switch r.Intn(4) {
			case 0:
				op = map[string]any{"op": "check", "name": name, "target": target, "force": r.Intn(2) == 0}
			case 1:
				op = map[string]any{"op": "uncheck", "name": name}
			case 2:
				op = map[string]any{"op": "status", "name": name, "target": target}
			default:
				op = map[string]any{"op": "compat", "name": name, "target": target}
			}
			plan = append(plan, op)
			reqs = append(reqs, op)
		}
		reqs = append(reqs, map[string]any{"op": "checked"})
		plans = append(plans, plan)
	}
	py := runKextOracle(t, reqs)

	idx := 0
	for s, plan := range plans {
		idx++ // reset
		m := New(t.TempDir())
		m.RequestInput = answer
		for _, op := range plan {
			want := py[idx]
			idx++
			name := op["name"].(string)
			var got string
			var err error
			switch op["op"] {
			case "check":
				var ok bool
				ok, err = m.CheckKext(lookup(name), op["target"].(string), op["force"].(bool), nil)
				got = dumpText(t, ok)
			case "uncheck":
				var ok bool
				ok, err = m.UncheckKext(lookup(name))
				got = dumpText(t, ok)
			case "status":
				var st string
				st, err = m.GetKextStatus(m.Kexts[lookup(name)], op["target"].(string))
				got = dumpText(t, st)
			case "compat":
				var ok bool
				ok, err = m.IsKextCompatible(m.Kexts[lookup(name)], op["target"].(string))
				got = dumpText(t, ok)
			}
			if want.Error != "" {
				if err == nil {
					t.Errorf("sequence %d %v: python raised %s; go returned %s", s, op, want.Error, got)
				}
				continue
			}
			if err != nil {
				t.Errorf("sequence %d %v: go error %v; python %s", s, op, err, *want.OK)
				continue
			}
			if got != *want.OK {
				t.Errorf("sequence %d %v: go %s; python %s", s, op, got, *want.OK)
			}
		}
		// Final checked set after the sequence.
		want := py[idx]
		idx++
		var checked []string
		for _, k := range m.Kexts {
			if k.Checked {
				checked = append(checked, k.Name)
			}
		}
		sort.Strings(checked)
		got := dumpText(t, stringsAsAny(checked))
		if want.Error == "" && got != *want.OK {
			t.Errorf("sequence %d final checked: go %s; python %s", s, got, *want.OK)
		}
	}
}

// TestParityVerifyCompatibility compares verify_kext_compatibility with the
// prompt answered "yes" by both sides.
func TestParityVerifyCompatibility(t *testing.T) {
	names := []string{"AppleALC", "Lilu", "NootedRed", "WhateverGreen", "AirportItlwm", "VoodooPS2Controller", "CpuTscSync"}
	var reqs []map[string]any
	for _, target := range []string{"16.7.0", "25.0.0"} {
		reqs = append(reqs, map[string]any{"op": "verify", "names": names, "target": target})
	}
	py := runKextOracle(t, reqs)
	for i, target := range []string{"16.7.0", "25.0.0"} {
		m := New(t.TempDir())
		m.RequestInput = answer
		forced, err := m.VerifyKextCompatibility(names, target)
		if err != nil {
			t.Fatal(err)
		}
		var list []string
		for k := range forced {
			list = append(list, k)
		}
		sort.Strings(list)
		if got := dumpText(t, stringsAsAny(list)); got != *py[i].OK {
			t.Errorf("verify %s: go %s; python %s", target, got, *py[i].OK)
		}
	}
}
