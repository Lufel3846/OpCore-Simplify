package acpiguru

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt/dsdttest"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

type pdResult struct {
	OK *struct {
		Value any               `json:"value"`
		Files map[string]string `json:"files"`
		// setup fields
		DSDT   any `json:"dsdt"`
		LPC    any `json:"lpc"`
		Tables any `json:"tables"`
	} `json:"ok"`
	Error   bool              `json:"error"`
	Message string            `json:"message"`
	Files   map[string]string `json:"files"`
}

func pdOracle(t *testing.T, steps []map[string]any, iasl string, env map[string]string) []pdResult {
	t.Helper()
	in, _ := json.Marshal(steps)
	cmd := exec.Command("python3", "-I", "testdata/pyref_patches_d.py", repoRoot(t), iasl)
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle failed: %v\n%s", err, errb.String())
	}
	var res []pdResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("oracle output: %v\n%s", err, out.String())
	}
	return res
}

func pdFiles(dir string) map[string]string {
	r := map[string]string{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		r[e.Name()] = hex.EncodeToString(b)
	}
	return r
}

func pdWipe(dir string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

func pdJSON(v any) any {
	b, _ := json.Marshal(v)
	var o any
	_ = json.Unmarshal(b, &o)
	return o
}

// pdGoValue converts Go results into the oracle shape.
func pdField(f DField) *plist.Dict {
	d := plist.NewDict()
	d.Set("name", f.Name)
	d.Set("size", f.Size)
	return d
}

func pdCall(g *Guru, name string) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	switch name {
	case "surface_laptop_special_patch":
		return g.SurfaceLaptopSpecialPatch()
	case "battery_status_patch":
		return g.BatteryStatusPatch()
	case "fix_apic_processor_id":
		return g.FixAPICProcessorID()
	case "disable_usb_hub_devices":
		return g.DisableUSBHubDevices()
	case "return_thermal_zone":
		return g.ReturnThermalZone()
	}
	panic("unknown " + name)
}

func pdText(g *Guru, fn string, args []any) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	s := func(i int) string { return args[i].(string) }
	n := func(i int) int { return int(args[i].(float64)) }
	switch fn {
	case "find_line_start":
		return g.FindLineStart(s(0), n(1)), nil
	case "extract_line":
		l, a, b := g.ExtractLine(s(0), n(1))
		return []any{l, a, b}, nil
	case "extract_block_content":
		return g.ExtractBlockContent(s(0), n(1)), nil
	case "parse_field_line":
		name, size, ok := g.ParseFieldLine(s(0))
		if !ok {
			return []any{nil, nil}, nil
		}
		return []any{name, size}, nil
	case "process_embedded_control_region":
		name, idx, ok := g.ProcessEmbeddedControlRegion(s(0), n(1))
		if !ok {
			return []any{nil, idx}, nil
		}
		return []any{name, idx}, nil
	case "process_field_definition":
		fields, idx := g.ProcessFieldDefinition(s(0), s(1), n(2))
		l := []any{}
		for _, f := range fields {
			l = append(l, pdField(f))
		}
		return []any{l, idx}, nil
	}
	panic("unknown " + fn)
}

type pdCase struct {
	name   string
	tables map[string]dsdttest.Table
	report rep
	steps  []map[string]any
	// text helpers need no tables
}

func pdRun(t *testing.T, c pdCase) {
	t.Helper()
	sc := scenario{files: c.tables, dsl: map[string]string{}, env: map[string]string{"FAKE_COMPILE": "ok"}}
	for n, tb := range c.tables {
		sc.dsl[n] = tb.DSL
	}
	dir, dslDir, iasl, base := writeScenario(t, sc)
	env := map[string]string{"FAKE_DSL_DIR": dslDir, "HOME": "/home/user", "FAKE_COMPILE": "ok"}
	mk := func(out string) []map[string]any {
		steps := []map[string]any{{"op": "setup", "path": dir, "out": out, "report": c.report}}
		return append(steps, c.steps...)
	}
	outPy, outGo := filepath.Join(base, "out-py"), filepath.Join(base, "out-go")
	// FAKE_COMPILE=ok only affects calls with a .dsl argument (the compile).
	py := pdOracle(t, mk(outPy), iasl, env)
	goSteps := prepare(mk(outGo))
	g := New(dsdt.New(iasl), func(string) (string, error) { return "", fmt.Errorf("EOF") })
	for k, v := range env {
		t.Setenv(k, v)
	}
	_ = os.MkdirAll(outGo, 0o755)
	for i, s := range goSteps {
		want := py[i]
		var got any
		var gerr error
		switch s["op"] {
		case "setup":
			if _, _, err := g.ReadACPITables(dir); err != nil {
				t.Fatal(err)
			}
			g.Dsdt = g.Acpi.GetDSDTOrOnly()
			if c.report != nil {
				g.HardwareReport = load(t, c.report)
			}
			g.ACPIDirectory = outGo
			g.LPCBusDevice, _ = g.GetLPCName(false, false, false)
			if want.Error {
				t.Fatalf("%s: oracle setup failed: %s", c.name, want.Message)
			}
			continue
		case "call":
			pdWipe(outGo)
			got, gerr = pdCall(g, s["name"].(string))
		case "text":
			pdWipe(outGo)
			got, gerr = pdText(g, s["fn"].(string), s["args"].([]any))
		case "no_dsdt":
			pdWipe(outGo)
			saved := g.Dsdt
			g.Dsdt = nil
			got, gerr = pdCall(g, "battery_status_patch")
			g.Dsdt = saved
		case "set_table":
			pdWipe(outGo)
			saved := g.Dsdt
			cp := *g.Dsdt
			cp.Table = s["table"].(string)
			g.Dsdt = &cp
			got, gerr = pdCall(g, "battery_status_patch")
			g.Dsdt = saved
		case "run_all":
			pdWipe(outGo)
			got, gerr = pdRunAll(g, stepStrings(s, "checked"))
		}
		if os.Getenv("PD_DUMP") != "" {
			b, _ := json.Marshal(want)
			t.Logf("%s %.300s", c.name, b)
		}
		label := fmt.Sprintf("%s step %d %v %v %v", c.name, i, s["op"], s["name"], s["fn"])
		if gerr != nil != want.Error {
			t.Errorf("%s: Go err=%v, Python error=%v (%s)", label, gerr, want.Error, want.Message)
			continue
		}
		files := pdFiles(outGo)
		if want.Error {
			if !reflect.DeepEqual(files, nz(want.Files)) {
				t.Errorf("%s: files on error differ\n go: %v\n py: %v", label, files, want.Files)
			}
			continue
		}
		if gv := normalize(got); !reflect.DeepEqual(gv, want.OK.Value) {
			gb, _ := json.Marshal(gv)
			wb, _ := json.Marshal(want.OK.Value)
			t.Errorf("%s (%.80v):\n go: %.900s\n py: %.900s", label, s["args"], gb, wb)
		}
		if !reflect.DeepEqual(files, nz(want.OK.Files)) {
			t.Errorf("%s: files differ\n go: %v\n py: %v", label, files, want.OK.Files)
		}
	}
}

func nz(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func pdRunAll(g *Guru, checked []string) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	for _, p := range g.Patches {
		p.Checked = false
		for _, n := range checked {
			if p.Name == n {
				p.Checked = true
			}
		}
	}
	var ecen any
	load, err := g.RunPatches(func(c bool) { ecen = c })
	if err != nil {
		return nil, err
	}
	patch, err := g.ApplyACPIPatches(load.Patch)
	if err != nil {
		return nil, err
	}
	cfg := plist.NewDict()
	cfg.Set("Add", orEmpty(load.Add))
	cfg.Set("Delete", orEmpty(load.Delete))
	cfg.Set("Patch", orEmpty(patch))
	out := plist.NewDict()
	out.Set("cfg", cfg)
	out.Set("ecen", ecen)
	return out, nil
}

func orEmpty(l []any) []any {
	if l == nil {
		return []any{}
	}
	return l
}

// ---- DSL builders -------------------------------------------------------------

func pdTable(sig string, seed int64, body int, lines []string) dsdttest.Table {
	oem, id := "ALASKA", "A M I   "
	if sig == "SSDT" {
		oem, id = "PmRef ", "Cpu0Ist "
	}
	return dsdttest.BuildTable(dsdttest.TableOpts{Sig: sig, OEM: oem, ID: id, BodySize: body, Seed: seed, Lines: lines})
}

func pdDSDTLines(inner ...string) []string {
	l := []string{
		`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`,
		`{`,
		`    Scope (\_SB)`,
		`    {`,
		`        Device (PCI0)`,
		`        {`,
		`            Name (_HID, EisaId ("PNP0A08"))  // _HID: Hardware ID`,
		`            Device (LPCB)`,
		`            {`,
		`                Name (_ADR, 0x001F0000)  // _ADR: Address`,
		`            }`,
		`        }`,
	}
	l = append(l, inner...)
	return append(l, `    }`, `}`)
}

func pdEC(region string, fieldLines ...string) []string {
	l := []string{
		`        Device (EC0_)`,
		`        {`,
		`            Name (_HID, EisaId ("PNP0C09"))  // _HID: Hardware ID`,
		`            OperationRegion (` + region + `, EmbeddedControl, Zero, 0xFF)`,
		`            Field (` + region + `, ByteAcc, Lock, Preserve)`,
		`            {`,
	}
	l = append(l, fieldLines...)
	return append(l, `            }`, `        }`)
}

func pdCat(parts ...[]string) []string {
	var o []string
	for _, p := range parts {
		o = append(o, p...)
	}
	return o
}

func pdApic(procIDs []string, extra ...string) []string {
	l := []string{
		`[000h 0000   4]                    Signature : "APIC"    [Multiple APIC Description Table (MADT)]`,
		`[004h 0004   4]                 Table Length : 00000068`,
	}
	for i, id := range procIDs {
		l = append(l,
			fmt.Sprintf("[%03Xh %04d   1]                Subtable Type : 00 [Processor Local APIC]", 0x2C+i*8, 44+i*8),
			fmt.Sprintf("[%03Xh %04d   1]                       Length : 08", 0x2D+i*8, 45+i*8),
			fmt.Sprintf("[%03Xh %04d   1]                 Processor ID : %s", 0x2E+i*8, 46+i*8, id),
			fmt.Sprintf("[%03Xh %04d   1]                Local Apic ID : %02X", 0x2F+i*8, 47+i*8, i),
		)
	}
	return append(l, extra...)
}

func pdProcs(ids ...string) []string {
	l := []string{`    Scope (\_PR)`, `    {`}
	for i, id := range ids {
		l = append(l, fmt.Sprintf("        Processor (CPU%d, %s, 0x00001810, 0x06)", i, id), `        {`, `        }`)
	}
	return append(l, `    }`)
}

func pdCall1(name string) map[string]any { return map[string]any{"op": "call", "name": name} }

func pdBase() map[string]dsdttest.Table {
	return map[string]dsdttest.Table{"DSDT.aml": pdTable("DSDT", 1, 40, pdDSDTLines())}
}

func pdTextStep(fn string, args ...any) map[string]any {
	return map[string]any{"op": "text", "fn": fn, "args": args}
}

func TestPDTextHelpers(t *testing.T) {
	texts := []string{
		"", "abc", "a\nb\nc", "\n", "\n\n", "x\r\ny\r\n", "  line one  \n\tline two\t\n   ", "no newline at all",
		"a{b}c", "{", "}", "{}", "{{}}", "x { a { b } c } y { z }", "{ unbalanced { }", "  {\n  A, 8,\n  }\nrest",
		"// }\n{ // {\n }", "un\n{e}\n",
	}
	var steps []map[string]any
	for _, tx := range texts {
		for idx := -3; idx <= len(tx)+1; idx++ {
			steps = append(steps, pdTextStep("find_line_start", tx, idx), pdTextStep("extract_line", tx, idx),
				pdTextStep("extract_block_content", tx, idx))
		}
	}
	for _, line := range []string{
		"", "A", "A,8", "A, 8", "A,8,", " A ,  16 , ", "A,8 // c", "A,8// c", "// only", "A, 8 // x, 9", "A,x", "A,", ",8", ", 32,",
		"A,0x10", "A,+8", "A,-8", "A, 1_0", "A,1__0", "A,_1", "A,1_", "A,٩", "A,۱۲", "A,١٠ ,", "A,\U0001d7d3",
		"A, 9 ", "A,٣", "A,8.0", "Offset (0x10),", "  ,  ,  ",
		"Offset (0x04), // x", "A , 8", "A,\x1f8", "A,²", "A, ８",
	} {
		steps = append(steps, pdTextStep("parse_field_line", line))
	}
	tables := []string{
		"", "EmbeddedControl", "OperationRegion (ECF2, EmbeddedControl, Zero, 0xFF)\nField (ECF2, ByteAcc)\n{\n A, 8,\n B, 16\n}\n",
		"x\n  OperationRegion (ECF2, EmbeddedControl, 0x00, 0xFF)  // c\nEmbeddedControl again",
		"EmbeddedControl\n", "(EmbeddedControl", "OperationRegion (A , EmbeddedControl", "OperationRegion (, EmbeddedControl, 0)\n",
		"OperationRegion ( ECF2 , EmbeddedControl, 0)\n", "// EmbeddedControl\nOperationRegion (EC, EmbeddedControl, 0)", "a\r\nOperationRegion (R, EmbeddedControl, 0)",
	}
	for _, tb := range tables {
		for idx := -2; idx <= len(tb)+2; idx++ {
			steps = append(steps, pdTextStep("process_embedded_control_region", tb, idx))
		}
	}
	fieldTables := []string{
		"Field (ECF2, ByteAcc, Lock, Preserve)\n{\n    A, 8,\n    B, 16,\n    , 32,\n}\n",
		"Field (ECF2, ByteAcc)\n{\n  Offset (0x04),\n  X, 3 // c\n}\nField (ECF2, A)\n{\n Y, 12\n}\n",
		"Field (ECF2, ByteAcc)", "Field (ECF2, ByteAcc)\n", "Field (ECF2, ByteAcc)\n{\n A, 8", "Field (ECF2x, A)\n{\n A, 40\n}\n",
		"Field (ECF2, A) { A, 9 }\n", "Field (ECF2, A)\n{\n{\n A, 9\n}\n}\n", "Field (ECF2, A)\n{\r\n A, 9\r\n\v B, 10\x1c C, 11\x1d D, 12\x1e E, 13\f F, 14\n}\n",
		"Field (ECF2, A)\n{\n  A, 8,  }\n", "uni Field (ECF2, A)\n{\n e, 8\n}\n",
	}
	for _, tb := range fieldTables {
		for _, region := range []string{"ECF2", "EC", "ECF", "", "X"} {
			for idx := -2; idx <= len(tb)+1; idx += 3 {
				steps = append(steps, pdTextStep("process_field_definition", tb, region, idx))
			}
		}
	}
	pdRun(t, pdCase{name: "text", tables: pdBase(), steps: steps})
}

func TestPDBattery(t *testing.T) {
	eight := []string{`                Offset (0x04), `, `                ECST,   8, `, `                ECS2,   8, `, `                ECS3,   8`}
	wide := []string{`                ECST,   8, `, `                BST0,   16, // battery`, `                BST1,   8`}
	cases := map[string][]string{
		"all8":        pdDSDTLines(pdEC("ECF2", eight...)...),
		"wide16":      pdDSDTLines(pdEC("ECF2", wide...)...),
		"noEC":        pdDSDTLines(),
		"reserved":    pdDSDTLines(pdEC("ECF2", `                ECST,   8, `, `                ,   32, `, `                Offset (0x10), `, `                ,   16`)...),
		"badsize":     pdDSDTLines(pdEC("ECF2", `                ECST,   0x20, `, `                ECS2,   8, `, `                ECS3,   1_6,`, `                ECS4,   ٩`)...),
		"huge":        pdDSDTLines(pdEC("ECF2", `                A,   99999999999999999999999`)...),
		"hugeneg":     pdDSDTLines(pdEC("ECF2", `                A,   -99999999999999999999999`)...),
		"nonascii":    pdDSDTLines(`        // ünï ü`, `        Name (STR, "é")`, pdEC("ECF2", `                A,   ٩, // ü`, `                B,   16`)[0], pdEC("ECF2", `                A,   ٩, // ü`, `                B,   16`)[1]),
		"size9":       pdDSDTLines(pdEC("ECF2", `                A,   9`)...),
		"size8hex":    pdDSDTLines(pdEC("ECF2", `                A,   8, `, `                B, 8 // 99`)...),
		"comment":     pdDSDTLines(pdEC("ECF2", `                // BIG,   64, `, `                A,   8`)...),
		"twoRegions1": pdDSDTLines(pdCat(pdEC("ECF2", eight...), pdEC("ECF3", wide...))...),
		"twoRegions2": pdDSDTLines(pdCat(pdEC("ECF2", eight...), pdEC("ECF3", eight...))...),
		"twoFields":   pdDSDTLines(append(pdEC("ECF2", eight...), `        Field (ECF2, ByteAcc, Lock, Preserve)`, `        {`, `            Offset (0x40), `, `            BIG, 32`, `        }`)...),
		"prefix":      pdDSDTLines(append(pdEC("ECF", eight...), `        Field (ECF2, ByteAcc, Lock, Preserve)`, `        {`, `            BIG, 32`, `        }`)...),
		"onlyRegion":  pdDSDTLines(`        OperationRegion (ECF2, EmbeddedControl, Zero, 0xFF)`),
		"commentEC":   pdDSDTLines(`        // EmbeddedControl is not used`, `        OperationRegion (SMI0, SystemMemory, 0x1000, 0x10)`),
		"noParen":     pdDSDTLines(`        Name (XXXX, "EmbeddedControl")`),
		"emptyName":   pdDSDTLines(`        OperationRegion (, EmbeddedControl, 0, 1)`, `        Field (, ByteAcc)`, `        {`, `            A, 16`, `        }`),
		"noBrace":     pdDSDTLines(`        OperationRegion (ECF2, EmbeddedControl, 0, 1)`, `        Field (ECF2, ByteAcc)`),
		"unbalanced":  pdDSDTLines(`        OperationRegion (ECF2, EmbeddedControl, 0, 1)`, `        Field (ECF2, ByteAcc)`, `        {`, `            A, 16`),
		"emptyField":  pdDSDTLines(`        OperationRegion (ECF2, EmbeddedControl, 0, 1)`, `        Field (ECF2, ByteAcc)`, `        {`, `        }`, `        Field (ECF2, ByteAcc)`, `        {`, `            A, 16`, `        }`),
		"inlineBrace": pdDSDTLines(`        OperationRegion (ECF2, EmbeddedControl, 0, 1)`, `        Field (ECF2, ByteAcc) {`, `            A, 16`, `        }`),
		"nested":      pdDSDTLines(`        OperationRegion (ECF2, EmbeddedControl, 0, 1)`, `        Field (ECF2, ByteAcc)`, `        {`, `            A, 4,`, `            {`, `            B, 32 }`, `        }`),
	}
	for name, lines := range cases {
		for _, body := range []int{16, 200} {
			pdRun(t, pdCase{name: fmt.Sprintf("battery-%s-%d", name, body), tables: map[string]dsdttest.Table{"DSDT.aml": pdTable("DSDT", 3, body, lines)},
				steps: []map[string]any{pdCall1("battery_status_patch"), {"op": "no_dsdt"}}})
		}
	}
	// A DSDT text fed directly (including hand-made ones that BuildTable would reshape).
	var steps []map[string]any
	for _, tb := range []string{
		"", "EmbeddedControl", "EmbeddedControl EmbeddedControl EmbeddedControl",
		"(EmbeddedControl\n)Field (X\n{ A, 16 }",
		"OperationRegion (E, EmbeddedControl, 0, 1)\nField (E, A)\n{\n A, 16 }\n",
		"OperationRegion (E, EmbeddedControl, 0, 1)\nField (E, A)\n{\n A, 8 }\nField (E, A)\n{ A, 8 }\nField (E, A)\n{ A, 9 }",
		"x EmbeddedControl\nOperationRegion (E,EmbeddedControl,0,1)\nField (E, A)\n{ B, 33 }",
		"OperationRegion (E, EmbeddedControl, 0, 1)", "OperationRegion (E, EmbeddedControl, 0, 1)\nField (E, A)",
		"OperationRegion (E, EmbeddedControl, 0, 1)\nOperationRegion (F, EmbeddedControl, 0, 1)\nField (F, A)\n{\n A, 16 }\n",
		"OperationRegion (E, EmbeddedControl, 0, 1)\nField (E, A)\n{\n A, 8 }\nField (E, A)", "OperationRegion (E, EmbeddedControl, 0, 1)\nField (E, A)\n{\n A, 8 }\nField (E, A)\n",
	} {
		steps = append(steps, map[string]any{"op": "set_table", "table": tb})
	}
	for _, sep := range []string{"\n", "\r\n", "\r", "\v", "\f", "\x1c", "\x1d", "\x1e", "\u0085", "\u2028", "\u2029", "\x1f", "\u00a0", " "} {
		steps = append(steps, map[string]any{"op": "set_table", "table": "OperationRegion (E, EmbeddedControl, 0, 1)\nField (E, A)\n{" + sep + "A, 8" + sep + "B, 9" + sep + "}\n"})
	}
	pdRun(t, pdCase{name: "battery-direct", tables: pdBase(), steps: steps})
}

func TestPDApic(t *testing.T) {
	type c struct {
		name  string
		dsdt  []string
		apic  []string
		ssdt  []string
		extra bool
	}
	cs := []c{
		{"match", pdDSDTLines(pdProcs("0x01", "0x02")...), pdApic([]string{"01", "02"}), nil, false},
		{"differ", pdDSDTLines(pdProcs("0x01", "0x02")...), pdApic([]string{"00", "01"}), nil, false},
		{"second-differs", pdDSDTLines(pdProcs("0x01", "0x02")...), pdApic([]string{"01", "03"}), nil, false},
		{"fewer-procs", pdDSDTLines(pdProcs("0x01")...), pdApic([]string{"00", "01", "02"}), nil, false},
		{"more-procs", pdDSDTLines(pdProcs("0x01", "0x02", "0x03")...), pdApic([]string{"00"}), nil, false},
		{"no-procs", pdDSDTLines(), pdApic([]string{"00"}), nil, false},
		{"no-subtables", pdDSDTLines(pdProcs("0x01")...), pdApic(nil), nil, false},
		{"ssdt-procs", pdDSDTLines(), pdApic([]string{"00", "05"}), pdDSDTLines(pdProcs("0x07", "0x08")...), false},
		{"unknown-toggle", pdDSDTLines(pdProcs("0x01", "0x02")...), pdApic([]string{"00"}, `[030h 0048   1]                Subtable Type : 7F [Unknown]`, `[031h 0049   1]                Subtable Type : 00 [Processor Local APIC]`, `[032h 0050   1]                       Length : 08`, `[033h 0051   1]                 Processor ID : 55`, `[034h 0052   1]                Unknown`, `[038h 0056   1]                Subtable Type : 00 [Processor Local APIC]`, `[038h 0056   1]                Length : 08`, `[038h 0056   1]                Processor ID : 09`), nil, false},
		{"truncated", pdDSDTLines(pdProcs("0x01")...), pdApic([]string{"00"}, `[030h 0048   1]                Subtable Type : 00 [Processor Local APIC]`, `[031h 0049   1]                       Length : 08`), nil, false},
		{"truncated2", pdDSDTLines(pdProcs("0x01")...), pdApic([]string{"00"}, `[030h 0048   1]                Subtable Type : 00 [Processor Local APIC]`), nil, false},
		{"short-id", pdDSDTLines(pdProcs("0x01")...), pdApic([]string{"00"}, `Subtable Type : 00 [Processor Local APIC]`, `x`, `7`), nil, false},
		{"two-char-line", pdDSDTLines(pdProcs("0x01")...), pdApic(nil, `Subtable Type : 00 [Processor Local APIC]`, `x`, `AB`), nil, false},
		{"one-char-line", pdDSDTLines(pdProcs("0x01")...), pdApic(nil, `Subtable Type : 00 [Processor Local APIC]`, `x`, `7`), nil, false},
		{"wide-proc-id", pdDSDTLines(pdProcs("0x0001")...), pdApic([]string{"00"}), nil, false},
		{"proc-no-comma", pdDSDTLines(`    Scope (\_PR)`, `    {`, `        Processor (CPU0)`, `        {`, `        }`, `    }`), pdApic([]string{"00"}), nil, false},
		{"proc-one-char", pdDSDTLines(`    Scope (\_PR)`, `    {`, `        Processor (CPU0, 1, 0x1810, 6)`, `        {`, `        }`, `    }`), pdApic([]string{"01"}), nil, false},
		{"not-processor-apic", pdDSDTLines(pdProcs("0x01")...), pdApic(nil, `Subtable Type : 01 [IOAPIC]`, `x`, `Processor ID : 01`), nil, false},
		{"processor-apic-in-ssdt-too", pdDSDTLines(pdProcs("0x01")...), pdApic([]string{"01"}), pdDSDTLines(pdProcs("0x09")...), false},
		{"differ-ssdt-first", pdDSDTLines(pdProcs("0x03", "0x04")...), pdApic([]string{"01", "02"}), pdDSDTLines(pdProcs("0x01", "0x02")...), false},
	}
	for _, k := range cs {
		tabs := map[string]dsdttest.Table{"DSDT.aml": pdTable("DSDT", 5, 80, k.dsdt)}
		if k.apic != nil {
			tabs["APIC.aml"] = dsdttest.BuildTable(dsdttest.TableOpts{Sig: "APIC", OEM: "ALASKA", ID: "A M I   ", BodySize: 16, Seed: 9, Lines: k.apic})
		}
		if k.ssdt != nil {
			tabs["SSDT-1.aml"] = pdTable("SSDT", 6, 80, k.ssdt)
		}
		pdRun(t, pdCase{name: "apic-" + k.name, tables: tabs, steps: []map[string]any{pdCall1("fix_apic_processor_id")}})
	}
	pdRun(t, pdCase{name: "apic-absent", tables: map[string]dsdttest.Table{"DSDT.aml": pdTable("DSDT", 5, 80, pdDSDTLines(pdProcs("0x01")...))},
		steps: []map[string]any{pdCall1("fix_apic_processor_id")}})
}

func TestPDUsbThermalSurface(t *testing.T) {
	staMethod := func(dev string) []string {
		return []string{`            Method (_STA, 0, NotSerialized)  // _STA: Status`, `            {`, `                Return (0x0F)`, `            }`}
	}
	xhc := func(rhub string, sta bool, extra ...string) []string {
		l := []string{`        Device (XHC)`, `        {`, `            Name (_ADR, 0x00140000)  // _ADR: Address`, `            Device (` + rhub + `)`, `            {`, `                Name (_ADR, Zero)  // _ADR: Address`}
		if sta {
			l = append(l, `                Method (_STA, 0, NotSerialized)  // _STA: Status`, `                {`, `                    Return (0x0F)`, `                }`)
		}
		l = append(l, extra...)
		return append(l, `            }`, `        }`)
	}
	_ = staMethod
	usb := map[string][]string{
		"rhub-method":  pdDSDTLines(xhc("RHUB", true)...),
		"rhub-nosta":   pdDSDTLines(xhc("RHUB", false)...),
		"rhub-nameSta": pdDSDTLines(xhc("RHUB", false, `                Name (_STA, 0x0F)  // _STA: Status`)...),
		"rhub-xsta":    pdDSDTLines(xhc("RHUB", false, `                Method (XSTA, 0, NotSerialized)`, `                {`, `                    Return (0x0F)`, `                }`)...),
		"rhub-both":    pdDSDTLines(xhc("RHUB", true, `                Method (XSTA, 0, NotSerialized)`, `                {`, `                }`)...),
		"hubn":         pdDSDTLines(xhc("HUBN", true)...),
		"urth":         pdDSDTLines(xhc("URTH", true)...),
		"all-three":    pdDSDTLines(pdCat(xhc("RHUB", true), xhc("HUBN", false), xhc("URTH", true))...),
		"two-rhub":     pdDSDTLines(pdCat(xhc("RHUB", true), []string{`        Device (XHC2)`, `        {`, `            Device (RHUB)`, `            {`, `                Method (_STA, 0, NotSerialized)`, `                {`, `                    Return (0x0F)`, `                }`, `            }`, `        }`})...),
		"none":         pdDSDTLines(),
		"lookalike":    pdDSDTLines(`        Device (RHUB2)`, `        {`, `        }`),
		"underscore":   pdDSDTLines(`        Device (XHC_)`, `        {`, `            Device (RHU_)`, `            {`, `                Method (_STA, 0, NotSerialized)`, `                {`, `                    Return (0x0F)`, `                }`, `            }`, `        }`),
		"hub-in-scope": pdDSDTLines(`        Scope (_SB.PCI0.XHC)`, `        {`, `            Device (HUBN)`, `            {`, `                Method (_STA, 0, NotSerialized)`, `                {`, `                    Return (0x0F)`, `                }`, `            }`, `        }`),
		"hubn-in-ssdt": pdDSDTLines(),
	}
	for name, lines := range usb {
		for _, body := range []int{16, 200, 330} {
			tabs := map[string]dsdttest.Table{"DSDT.aml": pdTable("DSDT", 11, body, lines)}
			if name == "hubn-in-ssdt" {
				tabs["SSDT-1.aml"] = pdTable("SSDT", 12, 100, pdDSDTLines(xhc("HUBN", true)...))
			}
			pdRun(t, pdCase{name: fmt.Sprintf("usb-%s-%d", name, body), tables: tabs,
				steps: []map[string]any{pdCall1("disable_usb_hub_devices"), pdCall1("surface_laptop_special_patch")}})
		}
	}
	wq := func(zone string) []string {
		return []string{`        ThermalZone (` + zone + `)`, `        {`, `            Method (WQBI, 1, Serialized)`, `            {`, `                Local0 = Arg0`, `            }`, `            Method (_TMP, 0, NotSerialized)  // _TMP: Temperature`, `            {`, `                Local0 = 0x0B80`, `            }`, `        }`}
	}
	tz := map[string][2][]string{
		"none":            {pdDSDTLines(), nil},
		"dsdt-wqbi":       {pdDSDTLines(append(wq("TZ00"), `        Device (WMI1)`, `        {`, `            Method (WQBI, 1, NotSerialized)`, `            {`, `            }`, `        }`)...), nil},
		"ssdt-wqbi":       {pdDSDTLines(), pdDSDTLines(`        Method (WQBI, 1, NotSerialized)`, `        {`, `        }`)},
		"both":            {pdDSDTLines(`        Method (WQBI, 1, NotSerialized)`, `        {`, `        }`), pdDSDTLines(`        Method (WQBI, 1, NotSerialized)`, `        {`, `        }`)},
		"name-not-method": {pdDSDTLines(`        Name (WQBI, Zero)`), nil},
		"device-wqbi":     {pdDSDTLines(`        Device (WQBI)`, `        {`, `        }`), nil},
		"other-method":    {pdDSDTLines(`        Method (WQBJ, 1, NotSerialized)`, `        {`, `        }`), nil},
		"lowercase-ish":   {pdDSDTLines(`        Method (_SB.WQBI, 1, NotSerialized)`, `        {`, `        }`), nil},
	}
	for name, v := range tz {
		tabs := map[string]dsdttest.Table{"DSDT.aml": pdTable("DSDT", 21, 100, v[0])}
		if v[1] != nil {
			tabs["SSDT-1.aml"] = pdTable("SSDT", 22, 100, v[1])
		}
		pdRun(t, pdCase{name: "thermal-" + name, tables: tabs, steps: []map[string]any{pdCall1("return_thermal_zone")}})
	}
}

func TestPDRunAll(t *testing.T) {
	ec := pdEC("ECF2", `                ECST,   8, `, `                BST0,   16`)
	usb := []string{`        Device (XHC)`, `        {`, `            Device (RHUB)`, `            {`, `                Method (_STA, 0, NotSerialized)`, `                {`, `                    Return (0x0F)`, `                }`, `            }`, `        }`}
	wq := []string{`        Method (WQBI, 1, NotSerialized)`, `        {`, `        }`}
	dsdtL := pdDSDTLines(pdCat(ec, usb, wq)...)
	base := func() map[string]dsdttest.Table {
		return map[string]dsdttest.Table{
			"DSDT.aml":   pdTable("DSDT", 31, 300, dsdtL),
			"SSDT-1.aml": dsdttest.BuildTable(dsdttest.TableOpts{Sig: "SSDT", OEM: "PmRef ", ID: "CpuPm   ", BodySize: 60, Seed: 32, Lines: pdDSDTLines()}),
			"SSDT-2.aml": dsdttest.BuildTable(dsdttest.TableOpts{Sig: "SSDT", OEM: "PmRef ", ID: "Cpu0Ist ", BodySize: 60, Seed: 33, Lines: pdDSDTLines()}),
		}
	}
	withAPIC := func() map[string]dsdttest.Table {
		m := base()
		m["APIC.aml"] = dsdttest.BuildTable(dsdttest.TableOpts{Sig: "APIC", OEM: "ALASKA", ID: "A M I   ", BodySize: 16, Seed: 9, Lines: pdApic([]string{"01"})})
		return m
	}
	sets := [][]string{
		{}, {"BATP"}, {"Surface Patch"}, {"USB Reset"}, {"WMIS"}, {"PM (Legacy)"},
		{"BATP", "Surface Patch", "USB Reset", "WMIS", "PM (Legacy)"},
		{"WMIS", "BATP", "USB Reset", "Surface Patch"},
		{"APIC", "WMIS"}, {"WMIS", "APIC"},
	}
	for i, set := range sets {
		for tn, tabs := range map[string]map[string]dsdttest.Table{"plain": base(), "apic": withAPIC()} {
			pdRun(t, pdCase{name: fmt.Sprintf("runall-%d-%s", i, tn), tables: tabs, report: baseReport(),
				steps: []map[string]any{{"op": "run_all", "checked": set}}})
		}
	}
	var all []string
	for _, p := range New(nil, nil).Patches {
		if p.Name != "APIC" {
			all = append(all, p.Name)
		}
	}
	pdRun(t, pdCase{name: "runall-everything", tables: base(), report: baseReport(), steps: []map[string]any{{"op": "run_all", "checked": all}}})
	_ = strings.ToLower
}

// pdHexTable builds a table whose listing carries real hex lines: each "@hex"
// line in lines is replaced by the next 16-byte chunk (the last chunk is
// omitted from the listing like the disassembler does).
func pdHexTable(sig string, lines []string, chunks [][]byte) dsdttest.Table {
	var body []byte
	for _, c := range chunks {
		body = append(body, c...)
	}
	hdr := make([]byte, 36)
	copy(hdr, sig)
	hdr[4], hdr[5] = byte(36+len(body)), byte((36+len(body))>>8)
	hdr[8] = 2
	copy(hdr[10:], "ALASKA")
	copy(hdr[16:], "A M I   ")
	raw := append(hdr, body...)
	var sb strings.Builder
	sb.WriteString("/*\n * Intel ACPI Component Architecture\n */\n")
	k := 0
	for _, l := range lines {
		if l != "@hex" {
			sb.WriteString(l + "\n")
			continue
		}
		if k >= len(chunks)-1 {
			continue
		}
		parts := make([]string, 16)
		for i, b := range chunks[k] {
			parts[i] = fmt.Sprintf("%02X", b)
		}
		sb.WriteString(fmt.Sprintf("    %04X: %s\n", 0x24+16*k, strings.Join(parts, " ")))
		k++
	}
	sb.WriteString("\nTable Header:\n    Signature        : \"" + sig + "\"\n")
	return dsdttest.Table{Raw: raw, DSL: sb.String()}
}

func pdStaChunk(i int, dup bool) []byte {
	c := make([]byte, 16)
	for j := range c {
		c[j] = byte(0x50 + i)
	}
	copy(c[4:], "_STA")
	if dup {
		copy(c[10:], "_STA")
	}
	return c
}

func TestPDUsbWithUniquePads(t *testing.T) {
	sta := func(dev string, n int) []string {
		return []string{`            Device (` + dev + `)`, `            {`, `                Method (_STA, 0, NotSerialized)  // _STA: Status`, `@hex`, `                {`, `                    Return (0x0F)`, `                }`, `            }`}
	}
	wrap := func(inner ...[]string) []string {
		l := []string{`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`, `{`, `    Scope (\_SB)`, `    {`, `        Device (PCI0)`, `        {`, `            Name (_HID, EisaId ("PNP0A08"))`, `        }`, `        Device (XHC)`, `        {`}
		for _, i := range inner {
			l = append(l, i...)
		}
		return append(l, `        }`, `    }`, `}`)
	}
	chunks := func(n int, dup bool) [][]byte {
		var c [][]byte
		for i := 0; i < n; i++ {
			c = append(c, pdStaChunk(i, dup))
		}
		return append(c, pdStaChunk(99, false))
	}
	cases := map[string][]string{
		"one":    wrap(sta("RHUB", 0)),
		"three":  wrap(sta("RHUB", 0), sta("HUBN", 1), sta("URTH", 2)),
		"hubn2":  wrap(sta("HUBN", 0), sta("HUBN", 1)),
		"xsta":   wrap(sta("RHUB", 0), []string{`            Device (HUBN)`, `            {`, `                Method (XSTA, 0, NotSerialized)`, `                {`, `                }`, `            }`}),
		"nested": wrap([]string{`            Device (RHUB)`, `            {`}, sta("HUBN", 1), []string{`            }`}),
	}
	for name, lines := range cases {
		for _, dup := range []bool{false, true} {
			tabs := map[string]dsdttest.Table{"DSDT.aml": pdHexTable("DSDT", lines, chunks(3, dup))}
			pdRun(t, pdCase{name: fmt.Sprintf("usbhex-%s-%v", name, dup), tables: tabs,
				steps: []map[string]any{pdCall1("disable_usb_hub_devices"), {"op": "run_all", "checked": []string{"USB Reset", "BATP", "WMIS"}}}})
		}
	}
}
