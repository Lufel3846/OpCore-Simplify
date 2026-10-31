package validator

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

func obj(kv ...any) *plist.Dict {
	d := plist.NewDict()
	for i := 0; i < len(kv); i += 2 {
		d.Set(kv[i].(string), kv[i+1])
	}
	return d
}

func validReport() *plist.Dict {
	pci := "PciRoot(0x0)/Pci(0x1C,0x0)/Pci(0x0,0x0)"
	return obj(
		"Motherboard", obj("Name", "Dell XPS", "Chipset", "Cannon Point-LP", "Platform", "Laptop"),
		"BIOS", obj("Version", "1.2", "Release Date", "01/02/2020", "System Type", "x64-based PC", "Firmware Type", "UEFI", "Secure Boot", "Disabled"),
		"CPU", obj("Manufacturer", "Intel", "Processor Name", "Intel Core i7-8565U", "Codename", "Whiskey Lake-U", "Core Count", "4", "CPU Count", "1", "SIMD Features", "SSE4.2"),
		"GPU", obj(
			"Intel UHD Graphics 620", obj("Manufacturer", "Intel", "Codename", "Whiskey Lake", "Device ID", "8086-3EA0", "Device Type", "Integrated GPU",
				"Subsystem ID", "0x08591028", "PCI Path", "PciRoot(0x0)/Pci(0x2,0x0)", "ACPI Path", "\\_SB.PCI0.GFX0", "Resizable BAR", "Disabled"),
			"NVIDIA GeForce MX130", obj("Manufacturer", "NVIDIA", "Codename", "GM108", "Device ID", "10DE-1D12", "Device Type", "Discrete GPU", "Subsystem ID", "08591028"),
		),
		"Monitor", obj("Monitor 1", obj("Connector Type", "eDP", "Resolution", "1920x1080", "Connected GPU", "Intel UHD Graphics 620")),
		"Network", obj(
			"Intel Wireless-AC 9560", obj("Bus Type", "PCI", "Device ID", "8086-9DF0", "Subsystem ID", "0x00348086", "PCI Path", pci, "ACPI Path", "_SB.PCI0.RP05.PXSX"),
			"Realtek USB NIC", obj("Bus Type", "USB", "Device ID", "0BDA-8153"),
		),
		"Sound", obj("Realtek Audio", obj("Bus Type", "PCI", "Device ID", "8086-9DC8", "Subsystem ID", "0x08591028", "Audio Endpoints", []any{"Speakers", "Microphone"}, "Controller Device ID", "10EC-0295")),
		"USB Controllers", obj("Intel xHCI", obj("Bus Type", "PCI", "Device ID", "8086-9DED", "Subsystem ID", "0x08591028", "PCI Path", "PciRoot(0x0)/Pci(0x14,0x0)", "ACPI Path", "_SB.PCI0.XHC")),
		"Input", obj("Precision Touchpad", obj("Bus Type", "ACPI", "Device", "DLL0870", "Device ID", "06CB-CE2D", "Device Type", "I2C Touchpad"), "Keyboard", obj("Bus Type", "ROOT")),
		"Storage Controllers", obj("NVMe", obj("Bus Type", "PCI", "Device ID", "144D-A808", "Subsystem ID", "0xA801144D", "PCI Path", pci, "ACPI Path", "_SB.PCI0.RP09", "Disk Drives", []any{"Samsung SSD 970", "WD Blue"})),
		"Biometric", obj("Fingerprint", obj("Bus Type", "USB", "Device", "Goodix", "Device ID", "27C6-538C")),
		"Bluetooth", obj("Intel BT", obj("Bus Type", "USB", "Device ID", "8087-0AAA")),
		"SD Controller", obj("Realtek SD", obj("Bus Type", "PCI", "Device ID", "10EC-522A", "Subsystem ID", "0x08591028", "PCI Path", pci, "ACPI Path", "_SB.PCI0.RP01")),
		"System Devices", obj("SMBus", obj("Bus Type", "PCI", "Device", "Intel SMBus", "Device ID", "8086-9DA3", "Subsystem ID", "0x08591028", "PCI Path", pci, "ACPI Path", "_SB.PCI0.SBUS")),
	)
}

func clone(v any) any {
	text, err := pyjson.Dump(v)
	if err != nil {
		panic(err)
	}
	out, err := pyjson.Load([]byte(text))
	if err != nil {
		panic(err)
	}
	return out
}

// path is a route into a document: strings are dict keys, ints list indexes.
type path []any

func walk(v any, p path, visit func(p path, v any)) {
	visit(append(path{}, p...), v)
	switch x := v.(type) {
	case *plist.Dict:
		for _, k := range x.Keys() {
			c, _ := x.Get(k)
			walk(c, append(p, k), visit)
		}
	case []any:
		for i, c := range x {
			walk(c, append(p, i), visit)
		}
	}
}

func getAt(root any, p path) any {
	cur := root
	for _, step := range p {
		switch s := step.(type) {
		case string:
			cur, _ = cur.(*plist.Dict).Get(s)
		case int:
			cur = cur.([]any)[s]
		}
	}
	return cur
}

// setAt replaces the value at p (nil p replaces the root) in a clone of root.
// A nil remove deletes the key or item instead.
func setAt(root any, p path, value any, remove bool) any {
	root = clone(root)
	if len(p) == 0 {
		return value
	}
	parent := getAt(root, p[:len(p)-1])
	switch s := p[len(p)-1].(type) {
	case string:
		d := parent.(*plist.Dict)
		if remove {
			d.Delete(s)
		} else {
			d.Set(s, value)
		}
	case int:
		l := parent.([]any)
		if remove {
			parent2 := getAt(root, p[:len(p)-2])
			var rebuilt []any
			rebuilt = append(rebuilt, l[:s]...)
			rebuilt = append(rebuilt, l[s+1:]...)
			switch pp := parent2.(type) {
			case *plist.Dict:
				pp.Set(p[len(p)-2].(string), rebuilt)
			case []any:
				pp[p[len(p)-2].(int)] = rebuilt
			}
		} else {
			l[s] = value
		}
	}
	return root
}

type fileCase struct {
	name string
	data []byte
}

func conv(v any) any {
	switch x := v.(type) {
	case []byte:
		return map[string]any{"hex": hex.EncodeToString(x)}
	case *plist.Dict:
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
	case float64:
		switch {
		case math.IsNaN(x):
			return map[string]any{"float": "nan"}
		case math.IsInf(x, 1):
			return map[string]any{"float": "inf"}
		case math.IsInf(x, -1):
			return map[string]any{"float": "-inf"}
		}
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

type oracleResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	Cleaned  any      `json:"cleaned"`
}

func runOracle(t *testing.T, paths []string) []oracleResult {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(paths)
	cmd := exec.Command("python3", "-I", "testdata/pyref_validator.py", root)
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle: %v\n%s", err, errb.String())
	}
	var res []oracleResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("oracle output: %v", err)
	}
	return res
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// compare runs both validators on the files and reports differences.
func compare(t *testing.T, paths []string, label func(i int) string) (valid, invalid int) {
	t.Helper()
	py := runOracle(t, paths)
	failures := 0
	for i, p := range paths {
		v := New()
		ok, errs, warns, cleaned := v.ValidateReport(p)
		want := py[i]
		if ok {
			valid++
		} else {
			invalid++
		}
		if ok != want.Valid || !reflect.DeepEqual(nonNil(errs), nonNil(want.Errors)) || !reflect.DeepEqual(nonNil(warns), nonNil(want.Warnings)) ||
			!reflect.DeepEqual(normalize(cleaned), want.Cleaned) {
			failures++
			if failures <= 12 {
				gc, _ := json.Marshal(normalize(cleaned))
				wc, _ := json.Marshal(want.Cleaned)
				t.Errorf("%s:\n go: valid=%v errors=%q warnings=%q\n py: valid=%v errors=%q warnings=%q\n go cleaned: %.300s\n py cleaned: %.300s",
					label(i), ok, errs, warns, want.Valid, want.Errors, want.Warnings, gc, wc)
			}
		}
	}
	if failures > 12 {
		t.Errorf("%d differences in total", failures)
	}
	return
}

func writeDoc(t *testing.T, dir, name string, v any) string {
	t.Helper()
	text, err := pyjson.Dump(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParityMutatedReports(t *testing.T) {
	dir := t.TempDir()
	base := validReport()
	mutations := []any{
		nil, int64(5), true, 1.5, "", " ", "bad", "\n", "Desktop\n", "Laptop", "UEFI\n", "0x", "12\n", "٣", "８x８", "1920x1080\n", "8086-3EA0\n", "8086-3ea0",
		"\\_SB.PCI0\n", "_SB.PCI0.A_1", "PciRoot(0x0)/Pci(0x2,0x0)\n", []any{}, []any{"x"}, []any{int64(1), "y", nil}, plist.NewDict(), obj("k", "v"),
	}
	var paths []string
	var labels []string
	add := func(label string, doc any) {
		paths = append(paths, writeDoc(t, dir, fmt.Sprintf("case%04d.json", len(paths)), doc))
		labels = append(labels, label)
	}
	add("valid base", base)
	var allPaths []path
	walk(base, nil, func(p path, v any) { allPaths = append(allPaths, p) })
	for _, p := range allPaths {
		desc := fmt.Sprint([]any(p))
		for mi, m := range mutations {
			add(fmt.Sprintf("replace %s with #%d (%v)", desc, mi, m), setAt(base, p, m, false))
		}
		if len(p) > 0 {
			add("remove "+desc, setAt(base, p, nil, true))
		}
		// An unknown key inside every dict.
		if d, ok := getAt(base, p).(*plist.Dict); ok {
			_ = d
			grown := clone(base)
			target := getAt(grown, p)
			target.(*plist.Dict).Set("Extra Key", "value")
			add("unknown key in "+desc, grown)
			grown2 := clone(base)
			getAt(grown2, p).(*plist.Dict).Set("Extra Obj", obj("a", int64(1)))
			add("unknown object in "+desc, grown2)
		}
	}
	valid, invalid := compare(t, paths, func(i int) string { return labels[i] })
	if invalid < 1500 || valid < 10 {
		t.Errorf("weak coverage: %d valid, %d invalid reports", valid, invalid)
	}
	t.Logf("%d reports compared (%d valid, %d invalid)", len(paths), valid, invalid)
}

func TestParityReportFiles(t *testing.T) {
	dir := t.TempDir()
	text, _ := pyjson.Dump(validReport())
	var paths []string
	var labels []string
	add := func(label, name string, data []byte) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		labels = append(labels, label)
	}
	add("valid", "valid.json", []byte(text))
	add("crlf", "crlf.json", []byte(strings.ReplaceAll(text, "\n", "\r\n")))
	add("cr only", "cr.json", []byte(strings.ReplaceAll(text, "\n", "\r")))
	add("bom", "bom.json", append([]byte("\xef\xbb\xbf"), text...))
	add("empty", "empty.json", nil)
	add("spaces", "spaces.json", []byte("  \n  "))
	add("invalid utf8", "badutf8.json", append([]byte(text), 0xff))
	add("truncated utf8", "trunc.json", []byte(`{"a": "`+"\xe2\x82"+`"}`))
	add("truncated", "truncated.json", []byte(text[:len(text)/2]))
	add("trailing comma", "comma.json", []byte(strings.Replace(text, "\"Laptop\"", "\"Laptop\",", 1)))
	add("extra data", "extra.json", []byte(text+"\n{}"))
	add("single quotes", "single.json", []byte(`{'a': 1}`))
	add("top-level list", "list.json", []byte(`[]`))
	add("top-level string", "string.json", []byte(`"text"`))
	add("top-level number", "number.json", []byte(`12`))
	add("top-level null", "null.json", []byte(`null`))
	add("top-level true", "true.json", []byte(`true`))
	add("top-level float", "float.json", []byte(`1.5`))
	add("empty object", "emptyobj.json", []byte(`{}`))
	add("nan", "nan.json", []byte(`{"Motherboard": NaN, "CPU": Infinity, "GPU": -Infinity}`))
	add("big int", "bigint.json", []byte(`{"Motherboard": 123456789012345678901234567890}`))
	add("duplicate keys", "dup.json", []byte(strings.Replace(text, "{\n", "{\n    \"BIOS\": 1,\n", 1)))
	add("control char", "ctrl.json", []byte("{\"Motherboard\": {\"Name\": \"a\x01b\"}}"))
	add("not json extension", "report.txt", []byte(text))
	add("no extension", "report", []byte(text))
	add("uppercase extension", "REPORT.JSON", []byte(text))
	add("plist extension", "report.plist", []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CPU</key><dict><key>Manufacturer</key><string>Intel</string></dict></dict></plist>`))
	add("plist list", "list.plist", []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><array><string>x</string></array></plist>`))
	// Not files: a missing path and a directory.
	paths = append(paths, filepath.Join(dir, "missing.json"), dir, filepath.Join(dir, "sub dir'q"))
	labels = append(labels, "missing", "directory", "missing with quote")
	if err := os.Mkdir(filepath.Join(dir, "folder.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths = append(paths, filepath.Join(dir, "folder.json"))
	labels = append(labels, "directory named .json")
	compare(t, paths, func(i int) string { return labels[i] })
}

func TestShowValidationReport(t *testing.T) {
	var b strings.Builder
	ShowValidationReport(&b, "r.json", false, []string{"e1", "e2"}, []string{"w1"})
	want := "\nValidation report for: r.json\n\nHardware report is not valid! Please check the errors and warnings below.\n" +
		"\n\033[31mErrors (2):\033[0m\n    1. e1\n    2. e2\n\n\033[33mWarnings (1):\033[0m\n    1. w1\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
}
