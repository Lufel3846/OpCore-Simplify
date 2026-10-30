package smbios

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// pyRequest and pyResult follow the protocol of testdata/pyref_smbios.py.
type pyRequest struct {
	Op    string `json:"op"`
	Arg   any    `json:"arg,omitempty"`
	MacOS string `json:"macos,omitempty"`
}

type pyResult struct {
	OK    any    `json:"ok"`
	Error string `json:"error"`
}

// selectCase is one input to both implementations.
type selectCase struct {
	report HardwareReport
	macos  string
}

// selectMatrix builds every combination of the inputs below. The values cover
// each branch of select_smbios_model: all codenames the original tests for,
// laptop/desktop/NUC platforms, GPU orderings, core counts on both sides of
// each threshold, and a malformed macOS version. They are test vectors for
// comparison; the expected results come from the Python original.
func selectMatrix() []selectCase {
	boards := []Motherboard{
		{Name: "Board", Platform: "Desktop"},
		{Name: "Board", Platform: "Laptop"},
		{Name: "Intel NUC", Platform: "Desktop"},
		{Name: "Board", Platform: "Other"},
	}
	codenames := []string{
		"Lynnfield", "Clarkdale", "Beckton", "Westmere-EX", "Gulftown", "Westmere-EP",
		"Jasper Forest", "Gainestown", "Bloomfield", "Sandy Bridge", "Ivy Bridge",
		"Arrandale", "Clarksfield", "Haswell", "Broadwell", "Skylake", "Amber Lake",
		"Kaby Lake", "Cannon Lake", "Whiskey Lake", "Coffee Lake", "Comet Lake",
		"Ice Lake", "Unknown",
	}
	procNames := []string{"Intel Core i7-8750H", "Intel Xeon W", "Intel Core i5-7300U"}
	gpuSets := [][]GPU{
		{},
		{{DeviceType: "Integrated GPU"}},
		{{DeviceType: "Discrete GPU"}},
		{{DeviceType: "Integrated GPU"}, {DeviceType: "Discrete GPU"}},
		{{DeviceType: "Discrete GPU"}, {DeviceType: "Integrated GPU"}},
	}
	cores := []int{2, 4, 10}
	macosVersions := []string{
		"16.7.0", "17.99.99", "18.0.0", "19.0.0", "19.6.0", "22.0.0", "24.0.0", "25.0.0", "bad",
	}

	var out []selectCase
	for _, b := range boards {
		for _, cn := range codenames {
			for _, pn := range procNames {
				for _, gpus := range gpuSets {
					for _, c := range cores {
						for _, mv := range macosVersions {
							out = append(out, selectCase{
								report: HardwareReport{
									Motherboard: b,
									CPU:         CPU{Codename: cn, ProcessorName: pn, CoreCount: c},
									GPUs:        gpus,
								},
								macos: mv,
							})
						}
					}
				}
			}
		}
	}
	return out
}

// pyReport converts a case to the neutral JSON form read by the Python oracle.
func pyReport(c selectCase) map[string]any {
	gpus := make([]map[string]string, 0, len(c.report.GPUs))
	for _, g := range c.report.GPUs {
		gpus = append(gpus, map[string]string{"device_type": g.DeviceType})
	}
	return map[string]any{
		"motherboard": map[string]string{"name": c.report.Motherboard.Name, "platform": c.report.Motherboard.Platform},
		"cpu": map[string]any{
			"codename":       c.report.CPU.Codename,
			"processor_name": c.report.CPU.ProcessorName,
			"core_count":     c.report.CPU.CoreCount,
		},
		"gpus": gpus,
	}
}

// TestParityWithPythonReference compares the device table and the SMBIOS
// selection against the unmodified Python original on every matrix case.
func TestParityWithPythonReference(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cases := selectMatrix()
	reqs := []pyRequest{{Op: "devices"}}
	for _, c := range cases {
		reqs = append(reqs, pyRequest{Op: "select", MacOS: c.macos, Arg: pyReport(c)})
	}
	results := runPython(t, python, repo, reqs)

	checkDevices(t, results[0])

	mismatches := 0
	for i, c := range cases {
		if !checkSelect(t, c, results[i+1]) {
			mismatches++
			if mismatches >= 10 {
				t.Fatalf("stopping after %d mismatches", mismatches)
			}
		}
	}
	t.Logf("compared %d select cases against the Python original", len(cases))
}

// checkDevices compares the mac_devices table. A Go "" discrete GPU stands for
// Python's None, so it is expected as null.
func checkDevices(t *testing.T, py pyResult) {
	t.Helper()
	var want []any
	for _, d := range Devices() {
		row := []any{d.Name, d.CPU, d.CPUGeneration, nil, d.InitialSupport, d.LastSupportedVersion}
		if d.DiscreteGPU != "" {
			row[3] = d.DiscreteGPU
		}
		want = append(want, row)
	}
	if !reflect.DeepEqual(py.OK, any(want)) {
		t.Errorf("device table differs from mac_model_data.py (go %d rows)", len(want))
	}
}

// checkSelect reports whether Go agrees with Python for one case. An IndexError
// in Python must map to ErrNoGPU, and a ValueError to any other error.
func checkSelect(t *testing.T, c selectCase, py pyResult) bool {
	t.Helper()
	got, err := SelectDefaultModel(c.report, c.macos)
	ok := true
	switch py.Error {
	case "IndexError":
		ok = errors.Is(err, ErrNoGPU)
	case "ValueError":
		ok = err != nil && !errors.Is(err, ErrNoGPU)
	case "":
		ok = err == nil && py.OK == any(got)
	default:
		ok = false
	}
	if !ok {
		t.Errorf("SelectDefaultModel(%+v, %q) = %q, %v; python %v / %s",
			c.report, c.macos, got, err, py.OK, py.Error)
	}
	return ok
}

func runPython(t *testing.T, python, repo string, reqs []pyRequest) []pyResult {
	t.Helper()
	input, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_smbios.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
	}
	var results []pyResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("decoding python output: %v", err)
	}
	if len(results) != len(reqs) {
		t.Fatalf("python returned %d results for %d requests", len(results), len(reqs))
	}
	return results
}
