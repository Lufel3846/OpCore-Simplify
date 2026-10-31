package acpiguru

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt/dsdttest"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

type result struct {
	OK      any      `json:"ok"`
	Error   bool     `json:"error"`
	Prompts []string `json:"prompts"`
	Message string   `json:"message"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runOracle(t *testing.T, steps []map[string]any, iasl string, env map[string]string) []result {
	t.Helper()
	in, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-I", "testdata/pyref_acpiguru.py", repoRoot(t), iasl)
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle failed: %v\n%s", err, errb.String())
	}
	var res []result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("oracle output: %v\n%s", err, out.String())
	}
	return res
}

// conv turns Go values into the JSON shape the oracle emits.
func conv(v any) any {
	switch x := v.(type) {
	case []byte:
		return map[string]any{"hex": hex.EncodeToString(x)}
	case *plist.Dict:
		if x == nil {
			return nil
		}
		pairs := []any{}
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			pairs = append(pairs, []any{k, conv(val)})
		}
		return map[string]any{"__dict__": pairs}
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = conv(e)
		}
		return out
	}
	return v
}

func normalize(v any) any {
	b, err := json.Marshal(conv(v))
	if err != nil {
		panic(err)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func stepStr(s map[string]any, k string) string { v, _ := s[k].(string); return v }

func stepInt(s map[string]any, k string, def int) int {
	switch v := s[k].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return def
}

func stepStrings(s map[string]any, k string) []string {
	switch v := s[k].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, len(v))
		for i, x := range v {
			out[i], _ = x.(string)
		}
		return out
	}
	return nil
}

func load(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pyjson.Load(b)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func checkedNames(g *Guru) []any {
	out := []any{}
	for _, p := range g.Patches {
		if p.Checked {
			out = append(out, p.Name)
		}
	}
	return out
}

func tableState(g *Guru) map[string]any {
	names := g.Acpi.AcpiTables.Names()
	sort.Strings(names)
	var d any
	if g.Dsdt != nil {
		d = shaHex(g.Dsdt.Raw)
	}
	n := make([]any, len(names))
	for i, x := range names {
		n[i] = x
	}
	return map[string]any{"tables": n, "dsdt": d}
}

func shaHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// goStep runs one step on the Go port; the second value is the error flag.
func goStep(t *testing.T, g *Guru, s map[string]any) (any, bool) {
	t.Helper()
	switch stepStr(s, "op") {
	case "unique_name":
		app := "-Patched"
		if v, ok := s["append"].(string); ok {
			app = v
		}
		return GetUniqueName(stepStr(s, "name"), stepStr(s, "folder"), app), false
	case "unique_device":
		name, n := g.GetUniqueDevice(stepStr(s, "base"), stepInt(s, "start", 0), stepStrings(s, "used"))
		return []any{name, n}, false
	case "sorted_nicely":
		out := SortedNicely(stepStrings(s, "list"))
		r := make([]any, len(out))
		for i, x := range out {
			r[i] = x
		}
		return r, false
	case "get_data":
		var data any = stepStr(s, "text")
		if h, ok := s["bytes"].(string); ok {
			data, _ = hex.DecodeString(h)
		}
		out, err := GetData(data, stepInt(s, "pad_to", 0))
		return out, err != nil
	case "hex_to_bytes":
		out, err := HexToBytes(s["value"])
		return out, err != nil
	case "apply_patches":
		out, err := g.ApplyACPIPatches(load(t, s["patches"]).([]any))
		return out, err != nil
	case "drop_table":
		var sig, oem []byte
		if h, ok := s["sig"].(string); ok {
			sig, _ = hex.DecodeString(h)
			if sig == nil {
				sig = []byte{}
			}
		}
		if h, ok := s["oem"].(string); ok {
			oem, _ = hex.DecodeString(h)
			if oem == nil {
				oem = []byte{}
			}
		}
		d, err := g.DroppingTheTable(sig, oem)
		if d == nil {
			return nil, err != nil
		}
		return d, err != nil
	case "drop_cpu":
		d, err := g.DropCPUTables()
		if err != nil {
			return nil, true
		}
		return d, false
	case "patch_index":
		i, ok := g.GetPatchIndex(stepStr(s, "name"))
		if !ok {
			return nil, false
		}
		return i, false
	case "hedt":
		b, err := IsIntelHEDTCPU(load(t, s["processor"]), load(t, s["codename"]))
		return b, err != nil
	case "select_patches":
		if err := g.SelectACPIPatches(load(t, s["report"]), load(t, s["disabled"])); err != nil {
			return nil, true
		}
		return checkedNames(g), false
	case "checked":
		return checkedNames(g), false
	case "customize":
		err := g.CustomizePatchSelection()
		if errors.Is(err, ErrExit) {
			return map[string]any{"exit": true, "checked": checkedNames(g)}, false
		}
		if err != nil {
			return nil, true
		}
		return map[string]any{"exit": false, "checked": checkedNames(g)}, false
	case "read_tables":
		p, ok, err := g.ReadACPITables(stepStr(s, "path"))
		if err != nil {
			return nil, true
		}
		var r any
		if ok {
			r = p
		}
		return map[string]any{"result": r, "state": tableState(g)}, false
	case "ensure":
		ok, err := g.EnsureDSDT(s["allow_any"] == true)
		if errors.Is(err, ErrExit) {
			return map[string]any{"exit": true, "state": tableState(g)}, false
		}
		if err != nil {
			return nil, true
		}
		return map[string]any{"result": ok, "state": tableState(g), "exit": false}, false
	case "select_tables":
		p, ok, err := g.SelectACPITables()
		if errors.Is(err, ErrExit) {
			return map[string]any{"exit": true, "state": tableState(g)}, false
		}
		if err != nil {
			return nil, true
		}
		var r any
		if ok {
			r = p
		}
		return map[string]any{"result": r, "state": tableState(g), "exit": false}, false
	case "set_dsdt":
		g.Dsdt = g.Acpi.GetDSDTOrOnly()
		return true, false
	case "clear":
		g.Dsdt = nil
		g.Acpi.AcpiTables = dsdt.NewTables()
		return true, false
	case "write_ssdt":
		dir := stepStr(s, "directory")
		g.ACPIDirectory = dir
		compile := true
		if v, ok := s["compile"].(bool); ok {
			compile = v
		}
		r, err := g.WriteSSDT(stepStr(s, "name"), stepStr(s, "content"), compile)
		if err != nil {
			return nil, true
		}
		out := map[string]any{"result": r}
		for _, ext := range []string{".dsl", ".aml"} {
			b, rerr := os.ReadFile(filepath.Join(dir, stepStr(s, "name")+ext))
			if rerr != nil {
				out[ext] = nil
			} else {
				out[ext] = hex.EncodeToString(b)
			}
		}
		return out, false
	case "normalize_path":
		p, err := NormalizePath(stepStr(s, "path"))
		return p, err != nil
	}
	panic("unknown op " + stepStr(s, "op"))
}

type scenario struct {
	files map[string]dsdttest.Table
	extra map[string][]byte
	dsl   map[string]string
	env   map[string]string
	// subdirs are written under the tables folder: name -> file name -> bytes.
	subdirs map[string]map[string][]byte
	// steps receives the tables folder and a fresh output folder.
	steps func(dir, out string) []map[string]any
}

func writeScenario(t *testing.T, sc scenario) (dir, dslDir, iasl, base string) {
	t.Helper()
	base = t.TempDir()
	dir = filepath.Join(base, "tables")
	dslDir = filepath.Join(base, "dsl")
	for _, d := range []string{dir, dslDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, tb := range sc.files {
		if err := os.WriteFile(filepath.Join(dir, name), tb.Raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, b := range sc.extra {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for sub, files := range sc.subdirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		for name, b := range files {
			if err := os.WriteFile(filepath.Join(dir, sub, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, text := range sc.dsl {
		if err := os.WriteFile(filepath.Join(dslDir, name+".dsl"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	iasl = filepath.Join(base, "iasl")
	if err := os.WriteFile(iasl, []byte(dsdttest.FakeIasl), 0o755); err != nil {
		t.Fatal(err)
	}
	return
}

func prepare(steps []map[string]any) []map[string]any {
	b, _ := json.Marshal(steps)
	var out []map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func runScenario(t *testing.T, sc scenario) []result {
	t.Helper()
	dir, dslDir, iasl, base := writeScenario(t, sc)
	env := map[string]string{"FAKE_DSL_DIR": dslDir, "HOME": "/home/user"}
	for k, v := range sc.env {
		env[k] = v
	}
	pySteps := sc.steps(dir, filepath.Join(base, "out-py"))
	goSteps := sc.steps(dir, filepath.Join(base, "out-go"))
	py := runOracle(t, prepare(pySteps), iasl, env)
	if len(py) != len(pySteps) {
		t.Fatalf("oracle returned %d results for %d steps", len(py), len(pySteps))
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	var inputs []string
	var prompts []string
	g := New(dsdt.New(iasl), func(prompt string) (string, error) {
		prompts = append(prompts, prompt)
		if len(inputs) == 0 {
			return "", io.EOF
		}
		v := inputs[0]
		inputs = inputs[1:]
		return v, nil
	})
	for i, s := range goSteps {
		inputs = stepStrings(s, "inputs")
		prompts = nil
		got, gerr := goStep(t, g, s)
		want := py[i]
		if gerr != want.Error {
			t.Errorf("step %d %v: Go error=%v, Python error=%v (%s)", i, s["op"], gerr, want.Error, want.Message)
			continue
		}
		gp := prompts
		if gp == nil {
			gp = []string{}
		}
		wp := want.Prompts
		if wp == nil {
			wp = []string{}
		}
		if !reflect.DeepEqual(gp, wp) {
			t.Errorf("step %d %v prompts:\n go: %q\n py: %q", i, s["op"], gp, wp)
		}
		if gerr {
			continue
		}
		if g := normalize(got); !reflect.DeepEqual(g, want.OK) {
			gb, _ := json.Marshal(g)
			wb, _ := json.Marshal(want.OK)
			t.Errorf("step %d %v %s:\n go: %.700s\n py: %.700s", i, s["op"], summarize(s), gb, wb)
		}
	}
	return py
}

func summarize(s map[string]any) string {
	var parts []string
	for k, v := range s {
		if k == "op" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%.80v", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// ---- reports ----------------------------------------------------------------

type rep = map[string]any

func baseReport() rep {
	return rep{
		"Motherboard":    rep{"Name": "Generic Board", "Platform": "Desktop", "Chipset": "Z370"},
		"CPU":            rep{"Manufacturer": "Intel", "Processor Name": "Intel Core i7", "Codename": "Coffee Lake-S"},
		"GPU":            rep{"GPU0": rep{"Device Type": "Discrete GPU"}},
		"Network":        rep{"N0": rep{"Bus Type": "PCI"}},
		"Input":          rep{},
		"System Devices": rep{},
	}
}

func clone(r rep) rep {
	b, _ := json.Marshal(r)
	var out rep
	_ = json.Unmarshal(b, &out)
	return out
}

func sub(r rep, keys ...string) rep {
	cur := r
	for _, k := range keys {
		cur = cur[k].(map[string]any)
	}
	return cur
}

func TestParityPureFunctions(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "res")
	_ = os.MkdirAll(folder, 0o755)
	for _, n := range []string{"SSDT-A-Patched.aml", "SSDT-A-Patched-1.aml", "SSDT-A-Patched-2.aml", "plain-Patched", "dot.-Patched", "x.tar-Patched.gz", "x.tar-Patched-1.gz"} {
		_ = os.WriteFile(filepath.Join(folder, n), nil, 0o644)
	}
	sc := scenario{
		files: map[string]dsdttest.Table{
			"DSDT.aml":     dsdttest.BuildTable(dsdttest.TableOpts{Sig: "DSDT", OEM: "ALASKA", ID: "A M I   ", BodySize: 200, Seed: 1}),
			"SSDT-Pm.aml":  dsdttest.BuildTable(dsdttest.TableOpts{Sig: "SSDT", OEM: "PmRef ", ID: "Cpu0Ist ", BodySize: 90, Seed: 2}),
			"SSDT-Pm2.aml": dsdttest.BuildTable(dsdttest.TableOpts{Sig: "SSDT", OEM: "PmRef ", ID: "CpuPm   ", BodySize: 60, Seed: 3}),
		},
	}
	sc.files["DSDT.aml"] = dsdttest.BuildTable(dsdttest.TableOpts{Sig: "DSDT", OEM: "ALASKA", ID: "A M I   ", BodySize: 200, Seed: 1})
	sc.dsl = map[string]string{"DSDT.aml": sc.files["DSDT.aml"].DSL, "SSDT-Pm.aml": sc.files["SSDT-Pm.aml"].DSL, "SSDT-Pm2.aml": sc.files["SSDT-Pm2.aml"].DSL}
	sc.steps = func(dir, out string) []map[string]any {
		var steps []map[string]any
		add := func(s map[string]any) { steps = append(steps, s) }
		for _, name := range []string{"SSDT-A.aml", "/some/dir/SSDT-A.aml", "plain", "dot.", "x.tar.gz", "x.tar.gz.bak", "noext", "fresh.aml", ".hidden", "a/b/", ""} {
			add(map[string]any{"op": "unique_name", "name": name, "folder": folder})
			add(map[string]any{"op": "unique_name", "name": name, "folder": folder, "append": ""})
			add(map[string]any{"op": "unique_name", "name": name, "folder": folder, "append": "-X"})
		}
		for _, l := range [][]string{
			{"SSDT-10.aml", "SSDT-2.aml", "ssdt-1.aml", "DSDT.aml"},
			{"a10b2", "a10b10", "a9b99", "A10B1", "a", "a0", "a00", "a000"},
			{"", "1", "01", "001", "z", "Z"},
			{"file 10", "file 9", "File 9a", "file 9B"},
			{"123456789012345678901234567890", "99999999999999999999999999999", "5"},
		} {
			add(map[string]any{"op": "sorted_nicely", "list": l})
		}
		for _, text := range []string{"", "CpuPm", "Cpu0Ist", "ünï", "abcdefghij"} {
			for _, pad := range []int{0, 4, 8, 12} {
				add(map[string]any{"op": "get_data", "text": text, "pad_to": pad})
			}
		}
		add(map[string]any{"op": "get_data", "bytes": "0a0b", "pad_to": 5})
		add(map[string]any{"op": "get_data", "bytes": "", "pad_to": 2})
		for _, v := range []any{"", " ", "AB", "ab cd", "A B C D", "ABC", "0x41", "GG", "41 42\n43", "4\t1", "４１", "12 34 56 7", 5, nil, true} {
			add(map[string]any{"op": "hex_to_bytes", "value": v})
		}
		add(map[string]any{"op": "apply_patches", "patches": []any{}})
		add(map[string]any{"op": "apply_patches", "patches": []any{
			map[string]any{"Comment": "zeta", "Find": "AA BB", "Replace": "CC DD", "Count": 2, "Mask": "FFFF", "Skip": 3},
			map[string]any{"Comment": "alpha", "Find": "11", "Replace": "22", "OemTableId": "4143504900000000", "TableSignature": "44534454", "TableLength": 100, "Limit": 5, "Base": "\\_SB", "BaseSkip": 1, "ReplaceMask": "FF"},
			map[string]any{"Find": "xyz", "Replace": "123", "Comment": "Mid"},
			map[string]any{"Find": "1", "Replace": "", "Comment": "alpha"},
			map[string]any{"Find": "", "Replace": "", "Comment": "alpha", "Enabled": false},
		}})
		add(map[string]any{"op": "apply_patches", "patches": []any{map[string]any{"Replace": "AA"}}})
		add(map[string]any{"op": "apply_patches", "patches": []any{map[string]any{"Find": "AA"}}})
		add(map[string]any{"op": "apply_patches", "patches": []any{map[string]any{"Find": 5, "Replace": "AA"}}})
		add(map[string]any{"op": "apply_patches", "patches": []any{"notadict"}})
		add(map[string]any{"op": "apply_patches", "patches": []any{
			map[string]any{"Find": "A1", "Replace": "B1", "Comment": 2}, map[string]any{"Find": "A1", "Replace": "B1", "Comment": 1}}})
		add(map[string]any{"op": "apply_patches", "patches": []any{
			map[string]any{"Find": "A1", "Replace": "B1", "Comment": 2}, map[string]any{"Find": "A1", "Replace": "B1", "Comment": "x"}}})
		add(map[string]any{"op": "apply_patches", "patches": []any{map[string]any{"Find": "A1", "Replace": "B1", "Comment": 2}}})
		for _, n := range []string{"ALS", "XOSI", "PM (Legacy)", "nope", "", "als"} {
			add(map[string]any{"op": "patch_index", "name": n})
		}
		for _, gen := range datasets.IntelCPUGenerations {
			for _, proc := range []any{"Intel Xeon W-3175X", "Intel Core i9", nil} {
				add(map[string]any{"op": "hedt", "processor": proc, "codename": gen})
			}
		}
		for _, c := range []any{nil, "Unknown", "Zen 3", 5, []any{"Ivy Bridge"}} {
			add(map[string]any{"op": "hedt", "processor": "Xeon", "codename": c})
		}
		// Tables: drop helpers.
		add(map[string]any{"op": "drop_table", "sig": hex.EncodeToString([]byte("DSDT"))})
		add(map[string]any{"op": "drop_cpu"})
		add(map[string]any{"op": "read_tables", "path": dir})
		add(map[string]any{"op": "drop_table", "sig": hex.EncodeToString([]byte("DSDT"))})
		add(map[string]any{"op": "drop_table", "sig": hex.EncodeToString([]byte("NONE"))})
		add(map[string]any{"op": "drop_table", "sig": "", "oem": hex.EncodeToString([]byte("Cpu0Ist "))})
		add(map[string]any{"op": "drop_table", "oem": hex.EncodeToString([]byte("CpuPm   "))})
		add(map[string]any{"op": "drop_table", "oem": hex.EncodeToString([]byte("CpuPm"))})
		add(map[string]any{"op": "drop_table", "sig": hex.EncodeToString([]byte("NONE")), "oem": hex.EncodeToString([]byte("A M I   "))})
		add(map[string]any{"op": "drop_table"})
		add(map[string]any{"op": "drop_cpu"})
		for _, base := range []string{"XHC1", "EHC1", "ABCD", "A", "AB", "ABCDEFGH", "SAT0", "HPET", "PCI0"} {
			for _, start := range []int{0, 1, 15, 16, 255} {
				add(map[string]any{"op": "unique_device", "base": base, "start": start})
				add(map[string]any{"op": "unique_device", "base": base, "start": start, "used": []string{base[:max(0, len(base)-1)] + "1", base[:max(0, len(base)-1)] + "2"}})
			}
		}
		add(map[string]any{"op": "unique_device", "base": "LPCB", "start": -1})
		add(map[string]any{"op": "unique_device", "base": "LPC", "start": 4095})
		return steps
	}
	runScenario(t, sc)
}

func TestParityNormalizePath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real dir")
	_ = os.MkdirAll(filepath.Join(real, "inner"), 0o755)
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	sc := scenario{steps: func(dir, out string) []map[string]any {
		var steps []map[string]any
		for _, p := range []string{
			"/tmp", "'/tmp'", " \"/tmp/\" ", "''", "\"'/tmp'\"", "'/tmp'\n", "/tmp\n", "'", "", "   ", "~", "~/x", "~root", "~nosuchuser_zz/x", "~/",
			"a/b/../c", "/a\\ b/c", "//x//y", "///x", "/tmp/../tmp/./", "/nonexistent/../x", "/nonexistent/deep/path", ".", "..", "testdata", "./testdata/",
			link, link + "/inner", link + "/inner/../inner/", link + "/missing/x", filepath.Join(root, "real dir/inner"),
			"\t/tmp\t", "/tmp ' ", "'/tmp/real dir'", "a\\b\\c",
		} {
			steps = append(steps, map[string]any{"op": "normalize_path", "path": p})
		}
		return steps
	}}
	runScenario(t, sc)
}

// ---- patch selection ---------------------------------------------------------

func TestParitySelectPatches(t *testing.T) {
	var reports []rep
	add := func(r rep) { reports = append(reports, r) }

	add(baseReport())
	// Every generation, with desktop and laptop, Intel and AMD.
	for _, gen := range datasets.IntelCPUGenerations {
		for _, plat := range []string{"Desktop", "Laptop"} {
			r := baseReport()
			sub(r, "CPU")["Codename"] = gen
			sub(r, "Motherboard")["Platform"] = plat
			add(r)
			x := clone(r)
			sub(x, "CPU")["Processor Name"] = "Intel Xeon W"
			add(x)
		}
	}
	// Every chipset.
	for _, c := range append(append([]string{}, datasets.IntelChipsets...), datasets.AMDChipsets...) {
		r := baseReport()
		sub(r, "Motherboard")["Chipset"] = c
		add(r)
	}
	// AMD.
	amd := baseReport()
	sub(amd, "CPU")["Manufacturer"] = "AMD"
	sub(amd, "CPU")["Codename"] = "Zen 3"
	add(amd)
	// Laptop with integrated GPU, with/without Surface and HP.
	for _, name := range []string{"Generic", "HP ProBook 450", "SURFACE Laptop", "Dell XPS"} {
		for _, gpuType := range []string{"Integrated GPU", "Discrete GPU", "Integrated GPU (Intel)"} {
			r := baseReport()
			sub(r, "Motherboard")["Platform"] = "Laptop"
			sub(r, "Motherboard")["Name"] = name
			r["GPU"] = rep{"G0": rep{"Device Type": "Discrete GPU"}, "G1": rep{"Device Type": gpuType}}
			add(r)
			y := clone(r)
			y["GPU"] = rep{"G0": rep{"Device Type": gpuType}, "G1": rep{"Device Type": "Discrete GPU"}}
			add(y)
		}
	}
	// Disabled-device / system-device / input / network variants.
	for _, id := range []string{"8086-1C3A", "8086-1E3A", "8086-1C3B", "8086-1E3"} {
		for _, gen := range []string{"Sandy Bridge", "Ivy Bridge", "Haswell"} {
			r := baseReport()
			sub(r, "CPU")["Codename"] = gen
			r["System Devices"] = rep{"IMEI": rep{"Device ID": id}, "Other": rep{"Device ID": "10DE-1234"}}
			add(r)
		}
	}
	for _, hid := range append(append([]string{}, datasets.YogaHIDs...), "NOPE0000") {
		r := baseReport()
		r["System Devices"] = rep{"Y": rep{"Bus Type": "ACPI", "Device": hid}, "Z": rep{"Bus Type": "PCI", "Device": hid}}
		add(r)
	}
	for _, networks := range []rep{
		{}, {"N0": rep{"Bus Type": "USB"}}, {"N0": rep{"Bus Type": "USB"}, "N1": rep{"Bus Type": "PCI"}},
		{"N0": rep{"Bus Type": "PCI"}, "N1": rep{"Bus Type": "USB"}}, {"N0": rep{}},
	} {
		r := baseReport()
		r["Network"] = networks
		add(r)
	}
	noNet := baseReport()
	delete(noNet, "Network")
	add(noNet)
	noSys := baseReport()
	delete(noSys, "System Devices")
	add(noSys)
	noIn := baseReport()
	delete(noIn, "Input")
	add(noIn)
	for _, inputs := range []rep{
		{"K": rep{"Device Type": "Keyboard"}}, {"T": rep{"Device Type": "I2C Touchpad"}}, {"T": rep{}}, {"T": rep{"Device Type": "I2C"}, "K": rep{"Device Type": "PS/2"}},
	} {
		for _, name := range []string{"Generic", "SURFACE Pro"} {
			r := baseReport()
			r["Input"] = inputs
			sub(r, "Motherboard")["Name"] = name
			add(r)
		}
	}
	// Error shapes.
	for _, mut := range []func(r rep){
		func(r rep) { delete(r, "Motherboard") },
		func(r rep) { delete(r, "CPU") },
		func(r rep) { r["Motherboard"] = nil },
		func(r rep) { sub(r, "Motherboard")["Platform"] = nil },
		func(r rep) { sub(r, "Motherboard")["Platform"] = 5 },
		func(r rep) { delete(sub(r, "Motherboard"), "Platform") },
		func(r rep) { delete(sub(r, "Motherboard"), "Name") },
		func(r rep) { sub(r, "Motherboard")["Name"] = nil },
		func(r rep) { delete(sub(r, "Motherboard"), "Chipset") },
		func(r rep) { sub(r, "Motherboard")["Chipset"] = 7 },
		func(r rep) { delete(sub(r, "CPU"), "Manufacturer") },
		func(r rep) { delete(sub(r, "CPU"), "Codename") },
		func(r rep) { delete(sub(r, "CPU"), "Processor Name") },
		func(r rep) { sub(r, "CPU")["Processor Name"] = nil; sub(r, "CPU")["Codename"] = "Skylake-X" },
		func(r rep) { sub(r, "Motherboard")["Platform"] = "Laptop"; r["GPU"] = rep{} },
		func(r rep) { sub(r, "Motherboard")["Platform"] = "Laptop"; delete(r, "GPU") },
		func(r rep) { sub(r, "Motherboard")["Platform"] = "Laptop"; r["GPU"] = nil },
		func(r rep) { sub(r, "Motherboard")["Platform"] = "Laptop"; r["GPU"] = rep{"G": rep{}} },
		func(r rep) {
			sub(r, "Motherboard")["Platform"] = "Laptop"
			r["GPU"] = rep{"G": rep{"Device Type": nil}}
		},
		func(r rep) { sub(r, "Motherboard")["Platform"] = "Laptop"; r["GPU"] = rep{"G": "text"} },
		func(r rep) { r["Network"] = nil },
		func(r rep) { r["Network"] = rep{"N": "text"} },
		func(r rep) { r["System Devices"] = nil },
		func(r rep) { r["System Devices"] = rep{"D": "text"} },
		func(r rep) { r["System Devices"] = rep{"D": rep{}} },
		func(r rep) {
			r["System Devices"] = rep{"D": rep{"Device ID": "8086-1C3A"}}
			delete(sub(r, "CPU"), "Codename")
		},
		func(r rep) { r["Input"] = nil },
		func(r rep) { r["Input"] = rep{"I": rep{"Device Type": nil}} },
		func(r rep) { r["Input"] = rep{"I": "text"} },
	} {
		r := baseReport()
		mut(r)
		add(r)
	}

	disabledVariants := []rep{
		{},
		{"GPU": rep{"Bus Type": "PCI"}},
		{"WiFi": rep{"Bus Type": "USB"}},
		{"X": rep{}},
		{"X": rep{"Bus Type": nil}},
		{"X": "text"},
		{"A": rep{"Bus Type": "PCI Express"}, "B": rep{"Bus Type": "ACPI"}},
	}

	sc := scenario{steps: func(dir, out string) []map[string]any {
		var steps []map[string]any
		for i, r := range reports {
			steps = append(steps, map[string]any{"op": "select_patches", "report": r, "disabled": disabledVariants[i%len(disabledVariants)]})
		}
		// A few reports with every disabled variant.
		for _, d := range disabledVariants {
			steps = append(steps, map[string]any{"op": "select_patches", "report": baseReport(), "disabled": d})
		}
		steps = append(steps,
			map[string]any{"op": "select_patches", "report": baseReport(), "disabled": nil},
			map[string]any{"op": "select_patches", "report": nil, "disabled": rep{}},
			map[string]any{"op": "select_patches", "report": []any{}, "disabled": rep{}},
		)
		return steps
	}}
	py := runScenario(t, sc)
	distinct := map[string]bool{}
	for _, r := range py {
		if !r.Error {
			b, _ := json.Marshal(r.OK)
			distinct[string(b)] = true
		}
	}
	if len(distinct) < 40 {
		t.Errorf("the reports only produce %d distinct patch selections; the cases are too narrow", len(distinct))
	}
}

func TestParityCustomizePatchSelection(t *testing.T) {
	sc := scenario{steps: func(dir, out string) []map[string]any {
		return []map[string]any{
			{"op": "checked"},
			{"op": "customize", "inputs": []string{"b"}},
			{"op": "customize", "inputs": []string{"1", "b"}},
			{"op": "customize", "inputs": []string{"1,2, 3 ,x,,0,-1, 999,99999999999999999999,29,30", "B"}},
			{"op": "customize", "inputs": []string{"5, 5", "q"}},
			{"op": "customize", "inputs": []string{"Q"}},
			{"op": "customize", "inputs": []string{" 1", "1 ", "\t2\t", "b"}},
			{"op": "customize", "inputs": []string{"1;2", "1.5", "١", "b"}},
			{"op": "customize", "inputs": []string{"1"}},
			{"op": "checked"},
			{"op": "customize", "inputs": []string{}},
		}
	}}
	py := runScenario(t, sc)
	for i, r := range py {
		if i == 3 {
			if r.Error || r.OK == nil {
				t.Fatalf("oracle step %d: %+v", i, r)
			}
		}
	}
}

func TestParityTableLoading(t *testing.T) {
	files := map[string]dsdttest.Table{
		"DSDT.aml":   dsdttest.BuildTable(dsdttest.TableOpts{Sig: "DSDT", OEM: "ALASKA", ID: "A M I   ", BodySize: 300, Seed: 1}),
		"SSDT1.aml":  dsdttest.BuildTable(dsdttest.TableOpts{Sig: "SSDT", OEM: "PmRef ", ID: "Cpu0Ist ", BodySize: 100, Seed: 2, CRLF: true}),
		"SSDT10.aml": dsdttest.BuildTable(dsdttest.TableOpts{Sig: "SSDT", OEM: "PmRef ", ID: "CpuPm   ", BodySize: 80, Seed: 3}),
		"SSDT2.aml":  dsdttest.BuildTable(dsdttest.TableOpts{Sig: "SSDT", OEM: "PmRef ", ID: "Other   ", BodySize: 70, Seed: 4}),
	}
	all := []string{"DSDT.aml", "SSDT1.aml", "SSDT10.aml", "SSDT2.aml"}
	dsl := map[string]string{}
	for _, n := range all {
		dsl[n] = files[n].DSL
	}
	subTables := map[string][]byte{"DSDT.aml": files["DSDT.aml"].Raw}
	cases := []struct {
		name string
		sc   scenario
	}{
		{"whole directory", scenario{files: files, dsl: dsl, extra: map[string][]byte{"notes.txt": []byte("x"), "DSDT-Patched.aml": files["DSDT.aml"].Raw}}},
		{"nested ACPI folder", scenario{subdirs: map[string]map[string][]byte{"ACPI": subTables}, dsl: map[string]string{"DSDT.aml": files["DSDT.aml"].DSL}}},
		{"partial failure", scenario{files: files, dsl: map[string]string{"DSDT.aml": dsl["DSDT.aml"], "SSDT1.aml": dsl["SSDT1.aml"]}}},
		{"nothing disassembles", scenario{files: files}},
		{"retry without -da", scenario{files: files, dsl: dsl, env: map[string]string{"FAKE_FAIL_DA": "1"}}},
		{"no tables", scenario{extra: map[string][]byte{"a.txt": []byte("x")}}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			sc := c.sc
			sc.steps = func(dir, out string) []map[string]any {
				return []map[string]any{
					{"op": "read_tables", "path": dir, "inputs": []string{""}},
					{"op": "read_tables", "path": dir, "inputs": []string{}},
					{"op": "read_tables", "path": filepath.Join(dir, "DSDT.aml"), "inputs": []string{""}},
					{"op": "read_tables", "path": filepath.Join(dir, "DSDT-Patched.aml"), "inputs": []string{""}},
					{"op": "read_tables", "path": filepath.Join(dir, "SSDT1.aml"), "inputs": []string{""}},
					{"op": "read_tables", "path": filepath.Join(dir, "nope.aml"), "inputs": []string{""}},
					{"op": "read_tables", "path": filepath.Join(dir, "notes.txt"), "inputs": []string{""}},
					{"op": "read_tables", "path": "", "inputs": []string{}},
					{"op": "clear"},
					{"op": "ensure", "inputs": []string{dir}},
					{"op": "ensure", "inputs": []string{}},
					{"op": "ensure", "allow_any": true, "inputs": []string{}},
					{"op": "clear"},
					{"op": "ensure", "allow_any": true, "inputs": []string{"'" + dir + "'"}},
					{"op": "clear"},
					{"op": "ensure", "inputs": []string{"q"}},
					{"op": "clear"},
					{"op": "ensure", "inputs": []string{"Q", ""}},
					{"op": "clear"},
					{"op": "ensure", "inputs": []string{dir + "/nope", "", "q"}},
					{"op": "clear"},
					{"op": "ensure", "inputs": []string{"", "", "q"}},
					{"op": "clear"},
					{"op": "select_tables", "inputs": []string{filepath.Join(dir, "DSDT.aml")}},
					{"op": "set_dsdt"},
					{"op": "ensure", "inputs": []string{}},
					{"op": "select_tables", "inputs": []string{dir}},
					{"op": "unique_device", "base": "PCI0", "start": 0},
					{"op": "drop_cpu"},
				}
			}
			runScenario(t, sc)
		})
	}
}

func TestParityWriteSSDT(t *testing.T) {
	for _, mode := range []string{"ok", "fail", "norc", ""} {
		mode := mode
		t.Run("compile "+mode, func(t *testing.T) {
			sc := scenario{env: map[string]string{"FAKE_COMPILE": mode}}
			sc.steps = func(dir, out string) []map[string]any {
				return []map[string]any{
					{"op": "write_ssdt", "directory": out, "name": "SSDT-A", "content": "DefinitionBlock ()\n{\n}\n"},
					{"op": "write_ssdt", "directory": out, "name": "SSDT-B", "content": "x", "compile": false},
					{"op": "write_ssdt", "directory": filepath.Join(out, "nested", "deeper"), "name": "SSDT-C", "content": "ünï\n"},
					{"op": "write_ssdt", "directory": out, "name": "SSDT-A", "content": "overwritten"},
					{"op": "write_ssdt", "directory": out, "name": "sub/SSDT-D", "content": "bad path"},
				}
			}
			runScenario(t, sc)
		})
	}
}
