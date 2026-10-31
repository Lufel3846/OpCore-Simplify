package acpiguru

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

type pbMap = map[string]any

// pbCase is one comparison: the synthetic tables, the inputs and the method.
type pbCase struct {
	name   string
	method string
	// tables maps the .aml file name to DSL lines; a line "@HEX b1 b2 ..." becomes
	// a hex dump line. The signature is DSDT for the name DSDT.aml, else SSDT.
	tables       map[string][]string
	report       any
	disabled     any
	model        string
	noLPC        bool   // lpc_bus_device = lpcValue instead of get_lpc_name()
	lpcValue     string // used with noLPC
	lines        []string
	methodPath   []any
	hasDisabled  bool
	expectNotNil bool
}

type pbResult struct {
	OK       any               `json:"ok"`
	Error    bool              `json:"error"`
	Message  string            `json:"message"`
	Files    map[string]string `json:"files"`
	Lpc      any               `json:"lpc"`
	Disabled any               `json:"disabled"`
}

// pbTable builds a table whose listing is the given lines (see pbCase.tables).
func pbTable(name string, lines []string, seed int64) dsdttest.Table {
	var out []string
	var body []byte
	addr := 0x24
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		if strings.HasPrefix(strings.TrimSpace(l), "@HEX") {
			var parts []string
			for _, f := range strings.Fields(strings.TrimSpace(l))[1:] {
				parts = append(parts, strings.ToUpper(f))
				b, _ := hex.DecodeString(f)
				body = append(body, b...)
			}
			out = append(out, fmt.Sprintf("    %04X: %s", addr, strings.Join(parts, " ")))
			addr += 16
			continue
		}
		out = append(out, l)
	}
	sig, id := "SSDT", "Tbl"+fmt.Sprint(seed)
	if name == "DSDT.aml" {
		sig, id = "DSDT", "A M I   "
	}
	tb := dsdttest.BuildTable(dsdttest.TableOpts{Sig: sig, OEM: "ALASKA", ID: (id + "        ")[:8], BodySize: 12, Seed: seed, Lines: out})
	// The raw bytes carry the hex dump lines, so the unique-pad search of the
	// original (which counts occurrences in the raw table) sees the same data.
	raw := append(append([]byte{}, tb.Raw[:36]...), body...)
	raw = append(raw, tb.Raw[36:]...)
	binary.LittleEndian.PutUint32(raw[4:8], uint32(len(raw)))
	tb.Raw = raw
	return tb
}

func pbOracle(t *testing.T, cases []map[string]any, iasl string) []pbResult {
	t.Helper()
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../../..")
	cmd := exec.Command("python3", "-I", "testdata/pyref_patches_b.py", root, iasl)
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("oracle failed: %v\n%s", err, errb.String())
	}
	var res []pbResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("oracle output: %v\n%s", err, out.String())
	}
	return res
}

func pbConv(v any) any {
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
			pairs = append(pairs, []any{k, pbConv(val)})
		}
		return map[string]any{"__dict__": pairs}
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = pbConv(e)
		}
		return out
	}
	return v
}

func pbNormalize(v any) any {
	b, err := json.Marshal(pbConv(v))
	if err != nil {
		panic(err)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func pbLoad(t *testing.T, v any) any {
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

func pbRun(t *testing.T, cases []pbCase) {
	t.Helper()
	base := t.TempDir()
	iasl := filepath.Join(base, "iasl")
	if err := os.WriteFile(iasl, []byte(dsdttest.FakeIasl), 0o755); err != nil {
		t.Fatal(err)
	}
	var jc []map[string]any
	for i, c := range cases {
		cd := filepath.Join(base, fmt.Sprintf("c%d", i))
		dir, dslDir := filepath.Join(cd, "tables"), filepath.Join(cd, "dsl")
		for _, d := range []string{dir, dslDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		seed := int64(1)
		names := make([]string, 0, len(c.tables))
		for n := range c.tables {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			seed++
			tb := pbTable(n, c.tables[n], seed+int64(i)*7)
			if err := os.WriteFile(filepath.Join(dir, n), tb.Raw, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dslDir, n+".dsl"), []byte(tb.DSL), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		m := map[string]any{
			"method": c.method, "dir": dir, "dsl_dir": dslDir, "out": filepath.Join(cd, "out-py"),
			"report": c.report, "disabled": c.disabled, "smbios_model": c.model,
			"lpc": !c.noLPC, "lpc_value": c.lpcValue, "lines": c.lines, "method_path": c.methodPath,
		}
		if c.model == "" {
			m["smbios_model"] = ""
		}
		jc = append(jc, m)
	}
	py := pbOracle(t, jc, iasl)
	if len(py) != len(cases) {
		t.Fatalf("oracle returned %d results for %d cases", len(py), len(cases))
	}
	t.Setenv("FAKE_COMPILE", "ok")
	for i, c := range cases {
		cd := filepath.Join(base, fmt.Sprintf("c%d", i))
		t.Setenv("FAKE_DSL_DIR", filepath.Join(cd, "dsl"))
		want := py[i]
		g := New(dsdt.New(iasl), func(string) (string, error) { return "", nil })
		g.ACPIDirectory = filepath.Join(cd, "out-go")
		if c.report != nil {
			g.HardwareReport = pbLoad(t, c.report)
		}
		if c.disabled != nil {
			g.DisabledDevices = pbLoad(t, c.disabled)
		}
		g.SMBIOSModel = c.model
		g.ReadACPITables(jc[i]["dir"].(string))
		g.Dsdt = g.Acpi.GetDSDTOrOnly()
		if c.noLPC {
			g.LPCBusDevice = c.lpcValue
		} else {
			g.LPCBusDevice, _ = g.GetLPCName(false, false, false)
		}
		if lpc, _ := want.Lpc.(string); lpc != g.LPCBusDevice {
			t.Errorf("%s: lpc go=%q py=%v", c.name, g.LPCBusDevice, want.Lpc)
		}
		var res any
		var err error
		switch c.method {
		case "fake_embedded_controller":
			res, err = g.FakeEmbeddedController()
		case "add_intel_management_engine":
			res, err = g.AddIntelManagementEngine()
		case "add_memory_controller_device":
			res, err = g.AddMemoryControllerDevice()
		case "add_system_management_bus_device":
			res, err = g.AddSystemManagementBusDevice()
		case "add_usb_power_properties":
			res, err = g.AddUSBPowerProperties()
		case "ambient_light_sensor":
			res, err = g.AmbientLightSensor()
		case "disable_unsupported_device":
			res, err = g.DisableUnsupportedDevice()
		case "findall":
			var out []any
			for _, b := range g.FindallPowerResourceBlocks(c.lines) {
				out = append(out, []any{b[0], b[1]})
			}
			if out == nil {
				out = []any{}
			}
			res = out
		case "is_method":
			p := dsdt.Path{Path: c.methodPath[0].(string), Index: c.methodPath[1].(int), Type: "Method"}
			res = g.IsMethodInPowerResource(p, c.lines)
		default:
			t.Fatalf("unknown method %s", c.method)
		}
		if (err != nil) != want.Error {
			t.Errorf("%s: Go error=%v, Python error=%v (%s)", c.name, err, want.Error, want.Message)
			continue
		}
		if d, ok := res.(*plist.Dict); ok && d == nil {
			res = nil
		}
		if err == nil {
			if got := pbNormalize(res); !reflect.DeepEqual(got, want.OK) {
				gb, _ := json.Marshal(got)
				wb, _ := json.Marshal(want.OK)
				t.Errorf("%s: result\n go: %.1500s\n py: %.1500s", c.name, gb, wb)
			}
		}
		files := map[string]string{}
		if ents, rerr := os.ReadDir(g.ACPIDirectory); rerr == nil {
			for _, e := range ents {
				b, _ := os.ReadFile(filepath.Join(g.ACPIDirectory, e.Name()))
				files[e.Name()] = hex.EncodeToString(b)
			}
		}
		wf := want.Files
		if wf == nil {
			wf = map[string]string{}
		}
		if !reflect.DeepEqual(files, wf) {
			for n := range files {
				if files[n] != wf[n] {
					gb, _ := hex.DecodeString(files[n])
					wb, _ := hex.DecodeString(wf[n])
					t.Errorf("%s: file %s differs\n go:\n%s\n py:\n%s", c.name, n, gb, wb)
				}
			}
			for n := range wf {
				if _, ok := files[n]; !ok {
					t.Errorf("%s: Python wrote %s, Go did not", c.name, n)
				}
			}
		}
		if c.hasDisabled {
			if got := pbNormalize(g.DisabledDevices); !reflect.DeepEqual(got, want.Disabled) {
				gb, _ := json.Marshal(got)
				wb, _ := json.Marshal(want.Disabled)
				t.Errorf("%s: disabled devices\n go: %s\n py: %s", c.name, gb, wb)
			}
		}
	}
	nilN, errN, patchN, enabledN := 0, 0, 0, 0
	for _, w := range py {
		switch o := w.OK.(type) {
		case nil:
			nilN++
		case map[string]any:
			if strings.Contains(fmt.Sprint(o), "Enabled true") {
				enabledN++
			}
			if b, _ := json.Marshal(o); strings.Contains(string(b), `"Find"`) {
				patchN++
			}
		}
		if w.Error {
			errN++
		}
	}
	t.Logf("%d cases compared (nil %d, error %d, with rename patches %d, SSDT built %d)", len(cases), nilN, errN, patchN, enabledN)
}

func pbReport(platform, codename string) pbMap {
	return pbMap{
		"Motherboard": pbMap{"Name": "Generic Board", "Platform": platform, "Chipset": "Z370"},
		"CPU":         pbMap{"Manufacturer": "Intel", "Processor Name": "Intel Core i7", "Codename": codename},
	}
}

// ---- DSL builders -----------------------------------------------------------

func pbIndent(n int, lines ...string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.Repeat("    ", n) + l
	}
	return out
}

func pbCat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// pbDSDT wraps body lines (already placed under \_SB.PCI0) in a DSDT.
func pbDSDT(pci0 []string) []string {
	return pbCat(
		[]string{
			`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`,
			`{`,
			`    Scope (\_SB)`,
			`    {`,
			`        Device (PCI0)`,
			`        {`,
			`            Name (_HID, EisaId ("PNP0A08"))  // _HID: Hardware ID`,
			`            Name (_ADR, Zero)  // _ADR: Address`,
		},
		pbIndent(3, pci0...),
		[]string{`        }`, `    }`, `}`},
	)
}

// pbEC is an embedded controller device with the given STA flavour.
func pbEC(name, sta string, withGPE, crsInComment bool) []string {
	l := []string{
		`Device (` + name + `)`,
		`{`,
		`    Name (_HID, EisaId ("PNP0C09"))  // _HID: Hardware ID`,
		`    Name (_UID, One)  // _UID: Unique ID`,
	}
	if withGPE {
		l = append(l, `    Name (_GPE, 0x66)  // _GPE: General Purpose Events`)
	}
	if crsInComment {
		l = append(l, `    Name (XRES, Zero)  // _CRS: Current Resource Settings`)
	} else {
		l = append(l,
			`    Method (_CRS, 0, NotSerialized)  // _CRS: Current Resource Settings`,
			`    {`,
			`        Return (ResourceTemplate ()`,
			`        {`,
			`            IO (Decode16, 0x0062, 0x0062, 0x00, 0x01)`,
			`        })`,
			`    }`)
	}
	switch sta {
	case "method0F":
		l = append(l,
			`    Method (_STA, 0, NotSerialized)  // _STA: Status`,
			`    {`,
			`        Return (0x0F)`,
			`    }`,
			`@HEX 14 09 5F 53 54 41 00 A4 0A 0F 00 11 13 0A 10`)
	case "methodMulti":
		l = append(l,
			`    Method (_STA, 0, NotSerialized)  // _STA: Status`,
			`    {`,
			`        If (OSYS)`,
			`        {`,
			`            Return (0x0F)`,
			`        }`,
			`        Return (Zero)`,
			`    }`,
			`@HEX 14 0B 5F 53 54 41 00 A0 08 4F 53 59 53 A4 0A 0F`)
	case "methodCond":
		l = append(l,
			`    Method (_STA, 0, NotSerialized)  // _STA: Status`,
			`    {`,
			`        Return (ECON)  // Return (0x0F)`,
			`    }`,
			`@HEX 14 07 5F 53 54 41 00 A4 45 43 4F 4E 08 5F 55 49`)
	case "int0F":
		l = append(l,
			`    Name (_STA, 0x0F)  // _STA: Status`,
			`@HEX 08 5F 53 54 41 0A 0F 5B 82 31 4E 4F 50 45 5F 48`)
	case "intZero":
		l = append(l,
			`    Name (_STA, Zero)  // _STA: Status`,
			`@HEX 08 5F 53 54 41 00 5B 82 33 50 4F 50 45 5F 48 49`)
	case "xsta":
		l = append(l,
			`    Method (XSTA, 0, NotSerialized)`,
			`    {`,
			`        Return (0x0F)`,
			`    }`)
	}
	l = append(l, `}`)
	return l
}

func pbLPCB(inner ...[]string) []string {
	l := []string{`Device (LPCB)`, `{`, `    Name (_ADR, 0x001F0000)  // _ADR: Address`}
	l = append(l, pbIndent(1, pbCat(inner...)...)...)
	return append(l, `}`)
}

// ---- tests ------------------------------------------------------------------

func TestPBFakeEmbeddedController(t *testing.T) {
	var cases []pbCase
	stas := []string{"none", "method0F", "methodMulti", "methodCond", "int0F", "intZero", "xsta"}
	for _, plat := range []string{"Laptop", "Desktop"} {
		for _, ecName := range []string{"EC", "EC0", "H_EC"} {
			for _, sta := range stas {
				for _, variant := range []string{"plain", "nogpe", "crscomment"} {
					if variant != "plain" && sta != "method0F" && sta != "none" {
						continue
					}
					ec := pbEC(ecName, sta, variant != "nogpe", variant == "crscomment")
					cases = append(cases, pbCase{
						name:   fmt.Sprintf("ec/%s/%s/%s/%s", plat, ecName, sta, variant),
						method: "fake_embedded_controller",
						tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB(ec))},
						report: pbReport(plat, "Coffee Lake-S"),
					})
				}
			}
		}
	}
	for _, plat := range []string{"Laptop", "Desktop"} {
		// EC in a second table (SSDT) next to a DSDT with the LPCB; both tables hold an EC.
		ssdtEC := pbCat([]string{
			`DefinitionBlock ("", "SSDT", 2, "ALASKA", "Tbl2", 0x00000001)`,
			`{`,
			`    External (_SB_.PCI0.LPCB, DeviceObj)`,
			`    Scope (\_SB.PCI0.LPCB)`,
			`    {`,
		}, pbIndent(2, pbEC("EC", "methodMulti", true, false)...), []string{`    }`, `}`})
		cases = append(cases,
			pbCase{name: "ec/" + plat + "/ssdt-ec", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB()), "SSDT-EC.aml": ssdtEC}, report: pbReport(plat, "Skylake")},
			pbCase{name: "ec/" + plat + "/both", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB(pbEC("EC0", "method0F", true, false))), "SSDT-EC.aml": ssdtEC}, report: pbReport(plat, "Skylake")},
			// no EC at all: LPCB by common name / by _ADR / none
			pbCase{name: "ec/" + plat + "/lpcb-only", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB())}, report: pbReport(plat, "Skylake")},
			pbCase{name: "ec/" + plat + "/lpc-by-adr", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT([]string{`Device (ISA0)`, `{`, `    Name (_ADR, 0x001F0000)  // _ADR: Address`, `}`})}, report: pbReport(plat, "Skylake")},
			pbCase{name: "ec/" + plat + "/amd-lpc", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT([]string{`Device (SBRG)`, `{`, `    Name (_ADR, 0x00140003)  // _ADR: Address`, `}`})}, report: pbReport(plat, "Zen 3")},
			pbCase{name: "ec/" + plat + "/nothing", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT([]string{`Device (XYZ0)`, `{`, `    Name (_ADR, 0x00020000)  // _ADR: Address`, `}`})}, report: pbReport(plat, "Skylake")},
			pbCase{name: "ec/" + plat + "/no-tables", method: "fake_embedded_controller", tables: map[string][]string{}, report: pbReport(plat, "Skylake")},
			// two ECs in one table (one valid, one lacking _GPE)
			pbCase{name: "ec/" + plat + "/two", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB(pbEC("EC", "methodMulti", true, false), pbEC("EC2_", "int0F", false, false)))}, report: pbReport(plat, "Skylake")},
			pbCase{name: "ec/" + plat + "/two-valid", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB(pbEC("EC", "methodMulti", true, false), pbEC("EC2_", "intZero", true, false)))}, report: pbReport(plat, "Skylake")},
			// EC directly under PCI0 (no LPCB), and EC with an _STA that is a Name inside a nested scope
			pbCase{name: "ec/" + plat + "/ec-under-pci0", method: "fake_embedded_controller",
				tables: map[string][]string{"DSDT.aml": pbDSDT(pbEC("EC", "methodCond", true, false))}, report: pbReport(plat, "Skylake")},
		)
	}
	// Report shapes.
	for _, rep := range []any{
		nil,
		pbMap{},
		pbMap{"Motherboard": nil},
		pbMap{"Motherboard": pbMap{}},
		pbMap{"Motherboard": pbMap{"Platform": 5}},
		pbMap{"Motherboard": pbMap{"Platform": "Laptop/Desktop"}},
		pbMap{"Motherboard": pbMap{"Platform": "Mobile Laptop Pro"}},
		pbMap{"Motherboard": pbMap{"Platform": "laptop"}},
		pbMap{"Motherboard": pbMap{"Platform": ""}},
	} {
		cases = append(cases, pbCase{
			name:   fmt.Sprintf("ec/report/%v", rep),
			method: "fake_embedded_controller",
			tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB(pbEC("EC", "methodMulti", true, false)))},
			report: rep,
		})
	}
	pbRun(t, cases)
}

func TestPBIMEIMCHCSMBus(t *testing.T) {
	var cases []pbCase
	imeiDev := []string{`Device (IMEI)`, `{`, `    Name (_HID, 0x00160000)  // _HID: Hardware ID`, `}`}
	imeiAdr := []string{`Device (HECI)`, `{`, `    Name (_ADR, 0x00160000)  // _ADR: Address`, `}`}
	cases = append(cases,
		pbCase{name: "imei/none", method: "add_intel_management_engine", tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB())}},
		pbCase{name: "imei/adr", method: "add_intel_management_engine", tables: map[string][]string{"DSDT.aml": pbDSDT(imeiAdr)}},
		pbCase{name: "imei/hid", method: "add_intel_management_engine", tables: map[string][]string{"DSDT.aml": pbDSDT(imeiDev)}},
		pbCase{name: "imei/no-tables", method: "add_intel_management_engine", tables: map[string][]string{}},
		pbCase{name: "imei/ssdt-only", method: "add_intel_management_engine", tables: map[string][]string{"SSDT-A.aml": pbDSDT(imeiDev)}},
	)

	mchc := []string{`Device (MCHC)`, `{`, `    Name (_ADR, Zero)  // _ADR: Address`, `}`}
	for _, lpc := range []struct {
		n    string
		no   bool
		v    string
		body []string
	}{
		{"lpcb", false, "", pbLPCB()},
		{"lpcb+mchc", false, "", pbCat(pbLPCB(), mchc)},
		{"no-lpc", false, "", []string{`Device (XYZ0)`, `{`, `}`}},
		{"manual-short", true, "LPCB", pbLPCB()},
		{"manual-deep", true, `\_SB_.PCI0.RP01.LPCB`, pbLPCB()},
		{"manual-root", true, `\_SB_.PCI0.LPCB`, pbCat(pbLPCB(), mchc)},
		{"manual-empty", true, "", pbLPCB()},
		{"manual-onedot", true, "PCI0.LPCB", pbLPCB()},
	} {
		cases = append(cases, pbCase{name: "mchc/" + lpc.n, method: "add_memory_controller_device", tables: map[string][]string{"DSDT.aml": pbDSDT(lpc.body)}, noLPC: lpc.no, lpcValue: lpc.v})
	}

	// SMBus: devices by HID, codename on either side of IntelCPUGenerations[50:].
	smb := func(name, hid string) []string {
		return []string{`Device (` + name + `)`, `{`, `    Name (_HID, ` + hid + `)  // _HID: Hardware ID`, `}`}
	}
	codenames := []string{"Coffee Lake-S", "Skylake", "Zen 3", "Unknown"}
	for _, i := range []int{0, 10, 49, 50, 51, len(datasets.IntelCPUGenerations) - 1} {
		if i < len(datasets.IntelCPUGenerations) {
			codenames = append(codenames, datasets.IntelCPUGenerations[i])
		}
	}
	bodies := map[string][]string{
		"none":      pbLPCB(),
		"sbus3":     pbCat(pbLPCB(), smb("SBUS", "0x001F0003")),
		"smbs3":     pbCat(pbLPCB(), smb("SMBS", "0x001F0003")),
		"smbs4":     pbCat(pbLPCB(), smb("SMBS", "0x001F0004")),
		"both":      pbCat(pbLPCB(), smb("SBUS", "0x001F0003"), smb("SBS4", "0x001F0004")),
		"lower":     pbCat(pbLPCB(), smb("SMB1", "0x001f0004")),
		"underscor": pbCat(pbLPCB(), smb("SM__", "0x001F0004")),
	}
	var bnames []string
	for n := range bodies {
		bnames = append(bnames, n)
	}
	sort.Strings(bnames)
	for _, cn := range codenames {
		for _, bn := range bnames {
			cases = append(cases, pbCase{name: "smbus/" + cn + "/" + bn, method: "add_system_management_bus_device",
				tables: map[string][]string{"DSDT.aml": pbDSDT(bodies[bn])}, report: pbReport("Desktop", cn)})
		}
	}
	for _, rep := range []any{nil, pbMap{}, pbMap{"CPU": nil}, pbMap{"CPU": pbMap{}}, pbMap{"CPU": pbMap{"Codename": 5}}, pbMap{"CPU": "x"}} {
		cases = append(cases, pbCase{name: fmt.Sprintf("smbus/report/%v", rep), method: "add_system_management_bus_device",
			tables: map[string][]string{"DSDT.aml": pbDSDT(bodies["smbs4"])}, report: rep})
	}
	cases = append(cases,
		pbCase{name: "smbus/no-lpc", method: "add_system_management_bus_device", tables: map[string][]string{"DSDT.aml": pbDSDT(smb("SBUS", "0x001F0004"))}, report: pbReport("Desktop", "Skylake")},
		pbCase{name: "smbus/manual", method: "add_system_management_bus_device", tables: map[string][]string{"DSDT.aml": pbDSDT(bodies["smbs4"])}, report: pbReport("Desktop", "Skylake"), noLPC: true, lpcValue: "LPCB"},
		pbCase{name: "smbus/manual-deep", method: "add_system_management_bus_device", tables: map[string][]string{"DSDT.aml": pbDSDT(bodies["smbs4"])}, report: pbReport("Desktop", "Skylake"), noLPC: true, lpcValue: `\_SB_.PCI0.RP01.LPCB`},
	)
	pbRun(t, cases)
}

func TestPBUSBPower(t *testing.T) {
	var cases []pbCase
	for _, m := range []string{
		"", "MacPro7,1", "iMacPro1,1", "iMac20,1", "iMac20,2", "iMac19,1", "iMac18,3", "iMac17,1", "iMac16,2", "iMac15,1", "iMac14,2",
		"MacMini8,1", "MacMini7,1", "MacBookPro16,1", "MacBookPro15,2", "MacBookPro14,3", "MacBookPro13,1", "MacBookPro12,1",
		"MacBookAir9,1", "MacBookAir8,1", "MacBook9,1", "MacBook10,1", "MacBook8,1", "MacBookPro17,1", "MacPro6,1", "MacPro7,2",
		"imac20,1", "IMAC19,1", "macbookpro16,1", "macmini8,1", "iMac20", "iMac20,", "MacMini8,1 iMac19,1", "xMacBookAir9,1y",
		"MacBook9,12", "Macbook9,1",
	} {
		cases = append(cases, pbCase{name: "usbx/" + m, method: "add_usb_power_properties", tables: map[string][]string{}, model: m})
	}
	pbRun(t, cases)
}

func TestPBAmbientLightSensor(t *testing.T) {
	var cases []pbCase
	als := func(name, sta string) []string {
		l := []string{`Device (` + name + `)`, `{`, `    Name (_HID, "ACPI0008" /* Ambient Light Sensor Device */)  // _HID: Hardware ID`}
		switch sta {
		case "method":
			l = append(l, `    Method (_STA, 0, NotSerialized)  // _STA: Status`, `    {`, `        Return (0x0F)`, `    }`,
				`@HEX 14 09 5F 53 54 41 00 A4 0A 0F 08 5F 41 4C 49`)
		case "int":
			l = append(l, `    Name (_STA, 0x0F)  // _STA: Status`, `@HEX 08 5F 53 54 41 0A 0F 08 5F 41 4C 49 0B 2C 01`)
		case "xsta":
			l = append(l, `    Method (XSTA, 0, NotSerialized)`, `    {`, `        Return (0x0F)`, `    }`)
		case "both":
			l = append(l, `    Method (_STA, 0, NotSerialized)`, `    {`, `        Return (0x0F)`, `    }`, `@HEX 14 09 5F 53 54 41 00 A4 0A 0F 08 5F 41 4C 49`,
				`    Method (XSTA, 0, NotSerialized)`, `    {`, `        Return (0x0F)`, `    }`)
		}
		return append(l, `}`)
	}
	for _, name := range []string{"ALS0", "ALSD", "_ALS", "AMBI"} {
		for _, sta := range []string{"none", "method", "int", "xsta", "both"} {
			cases = append(cases,
				pbCase{name: "als/pci0/" + name + "/" + sta, method: "ambient_light_sensor", tables: map[string][]string{"DSDT.aml": pbDSDT(pbCat(pbLPCB(), als(name, sta)))}},
				pbCase{name: "als/lpcb/" + name + "/" + sta, method: "ambient_light_sensor", tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB(als(name, sta)))}},
				pbCase{name: "als/sb/" + name + "/" + sta, method: "ambient_light_sensor", tables: map[string][]string{"DSDT.aml": pbCat(
					[]string{`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`, `{`, `    Scope (\_SB)`, `    {`},
					pbIndent(2, als(name, sta)...), []string{`    }`, `}`})}},
				pbCase{name: "als/root/" + name + "/" + sta, method: "ambient_light_sensor", tables: map[string][]string{"DSDT.aml": pbCat(
					[]string{`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`, `{`},
					pbIndent(1, als(name, sta)...), []string{`}`})}},
			)
		}
	}
	cases = append(cases,
		pbCase{name: "als/none", method: "ambient_light_sensor", tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB())}},
		pbCase{name: "als/no-tables", method: "ambient_light_sensor", tables: map[string][]string{}},
		pbCase{name: "als/two", method: "ambient_light_sensor", tables: map[string][]string{"DSDT.aml": pbDSDT(pbCat(als("ALS1", "method"), als("ALS0", "int")))}},
		pbCase{name: "als/ssdt-only", method: "ambient_light_sensor", tables: map[string][]string{"SSDT-1.aml": pbDSDT(als("ALS0", "method"))}},
	)
	pbRun(t, cases)
}

func TestPBPowerResources(t *testing.T) {
	samples := map[string][]string{
		"empty":  {},
		"none":   {"Device (A)", "{", "}"},
		"simple": {"Device (A)", "{", "    PowerResource (PR00, 0x00, 0x0000)", "    {", "        Method (_ON, 0)", "        {", "        }", "    }", "}"},
		"two": {"PowerResource (P1, 0, 0)", "{", "    Method (_OFF, 0)", "    {", "    }", "}",
			"Device (B)", "{", "}", "PowerResource (P2, 0, 0)", "{", "    Method (_OFF, 0)", "    {", "    }", "}"},
		"sameline":  {"PowerResource (P1, 0, 0) { Method (_OFF) { } }", "Device (C) {", "}"},
		"unclosed":  {"PowerResource (P1, 0, 0)", "{", "    Method (_OFF)", "    {"},
		"indented":  {"   \t PowerResource (P1, 0, 0)", "{", "}", "x"},
		"nolbrace":  {"PowerResource (P1, 0, 0)", "Method (_OFF)", "}", "after"},
		"nested":    {"PowerResource (P1, 0, 0)", "{", "    If (X)", "    {", "        Foo ()", "    }", "    Method (_OFF) {", "    }", "}", "tail"},
		"notstart":  {"Foo PowerResource (P1)", "{", "}"},
		"twobraces": {"PowerResource (P1, 0, 0)", "{ {", "} }", "}", "z"},
		"prefix":    {"PowerResourceX (P1)", "{", "}"},
	}
	var names []string
	for n := range samples {
		names = append(names, n)
	}
	sort.Strings(names)
	var cases []pbCase
	for _, n := range names {
		l := samples[n]
		cases = append(cases, pbCase{name: "findall/" + n, method: "findall", tables: map[string][]string{}, lines: l})
		for idx := -1; idx <= len(l)+1; idx++ {
			cases = append(cases, pbCase{name: fmt.Sprintf("ismethod/%s/%d", n, idx), method: "is_method", tables: map[string][]string{}, lines: l, methodPath: []any{"X._OFF", idx}})
		}
	}
	pbRun(t, cases)
}

// pbGPUTable builds a PEG0.PEGP device with the chosen power methods.
func pbGPUTable(kind string) []string {
	l := []string{`Device (PEG0)`, `{`, `    Name (_ADR, 0x00010000)  // _ADR: Address`, `    Device (PEGP)`, `    {`, `        Name (_ADR, Zero)  // _ADR: Address`}
	off := []string{`        Method (_ON, 0, Serialized)  // _ON_: Power On`, `        {`, `        }`, `        Method (_OFF, 0, Serialized)  // _OFF: Power Off`, `        {`, `        }`}
	ps3 := []string{`        Method (_PS0, 0, Serialized)  // _PS0: Power State 0`, `        {`, `        }`, `        Method (_PS3, 0, Serialized)  // _PS3: Power State 3`, `        {`, `        }`,
		`        Method (_DSM, 4, Serialized)  // _DSM: Device-Specific Method`, `        {`, `            Return (Zero)`, `        }`}
	pr := []string{`        PowerResource (PGON, 0x00, 0x0000)`, `        {`, `            Method (_ON, 0, Serialized)  // _ON_: Power On`, `            {`, `            }`,
		`            Method (_OFF, 0, Serialized)  // _OFF: Power Off`, `            {`, `            }`, `        }`}
	switch kind {
	case "off":
		l = append(l, off...)
	case "ps3":
		l = append(l, ps3...)
	case "both":
		l = append(l, off...)
		l = append(l, ps3...)
	case "pr-off":
		l = append(l, pr...)
	case "pr-off+ps3":
		l = append(l, pr...)
		l = append(l, ps3...)
	case "pr-then-off":
		l = append(l, pr...)
		l = append(l, off...)
	case "off-then-pr":
		l = append(l, off...)
		l = append(l, pr...)
	}
	return append(l, `    }`, `}`)
}

func TestPBDisableUnsupportedDevice(t *testing.T) {
	var cases []pbCase
	gpu := func(path string, extra pbMap) pbMap {
		m := pbMap{"Device Type": "Discrete GPU", "Bus Type": "PCI", "ACPI Path": path}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	paths := []string{`\_SB.PCI0.PEG0.PEGP`, `\_SB.PCI0.PEG0`, `\_SB.PCI0.PEG`, `\_SB.PCI0.PEG0.PEGP._OFF`, `\_SB.PCI0.PEG1.PEGP`, `\_SB_.PCI0.PEG0.PEGP`, `_SB_.PCI0.PEG0.PEGP`, `\_SB_.PCI0.PEG0`, `\_SB.PCI0.PEG0.PEGP`, `PCI0.PEG0.PEGP`, `\_SB_.PCI0.PEG1.PEGP`, `\_SB_.PCI0.PEG`}
	kinds := []string{"none", "off", "ps3", "both", "pr-off", "pr-off+ps3", "pr-then-off", "off-then-pr"}
	for _, k := range kinds {
		for _, p := range paths {
			cases = append(cases, pbCase{name: "dis/gpu/" + k + "/" + p, method: "disable_unsupported_device", hasDisabled: true,
				tables:   map[string][]string{"DSDT.aml": pbDSDT(pbCat(pbLPCB(), pbGPUTable(k)))},
				disabled: pbMap{"GPU 0": gpu(p, nil)}})
		}
		// second table holds the methods, a third table holds a PowerResource-only _OFF
		cases = append(cases, pbCase{name: "dis/gpu/split/" + k, method: "disable_unsupported_device", hasDisabled: true,
			tables: map[string][]string{"DSDT.aml": pbDSDT(pbLPCB()),
				"SSDT-1.aml": pbCat([]string{`DefinitionBlock ("", "SSDT", 2, "ALASKA", "Tbl1", 0x00000001)`, `{`, `    Scope (\_SB.PCI0)`, `    {`}, pbIndent(2, pbGPUTable(k)...), []string{`    }`, `}`}),
				"SSDT-2.aml": pbCat([]string{`DefinitionBlock ("", "SSDT", 2, "ALASKA", "Tbl2", 0x00000001)`, `{`, `    Scope (\_SB.PCI0)`, `    {`}, pbIndent(2, pbGPUTable("pr-off+ps3")...), []string{`    }`, `}`})},
			disabled: pbMap{"GPU 0": gpu(`\_SB.PCI0.PEG0.PEGP`, nil)}})
	}
	body := pbDSDT(pbCat(pbLPCB(), pbGPUTable("both")))
	one := func(name string, dev any) {
		cases = append(cases, pbCase{name: "dis/" + name, method: "disable_unsupported_device", hasDisabled: true,
			tables: map[string][]string{"DSDT.aml": body}, disabled: dev})
	}
	gp := `\_SB.PCI0.PEG0.PEGP`
	one("gpu-integrated", pbMap{"GPU 0": gpu(gp, pbMap{"Device Type": "Integrated GPU"})})
	one("gpu-notype", pbMap{"GPU 0": pbMap{"Bus Type": "PCI", "ACPI Path": gp}})
	one("gpu-nobus", pbMap{"GPU 0": pbMap{"Device Type": "Discrete GPU", "ACPI Path": gp}})
	one("gpu-usb", pbMap{"GPU 0": gpu(gp, pbMap{"Bus Type": "USB"})})
	one("gpu-nopath", pbMap{"GPU 0": pbMap{"Device Type": "Discrete GPU", "Bus Type": "PCI"}})
	one("gpu-emptypath", pbMap{"GPU 0": gpu("", nil)})
	one("gpu-nullpath", pbMap{"GPU 0": gpu("", pbMap{"ACPI Path": nil})})
	one("gpu-intpath", pbMap{"GPU 0": gpu("", pbMap{"ACPI Path": 5})})
	one("gpu-listpath", pbMap{"GPU 0": gpu("", pbMap{"ACPI Path": []any{"a", "b", "c"}})})
	one("gpu-shortpath", pbMap{"GPU 0": gpu("PEG0.PEGP", nil)})
	one("gpu-1part", pbMap{"GPU 0": gpu("PEG0", nil)})
	one("gpu-name-lower", pbMap{"gpu 0": gpu(gp, nil)})
	one("gpu-name-nvidia", pbMap{"NVIDIA GPU": gpu(gp, nil), "AMD GPU": gpu(gp, nil)})
	one("gpu-network-name", pbMap{"Network GPU": gpu(gp, nil)})
	one("gpu-storage-name", pbMap{"Storage GPU": gpu(gp, pbMap{"Device Type": "Integrated GPU"})})
	one("net", pbMap{"Network 0": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}})
	one("net-multi", pbMap{"Network 0": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}, "Network 1": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP06.PXSX`}})
	one("net-nobus", pbMap{"Network 0": pbMap{"ACPI Path": `\_SB_.PCI0.RP05.PXSX`}})
	one("net-usb", pbMap{"Network 0": pbMap{"Bus Type": "USB", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}})
	one("net-nullbus", pbMap{"Network 0": pbMap{"Bus Type": nil, "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}})
	one("net-short", pbMap{"Network 0": pbMap{"Bus Type": "PCI", "ACPI Path": "RP05.PXSX"}})
	one("net-nopath", pbMap{"Network 0": pbMap{"Bus Type": "PCI"}})
	one("net-lower", pbMap{"network 0": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}})
	one("net-wifi", pbMap{"Broadcom Network": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}})
	one("storage", pbMap{"Samsung Storage": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP09.PXSX`}})
	one("storage-nobus", pbMap{"Samsung Storage": pbMap{"ACPI Path": `\_SB_.PCI0.RP09.PXSX`}})
	one("storage-usb", pbMap{"Samsung Storage": pbMap{"Bus Type": "USB", "ACPI Path": `\_SB_.PCI0.RP09.PXSX`}})
	one("storage-short2", pbMap{"Samsung Storage": pbMap{"ACPI Path": "RP09.PXSX"}})
	one("storage-short1", pbMap{"Samsung Storage": pbMap{"ACPI Path": "PXSX"}})
	one("storage-lower", pbMap{"samsung storage": pbMap{"ACPI Path": `\_SB_.PCI0.RP09.PXSX`}})
	one("other", pbMap{"Audio": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.HDAS`}, "Bluetooth": pbMap{"Bus Type": "USB", "ACPI Path": `\_SB_.PCI0.XHC_.RHUB`}})
	one("mixed", pbMap{
		"Network 0":        pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`},
		"Audio":            pbMap{"ACPI Path": `\_SB_.PCI0.HDAS`},
		"GPU 1":            gpu(gp, nil),
		"Storage 1":        pbMap{"ACPI Path": `\_SB_.PCI0.RP09.PXSX`},
		"GPU 2":            gpu(`\_SB.PCI0.PEG9.PEGP`, nil),
		"Integrated GPU 0": pbMap{"Device Type": "Integrated GPU", "ACPI Path": `\_SB_.PCI0.GFX0`},
	})
	one("mixed-error-late", pbMap{"Network 0": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}, "Network 1": pbMap{"Bus Type": "PCI", "ACPI Path": "X"}})
	one("empty", pbMap{})
	one("null", nil)
	one("list", []any{})
	one("props-string", pbMap{"GPU 0": "x"})
	one("props-null", pbMap{"GPU 0": nil})
	one("props-list", pbMap{"Network 0": []any{}})
	one("bus-int", pbMap{"Network 0": pbMap{"Bus Type": 5, "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}})
	one("bus-pci-case", pbMap{"Network 0": pbMap{"Bus Type": "pci", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}})
	one("path-with-spaces", pbMap{"Network 0": pbMap{"ACPI Path": `\_SB_.PCI0.RP05.PXSX`, "Bus Type": "PCI"}, "Storage": pbMap{"ACPI Path": "\\_SB_.PCI0.RP 09.PX SX"}})
	cases = append(cases, pbCase{name: "dis/no-tables", method: "disable_unsupported_device", hasDisabled: true, tables: map[string][]string{},
		disabled: pbMap{"GPU 0": gpu(gp, nil), "Network 0": pbMap{"Bus Type": "PCI", "ACPI Path": `\_SB_.PCI0.RP05.PXSX`}}})
	pbRun(t, cases)
}
