package dsdt

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// fakeIasl copies FAKE_DSL_DIR/<table>.dsl to <base>.dsl for each table argument.
// FAKE_FAIL_DA=1 makes the call that carries -da fail; FAKE_FAIL_MULTI=1 makes
// calls with more than one table fail. Both sides run this same script.
const fakeIasl = `#!/bin/sh
if [ "$FAKE_FAIL_DA" = "1" ]; then
  for a in "$@"; do [ "$a" = "-da" ] && exit 1; done
fi
n=0
for a in "$@"; do case "$a" in -*) ;; *) n=$((n+1));; esac; done
if [ "$FAKE_FAIL_MULTI" = "1" ] && [ "$n" -gt 1 ]; then exit 1; fi
for a in "$@"; do
  case "$a" in
    -*) ;;
    *) b="${a%.*}"
       if [ -f "$FAKE_DSL_DIR/$a.dsl" ]; then cp "$FAKE_DSL_DIR/$a.dsl" "$b.dsl"; fi ;;
  esac
done
exit 0
`

// textLines is the disassembly body; hex lines are interleaved by buildTable.
var textLines = []string{
	`DefinitionBlock ("", "DSDT", 2, "ALASKA", "A M I ", 0x01072009)`,
	`{`,
	`    External (_SB_.PCI0.XHC_, DeviceObj)    // (from opcode)`,
	`    Scope (\_SB)`,
	`    {`,
	`        Device (PCI0)`,
	`        {`,
	`            Name (_HID, EisaId ("PNP0A08"))  // _HID: Hardware ID`,
	`            Method (_STA, 0, NotSerialized)  // _STA: Status`,
	`            {`,
	`                Return (0x0F)`,
	`            }`,
	`            Device (LPCB)`,
	`            {`,
	`                Name (_ADR, 0x001F0000)  // _ADR: Address`,
	`                Device (EC0_)`,
	`                {`,
	`                    Name (_HID, "ACPI0001")  // _HID: Hardware ID`,
	`                }`,
	`            }`,
	`            Device (^SAT0)`,
	`            {`,
	`                Name (_ADR, 0x00170000)  // _ADR: Address`,
	`            }`,
	`        }`,
	`        Scope (PCI0.LPCB)`,
	`        {`,
	`            Device (HPET)`,
	`            {`,
	`                Name (_HID, EisaId ("PNP0103"))  // _HID: Hardware ID`,
	`            }`,
	`        }`,
	`        Device (\_SB.USB0)`,
	`        {`,
	`            Name (_HID, "ACPI000E")`,
	`        }`,
	`    }`,
	`    Scope (\_PR)`,
	`    {`,
	`        Processor (CPU0, 0x01, 0x00001810, 0x06)`,
	`        {`,
	`        }`,
	`    }`,
	`    Method (_WAK, 1, Serialized)  // _WAK: Wake`,
	`    {`,
	`        Return (Package (0x02) { Zero, Zero })`,
	`    }`,
	`}`,
}

// table is a generated table: the raw bytes and the matching mixed listing.
type table struct {
	raw []byte
	dsl string
}

type tableOpts struct {
	sig      string
	oem      string
	id       string
	crlf     bool
	bodySize int
	seed     int64
}

func buildTable(o tableOpts) table {
	rng := rand.New(rand.NewSource(o.seed))
	body := make([]byte, o.bodySize)
	for i := range body {
		body[i] = byte(rng.Intn(256))
	}
	// Repeated sequences so that a pad is needed to make a pattern unique.
	for _, at := range []int{20, 100, 180, 260} {
		if at+8 <= len(body) {
			copy(body[at:], []byte{0x5C, 0x5F, 0x53, 0x42, 0x5F, 0x50, 0x43, 0x49})
		}
	}
	hdr := make([]byte, 36)
	copy(hdr[0:4], o.sig)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+len(body)))
	hdr[8] = 2
	copy(hdr[10:16], o.oem)
	copy(hdr[16:24], o.id)
	binary.LittleEndian.PutUint32(hdr[24:28], 0x01072009)
	copy(hdr[28:32], "INTL")
	binary.LittleEndian.PutUint32(hdr[32:36], 0x20230628)
	raw := append(hdr, body...)

	// Hex chunks: every 16-byte line of the body except the last one, which the
	// disassembler omits.
	var chunks []string
	n := (len(body) + 15) / 16
	for i := 0; i < n-1; i++ {
		c := body[i*16 : i*16+16]
		var parts []string
		for _, b := range c {
			parts = append(parts, fmt.Sprintf("%02X", b))
		}
		chunks = append(chunks, fmt.Sprintf("    %04X: %s", 0x24+i*16, strings.Join(parts, " ")))
	}
	var sb strings.Builder
	sb.WriteString("/*\n * Intel ACPI Component Architecture\n * AML/ASL+ Disassembler version 20230628\n */\n")
	ci := 0
	for i, l := range textLines {
		sb.WriteString(l + "\n")
		for k := 0; k < i%3 && ci < len(chunks); k++ {
			c := chunks[ci]
			if k == 0 && i%5 == 0 {
				c += "  // commented tail"
			}
			sb.WriteString(c + "\n")
			ci++
		}
	}
	for ; ci < len(chunks); ci++ {
		sb.WriteString(chunks[ci] + "\n")
	}
	sb.WriteString("\nTable Header:\n    Signature        : \"" + o.sig + "\"\n")
	dsl := sb.String()
	if o.crlf {
		dsl = strings.ReplaceAll(dsl, "\n", "\r\n")
	}
	return table{raw: raw, dsl: dsl}
}

type scenario struct {
	name string
	// files maps a file name to its .aml bytes; dsl maps "<file>" to the text
	// the fake iasl writes for it (absent means no output).
	files map[string]table
	extra map[string][]byte // files written as-is (not tables)
	dsl   map[string]string
	env   map[string]string
	// steps builds the steps once the directory is known.
	steps func(dir string) []map[string]any
}

type result struct {
	OK      any  `json:"ok"`
	Error   bool `json:"error"`
	Message string
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
	cmd := exec.Command("python3", "-I", "testdata/pyref_dsdt.py", repoRoot(t), iasl)
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

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func shaBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func pathsJSON(ps []Path) []any {
	out := []any{}
	for _, p := range ps {
		out = append(out, []any{p.Path, p.Index, p.Type})
	}
	return out
}

func detail(t *Table) map[string]any {
	scopes := []any{}
	for _, s := range t.Scopes {
		scopes = append(scopes, []any{s.Line, s.Index})
	}
	str := func(p *string) any {
		if p == nil {
			return nil
		}
		return *p
	}
	return map[string]any{
		"signature":       hex.EncodeToString(t.Signature),
		"oem":             hex.EncodeToString(t.OEM),
		"id":              hex.EncodeToString(t.ID),
		"revision":        t.Revision,
		"oem_revision":    t.OEMRevision.String(),
		"length":          t.Length,
		"raw":             shaBytes(t.Raw),
		"table":           sha(t.Table),
		"lines":           sha(strings.Join(t.Lines, "\n")),
		"nlines":          len(t.Lines),
		"scopes":          scopes,
		"paths":           pathsJSON(t.Paths),
		"signature_ascii": str(t.SignatureAsc),
		"oem_ascii":       str(t.OEMAsc),
		"id_ascii":        str(t.IDAsc),
	}
}

func tableHash(t *Table) any {
	if t == nil {
		return nil
	}
	return shaBytes(t.Raw)
}

func sel(d *DSDT, s map[string]any) *Table {
	name, ok := s["table"].(string)
	if !ok {
		return nil
	}
	tbl, _ := d.AcpiTables.Get(name)
	return tbl
}

func num(s map[string]any, k string) int {
	switch v := s[k].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func str(s map[string]any, k string) string { v, _ := s[k].(string); return v }

func flag(s map[string]any, k string) bool { v, _ := s[k].(bool); return v }

func hexBytes(s string) []byte { b, _ := hex.DecodeString(s); return b }

// goStep runs one step on the Go port and returns its value in the oracle's shape.
func goStep(d *DSDT, s map[string]any) (any, bool) {
	tbl := sel(d, s)
	switch str(s, "op") {
	case "load":
		loaded, failed := d.Load(str(s, "path"))
		det := map[string]any{}
		for _, n := range loaded.Names() {
			v, _ := loaded.Get(n)
			det[n] = detail(v)
		}
		if failed == nil {
			failed = []string{}
		}
		sort.Strings(failed)
		all := d.AcpiTables.Names()
		sort.Strings(all)
		return map[string]any{"loaded": det, "failed": failed, "all": all}, false
	case "reset":
		d.AcpiTables = NewTables()
		return true, false
	case "hex_from_int":
		pad := 4
		if _, ok := s["pad_to"]; ok {
			pad = num(s, "pad_to")
		}
		return GetHexFromInt(int64(num(s, "total")), pad), false
	case "ascii":
		var data []byte
		for _, v := range s["data"].([]int) {
			data = append(data, byte(v))
		}
		u, a := GetASCIIPrint(data)
		return []any{u, a}, false
	case "valid":
		ok, err := d.TableIsValid(str(s, "path"), str(s, "name"))
		return ok, err != nil
	case "signature":
		sig, ok, err := d.TableSignature(str(s, "path"), str(s, "name"))
		if err != nil {
			return nil, true
		}
		if !ok {
			return nil, false
		}
		return hex.EncodeToString(sig), false
	case "dsdt_or_only":
		r := d.GetDSDTOrOnly()
		if r == nil {
			return nil, false
		}
		h := hex.EncodeToString(r.Raw)
		return h[:min(64, len(h))] + ":" + fmt.Sprint(len(r.Raw)), false
	case "table_with_id":
		return tableHash(d.GetTableWithID(hexBytes(str(s, "value")))), false
	case "table_with_sig":
		return tableHash(d.GetTableWithSignature(hexBytes(str(s, "value")))), false
	case "table":
		return tableHash(d.GetTable(hexBytes(str(s, "value")))), false
	case "is_hex":
		return d.IsHex(str(s, "line")), false
	case "get_hex":
		return GetHex(str(s, "line")), false
	case "get_line":
		return GetLine(str(s, "line")), false
	case "prev_hex":
		h, a, b := d.FindPreviousHex(num(s, "index"), tbl)
		return []any{h, a, b}, false
	case "next_hex":
		h, a, b := d.FindNextHex(num(s, "index"), tbl)
		return []any{h, a, b}, false
	case "hex_start":
		h, a := d.GetHexStartingAt(num(s, "index"), tbl)
		return []any{h, a}, false
	case "hex_end":
		h, a := d.GetHexEndingAt(num(s, "index"), tbl)
		return []any{h, a}, false
	case "unique_pad":
		dir := map[string]Direction{"both": Both, "back": Backward, "fwd": Forward}[str(s, "direction")]
		l, r, err := d.GetUniquePad(str(s, "hex"), num(s, "index"), dir, num(s, "instance"), tbl)
		if err != nil {
			return nil, true
		}
		return []any{l, r}, false
	case "shortest_pad":
		p, err := d.GetShortestUniquePad(str(s, "hex"), num(s, "index"), num(s, "instance"), tbl)
		if err != nil {
			return nil, true
		}
		if p == nil {
			return nil, false
		}
		return []any{p.Left, p.Right}, false
	case "devices":
		types := DefaultDeviceTypes
		if v, ok := s["types"].([]string); ok {
			types = v
		}
		out := []any{}
		for _, dv := range d.GetDevices(str(s, "search"), types, flag(s, "strip"), tbl) {
			var line any
			if dv.HasDevice {
				line = dv.Line
			}
			out = append(out, []any{line, dv.DeviceIndex, dv.Index})
		}
		return out, false
	case "scope":
		out := d.GetScope(num(s, "index"), flag(s, "add_hex"), flag(s, "strip"), tbl)
		if out == nil {
			out = []string{}
		}
		return out, false
	case "scopes":
		out := []any{}
		for _, sc := range d.GetScopes(tbl) {
			out = append(out, []any{sc.Line, sc.Index})
		}
		return out, false
	case "paths":
		return pathsJSON(d.GetPaths(tbl)), false
	case "path_of_type":
		return pathsJSON(d.GetPathOfType(str(s, "type"), str(s, "obj"), tbl)), false
	case "device_paths":
		return pathsJSON(d.GetDevicePaths(str(s, "obj"), tbl)), false
	case "method_paths":
		return pathsJSON(d.GetMethodPaths(str(s, "obj"), tbl)), false
	case "name_paths":
		return pathsJSON(d.GetNamePaths(str(s, "obj"), tbl)), false
	case "processor_paths":
		return pathsJSON(d.GetProcessorPaths(str(s, "type"), tbl)), false
	case "hid_paths":
		return pathsJSON(d.GetDevicePathsWithHID(str(s, "hid"), tbl)), false
	}
	panic("unknown op " + str(s, "op"))
}

func normalize(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// normalizeSteps makes step values JSON-shaped before they are sent or run.
func prepare(steps []map[string]any) []map[string]any {
	out := make([]map[string]any, len(steps))
	for i, s := range steps {
		out[i] = normalize(s).(map[string]any)
	}
	return out
}

func writeScenario(t *testing.T, sc scenario) (dir, dslDir, iasl string) {
	t.Helper()
	base := t.TempDir()
	dir = filepath.Join(base, "tables")
	dslDir = filepath.Join(base, "dsl")
	for _, d := range []string{dir, dslDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, tb := range sc.files {
		if err := os.WriteFile(filepath.Join(dir, name), tb.raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, b := range sc.extra {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range sc.dsl {
		if err := os.WriteFile(filepath.Join(dslDir, name+".dsl"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	iasl = filepath.Join(base, "iasl")
	if err := os.WriteFile(iasl, []byte(fakeIasl), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, dslDir, iasl
}

func runScenario(t *testing.T, sc scenario) {
	dir, dslDir, iasl := writeScenario(t, sc)
	steps := prepareSteps(sc.steps(dir))
	env := map[string]string{"FAKE_DSL_DIR": dslDir}
	for k, v := range sc.env {
		env[k] = v
	}
	py := runOracle(t, steps, iasl, env)
	if len(py) != len(steps) {
		t.Fatalf("oracle returned %d results for %d steps", len(py), len(steps))
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	d := New(iasl)
	for i, s := range sc.steps(dir) {
		got, gerr := goStep(d, s)
		want := py[i]
		if gerr != want.Error {
			t.Errorf("step %d %v: Go error=%v, Python error=%v (%s)", i, s["op"], gerr, want.Error, want.Message)
			continue
		}
		if gerr {
			continue
		}
		if g := normalize(got); !reflect.DeepEqual(g, want.OK) {
			gb, _ := json.Marshal(g)
			wb, _ := json.Marshal(want.OK)
			t.Errorf("step %d %v %v:\n go: %.600s\n py: %.600s", i, s["op"], summarize(s), gb, wb)
		}
	}
}

func prepareSteps(steps []map[string]any) []map[string]any { return prepare(steps) }

func summarize(s map[string]any) string {
	var parts []string
	for k, v := range s {
		if k == "op" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// queries builds read-only steps over the DSDT given its generated lines.
func queries(lines []string, table string, hexLines []int) []map[string]any {
	var steps []map[string]any
	add := func(s map[string]any) {
		if table != "" {
			s["table"] = table
		}
		steps = append(steps, s)
	}
	n := len(lines)
	for _, i := range []int{-3, -1, 0, 1, 5, 9, n / 2, n - 2, n - 1, n, n + 4} {
		add(map[string]any{"op": "prev_hex", "index": i})
		add(map[string]any{"op": "next_hex", "index": i})
		add(map[string]any{"op": "hex_start", "index": i})
		add(map[string]any{"op": "hex_end", "index": i})
		add(map[string]any{"op": "scope", "index": i})
		add(map[string]any{"op": "scope", "index": i, "add_hex": true, "strip": true})
	}
	for _, s := range []string{"Device (", "_HID", "Name (", "Method (", "ACPI000E", "NOT_THERE", "", "LPCB"} {
		add(map[string]any{"op": "devices", "search": s})
		add(map[string]any{"op": "devices", "search": s, "strip": true})
		add(map[string]any{"op": "devices", "search": s, "types": []string{"Method ("}})
	}
	add(map[string]any{"op": "scopes"})
	add(map[string]any{"op": "paths"})
	for _, ty := range []string{"Device", "device", "Method", "Name", "Processor", "Scope", ""} {
		for _, obj := range []string{"", "HPET", "_HID", "hpet", "PCI0.LPCB", "LPCB.HPET", "EC0", "EC0_", "XXXX", "CPU0"} {
			add(map[string]any{"op": "path_of_type", "type": ty, "obj": obj})
		}
	}
	for _, obj := range []string{"HPET", "PCI0", "SAT0", "USB0", "_SB.USB0", "CPU0"} {
		add(map[string]any{"op": "device_paths", "obj": obj})
	}
	add(map[string]any{"op": "method_paths", "obj": "_STA"})
	add(map[string]any{"op": "method_paths", "obj": "_WAK"})
	add(map[string]any{"op": "name_paths", "obj": "_HID"})
	add(map[string]any{"op": "name_paths", "obj": "CPU0"})
	add(map[string]any{"op": "processor_paths", "type": "Processor"})
	for _, h := range []string{"ACPI0001", "acpi000e", "PNP0A08", "PNP0103", "ZZZZ"} {
		add(map[string]any{"op": "hid_paths", "hid": h})
	}
	for _, i := range hexLines {
		raw := strings.SplitN(strings.TrimSpace(lines[i]), ":", 2)[1]
		raw = strings.ReplaceAll(strings.SplitN(raw, "//", 2)[0], " ", "")
		for _, hx := range []string{raw[:4], raw[2:8], raw[:12], raw[len(raw)-6:]} {
			for _, dir := range []string{"both", "back", "fwd"} {
				for _, inst := range []int{0, 1} {
					add(map[string]any{"op": "unique_pad", "hex": hx, "index": i, "direction": dir, "instance": inst})
				}
			}
			add(map[string]any{"op": "shortest_pad", "hex": hx, "index": i})
		}
	}
	// The repeated sequence needs a pad to be unique.
	for _, i := range hexLines[:min(6, len(hexLines))] {
		add(map[string]any{"op": "shortest_pad", "hex": "5C5F5342", "index": i})
		add(map[string]any{"op": "unique_pad", "hex": "5F5342", "index": i, "direction": "both", "instance": 0})
	}
	add(map[string]any{"op": "unique_pad", "hex": "ABCDEF", "index": hexLines[0], "direction": "both", "instance": 0})
	add(map[string]any{"op": "unique_pad", "hex": "", "index": hexLines[0], "direction": "both", "instance": 0})
	add(map[string]any{"op": "unique_pad", "hex": "ABC", "index": hexLines[0], "direction": "both", "instance": 0})
	add(map[string]any{"op": "unique_pad", "hex": "AB", "index": 0, "direction": "both", "instance": 0})
	return steps
}

func hexLineIndexes(d *DSDT, lines []string) []int {
	var out []int
	for i, l := range lines {
		if d.IsHex(l) {
			out = append(out, i)
		}
	}
	return out
}

func standardFiles() map[string]table {
	return map[string]table{
		"DSDT.aml": buildTable(tableOpts{sig: "DSDT", oem: "ALASKA", id: "A M I   ", bodySize: 340, seed: 1}),
		"SSDT.aml": buildTable(tableOpts{sig: "SSDT", oem: "PmRef ", id: "Cpu0Ist ", bodySize: 137, seed: 2, crlf: true}),
		"APIC.aml": buildTable(tableOpts{sig: "APIC", oem: "ALASKA", id: "A M I   ", bodySize: 96, seed: 3}),
	}
}

func dslFor(files map[string]table, names ...string) map[string]string {
	out := map[string]string{}
	for _, n := range names {
		out[n] = files[n].dsl
	}
	return out
}

// loadedLines returns the lines the Go port builds, used to derive queries.
func loadedLines(t *testing.T, sc scenario, name string) []string {
	t.Helper()
	dir, dslDir, iasl := writeScenario(t, sc)
	t.Setenv("FAKE_DSL_DIR", dslDir)
	for k, v := range sc.env {
		t.Setenv(k, v)
	}
	d := New(iasl)
	d.Load(dir)
	tb, ok := d.AcpiTables.Get(name)
	if !ok {
		t.Fatalf("table %s did not load", name)
	}
	return tb.Lines
}

func TestParityLoadAndQueries(t *testing.T) {
	files := standardFiles()
	sc := scenario{
		name:  "directory",
		files: files,
		extra: map[string][]byte{"README.txt": []byte("not a table"), "empty.aml": {}},
		dsl:   dslFor(files, "DSDT.aml", "SSDT.aml", "APIC.aml"),
	}
	lines := loadedLines(t, sc, "DSDT.aml")
	hexes := hexLineIndexes(New(""), lines)
	if len(hexes) < 8 {
		t.Fatalf("expected generated hex lines, got %d", len(hexes))
	}
	sc.steps = func(dir string) []map[string]any {
		steps := []map[string]any{{"op": "load", "path": dir}}
		steps = append(steps, queries(lines, "DSDT.aml", hexes[:10])...)
		steps = append(steps, queries(lines, "", hexes[:3])...)
		steps = append(steps,
			map[string]any{"op": "dsdt_or_only"},
			map[string]any{"op": "table_with_id", "value": hex.EncodeToString([]byte("A M I   "))},
			map[string]any{"op": "table_with_id", "value": hex.EncodeToString([]byte("Cpu0Ist "))},
			map[string]any{"op": "table_with_id", "value": "00"},
			map[string]any{"op": "table_with_sig", "value": hex.EncodeToString([]byte("SSDT"))},
			map[string]any{"op": "table_with_sig", "value": hex.EncodeToString([]byte("APIC"))},
			map[string]any{"op": "table", "value": hex.EncodeToString([]byte("DSDT"))},
			map[string]any{"op": "table", "value": hex.EncodeToString([]byte("Cpu0Ist "))},
			map[string]any{"op": "table", "value": hex.EncodeToString([]byte("none"))},
			map[string]any{"op": "reset"},
			map[string]any{"op": "dsdt_or_only"},
			map[string]any{"op": "load", "path": filepath.Join(dir, "SSDT.aml")},
			map[string]any{"op": "dsdt_or_only"},
			map[string]any{"op": "paths"},
			map[string]any{"op": "devices", "search": "Device ("},
			map[string]any{"op": "load", "path": filepath.Join(dir, "APIC.aml")},
			map[string]any{"op": "dsdt_or_only"},
		)
		return steps
	}
	runScenario(t, sc)
}

func TestParityHelpers(t *testing.T) {
	files := standardFiles()
	sc := scenario{name: "helpers", files: files, extra: map[string][]byte{"x.txt": []byte("hi"), "tiny.aml": []byte("DS")}, dsl: dslFor(files, "DSDT.aml")}
	sc.steps = func(dir string) []map[string]any {
		var steps []map[string]any
		for _, total := range []int{0, 1, 255, 256, 4096, 65535, 70000, 0x12345678, -5, -300} {
			for _, pad := range []int{0, 2, 4, 8} {
				steps = append(steps, map[string]any{"op": "hex_from_int", "total": total, "pad_to": pad})
			}
			steps = append(steps, map[string]any{"op": "hex_from_int", "total": total})
		}
		steps = append(steps,
			map[string]any{"op": "ascii", "data": []int{}},
			map[string]any{"op": "ascii", "data": []int{65, 66, 67}},
			map[string]any{"op": "ascii", "data": []int{32, 126, 127, 0, 200, 31, 33}},
			map[string]any{"op": "ascii", "data": []int{126}},
		)
		for _, l := range []string{
			"    0024: 10 4F", "0024: 10", "   00F0: AA BB CC  // c", "0024:10 4F", "abcd: 10 4F",
			"   024: 10 4F", "   12345678: 10 4F 5C", "    0024: 10 4F  // x // y", "    0024: 10 4F //", "    0024: 10 4F\t//x",
			"Device (PCI0)", "", "   0024:", "  0024: 10 4F 5",
		} {
			steps = append(steps, map[string]any{"op": "is_hex", "line": l})
		}
		for _, l := range []string{"    0024: 10 4F  // x", "0024: 10 4F", "x:y:z // c: d"} {
			steps = append(steps, map[string]any{"op": "get_hex", "line": l})
		}
		for _, l := range []string{"    0024: 10 4F  // x: y", "no colon // c", "a:b:c", "// only comment: here", ""} {
			steps = append(steps, map[string]any{"op": "get_line", "line": l})
		}
		for _, name := range []string{"DSDT.aml", "x.txt", "missing.aml", "tiny.aml", ""} {
			steps = append(steps,
				map[string]any{"op": "valid", "path": dir, "name": name},
				map[string]any{"op": "signature", "path": dir, "name": name},
			)
		}
		steps = append(steps,
			map[string]any{"op": "signature", "path": filepath.Join(dir, "DSDT.aml")},
			map[string]any{"op": "valid", "path": filepath.Join(dir, "DSDT.aml")},
			map[string]any{"op": "valid", "path": dir},
		)
		return steps
	}
	runScenario(t, sc)
}

func TestParityLoadFailures(t *testing.T) {
	files := standardFiles()
	loadOnly := func(extraPaths ...string) func(dir string) []map[string]any {
		return func(dir string) []map[string]any {
			steps := []map[string]any{{"op": "load", "path": dir}, {"op": "dsdt_or_only"}, {"op": "paths"}}
			for _, p := range extraPaths {
				steps = append(steps, map[string]any{"op": "load", "path": filepath.Join(dir, p)}, map[string]any{"op": "dsdt_or_only"})
			}
			return steps
		}
	}
	unprintable := buildTable(tableOpts{sig: "DSDT", oem: "AL\x01SKA", id: "A\xffM I   ", bodySize: 120, seed: 9})
	short := table{raw: append([]byte("DSDT"), 0, 0, 0, 0, 2, 0, 'A', 'B', 'C', 'D', 'E', 'F', 'I', 'D'), dsl: files["DSDT.aml"].dsl}
	cases := []scenario{
		{name: "retry without -da", files: files, dsl: dslFor(files, "DSDT.aml", "SSDT.aml", "APIC.aml"),
			env: map[string]string{"FAKE_FAIL_DA": "1"}, steps: loadOnly()},
		{name: "individual retry", files: files, dsl: dslFor(files, "DSDT.aml", "SSDT.aml", "APIC.aml"),
			env: map[string]string{"FAKE_FAIL_MULTI": "1"}, steps: loadOnly()},
		{name: "one table missing", files: files, dsl: dslFor(files, "DSDT.aml", "APIC.aml"), steps: loadOnly()},
		{name: "other table missing", files: files, dsl: dslFor(files, "DSDT.aml", "SSDT.aml"), steps: loadOnly()},
		{name: "everything fails", files: files, dsl: map[string]string{}, steps: loadOnly()},
		{name: "no tables", files: map[string]table{}, extra: map[string][]byte{"a.txt": []byte("x")}, steps: loadOnly()},
		{name: "single file", files: files, dsl: dslFor(files, "DSDT.aml"), steps: loadOnly("DSDT.aml", "SSDT.aml", "missing.aml")},
		{name: "unprintable header", files: map[string]table{"DSDT.aml": unprintable}, dsl: dslFor(map[string]table{"DSDT.aml": unprintable}, "DSDT.aml"), steps: loadOnly()},
		{name: "short table", files: map[string]table{"DSDT.aml": short}, dsl: dslFor(map[string]table{"DSDT.aml": short}, "DSDT.aml"), steps: loadOnly()},
		{name: "invalid utf8", files: map[string]table{"DSDT.aml": files["DSDT.aml"]}, dsl: map[string]string{"DSDT.aml": "/* x */\nDevice (A)\n\xff\xfe\n"}, steps: loadOnly()},
		{name: "lone carriage returns", files: map[string]table{"DSDT.aml": files["DSDT.aml"]}, dsl: map[string]string{"DSDT.aml": "/* c */\rDevice (A)\r{\r    Name (_HID, \"X\")\r}\r"}, steps: loadOnly()},
		{name: "no header comment", files: map[string]table{"DSDT.aml": files["DSDT.aml"]}, dsl: map[string]string{"DSDT.aml": "Device (PCI0)\n{\n    Name (_HID, \"X\")\n}\n"}, steps: loadOnly()},
		{name: "header cut twice", files: map[string]table{"DSDT.aml": files["DSDT.aml"]}, dsl: map[string]string{"DSDT.aml": "/* c */\nDevice (A)\n{\n}\nRaw Table Data: Length 5\n\nTable Header:\nx\nTable Header:\ny\n"}, steps: loadOnly()},
		{name: "raw table data footer", files: map[string]table{"DSDT.aml": files["DSDT.aml"]}, dsl: map[string]string{"DSDT.aml": "/* c */  \nDevice (A)\n{\n}\n\nRaw Table Data: Length 5\n   0000: 00\n"}, steps: loadOnly()},
	}
	for _, sc := range cases {
		sc := sc
		t.Run(sc.name, func(t *testing.T) { runScenario(t, sc) })
	}
}
