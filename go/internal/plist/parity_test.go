package plist

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// dumpCase is a Go value that is written with Marshal and plistlib.dumps.
type dumpCase struct {
	name  string
	value any
}

// loadCase is an XML document read with Unmarshal and plistlib.loads.
type loadCase struct {
	name string
	xml  string
}

// toTagged converts a Go value to the tagged JSON form read by pyref_plist.py.
// It is test plumbing only: the expected results come from Python.
func toTagged(v any) any {
	switch x := v.(type) {
	case nil:
		return map[string]any{"t": "none"}
	case *Dict:
		var pairs []any
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			pairs = append(pairs, []any{k, toTagged(val)})
		}
		if pairs == nil {
			pairs = []any{}
		}
		return map[string]any{"t": "dict", "v": pairs}
	case []any:
		items := []any{}
		for _, item := range x {
			items = append(items, toTagged(item))
		}
		return map[string]any{"t": "list", "v": items}
	case string:
		return map[string]any{"t": "str", "v": x}
	case bool:
		return map[string]any{"t": "bool", "v": x}
	case int64:
		return map[string]any{"t": "int", "v": strconv.FormatInt(x, 10)}
	case *big.Int:
		return map[string]any{"t": "int", "v": x.String()}
	case float64:
		// Python's float() accepts this exact round-trip form, including inf and nan.
		return map[string]any{"t": "float", "v": strconv.FormatFloat(x, 'g', -1, 64)}
	case []byte:
		return map[string]any{"t": "data", "v": base64.StdEncoding.EncodeToString(x)}
	case time.Time:
		return map[string]any{"t": "date", "v": x.Format("2006-01-02T15:04:05")}
	}
	panic(fmt.Sprintf("toTagged: unsupported %T", v))
}

// resultTagged converts a Go value returned by Unmarshal to the tagged form of
// the Python result. Floats use the Go repr, which is the code under test.
func resultTagged(v any) any {
	switch x := v.(type) {
	case float64:
		return map[string]any{"t": "float", "v": pyFloatRepr(x)}
	case *Dict:
		var pairs []any
		for _, k := range x.Keys() {
			val, _ := x.Get(k)
			pairs = append(pairs, []any{k, resultTagged(val)})
		}
		if pairs == nil {
			pairs = []any{}
		}
		return map[string]any{"t": "dict", "v": pairs}
	case []any:
		items := []any{}
		for _, item := range x {
			items = append(items, resultTagged(item))
		}
		return map[string]any{"t": "list", "v": items}
	}
	return toTagged(v)
}

func dumpCases() []dumpCase {
	bigPositive, _ := new(big.Int).SetString("18446744073709551615", 10) // 2^64-1
	bigOverflow, _ := new(big.Int).SetString("18446744073709551616", 10) // 2^64
	bigNegative, _ := new(big.Int).SetString("-9223372036854775809", 10) // -2^63-1
	sample := make([]byte, 300)
	for i := range sample {
		sample[i] = byte(i*7 + 3)
	}
	nested := NewDict()
	nested.Set("Array", []any{"a", int64(1), NewDict(), []any{}, true, false})
	nested.Set("Empty", NewDict())
	nested.Set("Data", []byte{})
	nested.Set("Escapes", "a&b<c>d \"e\" 'f'")
	nested.Set("Lines", "x\r\ny\rz")
	nested.Set("Unicode", "çãé 日本")
	nested.Set("Keys with spaces", int64(-5))

	return []dumpCase{
		{"empty dict", NewDict()},
		{"empty array", []any{}},
		{"empty string", ""},
		{"nested", nested},
		{"escapes in key", func() *Dict { d := NewDict(); d.Set("a&<b>", "v"); return d }()},
		{"control char rejected", "bad\x01char"},
		{"int zero", int64(0)},
		{"int negative", int64(-1)},
		{"int64 max", int64(9223372036854775807)},
		{"int64 min", int64(-9223372036854775808)},
		{"uint64 max", bigPositive},
		{"uint64 overflow rejected", bigOverflow},
		{"below int64 rejected", bigNegative},
		{"float zero", 0.0},
		{"float negative zero", math.Copysign(0, -1)},
		{"float one", 1.0},
		{"float tenth", 0.1},
		{"float 1e15", 1e15},
		{"float 1e16", 1e16},
		{"float 1e-4", 1e-4},
		{"float 1e-5", 1e-5},
		{"float 123456.789", 123456.789},
		{"float 1.5e-7", 1.5e-7},
		{"float 2.5e+20", 2.5e20},
		{"float max", math.MaxFloat64},
		{"float min subnormal", 5e-324},
		{"float inf", math.Inf(1)},
		{"float -inf", math.Inf(-1)},
		{"float nan", math.NaN()},
		{"data empty", []byte{}},
		{"data one byte", []byte{0x41}},
		{"data 57 bytes", sample[:57]},
		{"data 58 bytes", sample[:58]},
		{"data 300 bytes", sample},
		{"bool true", true},
		{"bool false", false},
		{"date", time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"date leap", time.Date(2024, 2, 29, 23, 59, 59, 0, time.UTC)},
	}
}

func loadCases() []loadCase {
	return []loadCase{
		{"empty dict", `<plist version="1.0"><dict/></plist>`},
		{"root string", `<plist version="1.0"><string>hi</string></plist>`},
		{"no root value", `<plist version="1.0"></plist>`},
		{"unknown element children kept", `<plist version="1.0"><foo><string>x</string></foo></plist>`},
		{"entities", `<plist version="1.0"><string>a &amp; b &lt;c&gt; &#x41;&#66;</string></plist>`},
		{"empty key", `<plist version="1.0"><dict><key></key><string>v</string></dict></plist>`},
		{"duplicate key keeps first position", `<plist version="1.0"><dict><key>a</key><integer>1</integer><key>b</key><integer>2</integer><key>a</key><integer>3</integer></dict></plist>`},
		{"key without value", `<plist version="1.0"><dict><key>a</key></dict></plist>`},
		{"two keys in a row", `<plist version="1.0"><dict><key>a</key><key>b</key><integer>1</integer></dict></plist>`},
		{"value outside dict", `<plist version="1.0"><dict><integer>1</integer></dict></plist>`},
		{"integer with spaces", `<plist version="1.0"><integer> 42 </integer></plist>`},
		{"integer underscores", `<plist version="1.0"><integer>1_000</integer></plist>`},
		{"integer hex", `<plist version="1.0"><integer>0x1F</integer></plist>`},
		{"integer hex upper", `<plist version="1.0"><integer>0XfF</integer></plist>`},
		{"integer negative hex rejected", `<plist version="1.0"><integer>-0x1</integer></plist>`},
		{"integer bad underscore", `<plist version="1.0"><integer>1__0</integer></plist>`},
		{"integer beyond 64 bits", `<plist version="1.0"><integer>123456789012345678901234567890</integer></plist>`},
		{"integer empty", `<plist version="1.0"><integer></integer></plist>`},
		{"real plain", `<plist version="1.0"><real>2.5</real></plist>`},
		{"real underscore", `<plist version="1.0"><real>1_0.5</real></plist>`},
		{"real spaces", `<plist version="1.0"><real>  -2.5e3 </real></plist>`},
		{"real inf nan", `<plist version="1.0"><array><real>inf</real><real>-Infinity</real><real>nan</real></array></plist>`},
		{"real overflow", `<plist version="1.0"><real>1e400</real></plist>`},
		{"real hex rejected", `<plist version="1.0"><real>0x1p3</real></plist>`},
		{"real bad", `<plist version="1.0"><real>abc</real></plist>`},
		{"data padded", `<plist version="1.0"><data>QQ==</data></plist>`},
		{"data unpadded", `<plist version="1.0"><data>QQ</data></plist>`},
		{"data one char", `<plist version="1.0"><data>Q</data></plist>`},
		{"data with newlines", `<plist version="1.0"><data>
			QUJD
			RA==
		</data></plist>`},
		{"data empty", `<plist version="1.0"><data></data></plist>`},
		{"data three bytes", `<plist version="1.0"><data>QUJD</data></plist>`},
		// Known divergence, not compared: CPython keeps decoding after padding that
		// appears mid-stream ("QUJDRA==QQ==" gives "ABCD\x04\x10"). plistlib never
		// writes that shape, so it only occurs in malformed files. See go/README.md.
		{"data bad pad", `<plist version="1.0"><data>QUJDRA=</data></plist>`},
		{"date full", `<plist version="1.0"><date>2020-01-02T03:04:05Z</date></plist>`},
		{"date day only", `<plist version="1.0"><date>2020-01-02Z</date></plist>`},
		{"date hour only", `<plist version="1.0"><date>2020-01-02T03Z</date></plist>`},
		{"date year only", `<plist version="1.0"><date>2020Z</date></plist>`},
		{"date month invalid", `<plist version="1.0"><date>2020-13-01Z</date></plist>`},
		{"date day invalid", `<plist version="1.0"><date>2020-02-30Z</date></plist>`},
		{"date leap ok", `<plist version="1.0"><date>2024-02-29T00:00:00Z</date></plist>`},
		{"date fraction", `<plist version="1.0"><date>2020-01-02T03:04:05.5Z</date></plist>`},
		{"date trailing text", `<plist version="1.0"><date>2020-01-02T03:04:05Zxyz</date></plist>`},
		{"nested arrays", `<plist version="1.0"><array><array><integer>1</integer></array><dict/></array></plist>`},
		{"booleans", `<plist version="1.0"><array><true/><false/></array></plist>`},
		{"entity declaration rejected", `<?xml version="1.0"?><!DOCTYPE plist [<!ENTITY x "y">]><plist version="1.0"><string>&x;</string></plist>`},
		{"malformed xml", `<plist version="1.0"><dict><key>a</dict></plist>`},
		{"unknown entity", `<plist version="1.0"><string>&nope;</string></plist>`},
	}
}

// TestParityWithPythonPlistlib checks the XML writer byte-for-byte and the XML
// reader value-for-value against the original plistlib, including error cases.
func TestParityWithPythonPlistlib(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	dumps := dumpCases()
	loads := loadCases()

	var reqs []map[string]any
	for _, c := range dumps {
		reqs = append(reqs, map[string]any{"op": "dumps", "v": toTagged(c.value)})
	}
	for _, c := range loads {
		reqs = append(reqs, map[string]any{"op": "loads", "xml": c.xml})
	}
	results := runPython(t, python, reqs)

	for i, c := range dumps {
		py := results[i]
		got, err := Marshal(c.value)
		switch {
		case py.Error != "":
			if err == nil {
				t.Errorf("dumps %s: python raised %s, go returned %q", c.name, py.Error, got)
			}
		case err != nil:
			t.Errorf("dumps %s: go error %v; python returned %q", c.name, err, py.OK)
		case string(got) != py.OK:
			t.Errorf("dumps %s differs:\n go: %q\n py: %q", c.name, got, py.OK)
		}
	}

	for i, c := range loads {
		py := results[len(dumps)+i]
		got, err := Unmarshal([]byte(c.xml))
		switch {
		case py.Error != "":
			if err == nil {
				t.Errorf("loads %s: python raised %s, go returned %v", c.name, py.Error, got)
			}
		case err != nil:
			t.Errorf("loads %s: go error %v; python returned %v", c.name, err, py.OK)
		case !reflect.DeepEqual(resultTagged(got), py.OK):
			t.Errorf("loads %s differs:\n go: %v\n py: %v", c.name, resultTagged(got), py.OK)
		}
	}
	t.Logf("compared %d dumps and %d loads cases against plistlib", len(dumps), len(loads))
}

type pyResult struct {
	OK    any    `json:"ok"`
	Error string `json:"error"`
}

func runPython(t *testing.T, python string, reqs []map[string]any) []pyResult {
	t.Helper()
	input, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_plist.py"))
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
