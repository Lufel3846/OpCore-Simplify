package pyrt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// pyCase is one operator evaluation. Both sides receive the same JSON text.
type pyCase struct {
	Op string          `json:"op"`
	A  json.RawMessage `json:"a,omitempty"`
	B  json.RawMessage `json:"b,omitempty"`
}

type pyResult struct {
	OK    json.RawMessage `json:"ok"`
	Error string          `json:"error"`
}

// loadJSON reads a JSON value with the same loader the report uses.
func loadJSON(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	v, err := pyjson.Load(raw)
	if err != nil {
		t.Fatalf("load %s: %v", raw, err)
	}
	return v
}

var inCases = [][2]string{
	{`"a"`, `"abc"`}, {`"x"`, `"abc"`}, {`1`, `"abc"`}, {`""`, `"abc"`},
	{`"a"`, `["a", "b"]`}, {`1`, `[1.0]`}, {`true`, `[1]`}, {`null`, `["a"]`},
	{`"k"`, `{"k": 1}`}, {`1`, `{"1": 2}`}, {`"z"`, `{"k": 1}`},
	{`"a"`, `5`}, {`null`, `null`}, {`"a"`, `null`},
	{`[1, 2]`, `[[1, 2], [3]]`}, {`[1, 2]`, `[[2, 1]]`},
}

var eqCases = [][2]string{
	{`1`, `1.0`}, {`true`, `1`}, {`0`, `false`}, {`"a"`, `"a"`}, {`null`, `null`},
	{`[1, "a"]`, `[1, "a"]`}, {`{"a": 1}`, `{"a": 1.0}`}, {`"1"`, `1`},
	{`null`, `0`}, {`{"a": 1}`, `{"a": 2}`}, {`2.5`, `2.5`}, {`[1]`, `[1, 2]`},
}

var truthyCases = []string{
	`""`, `"x"`, `0`, `0.0`, `1`, `-1`, `[]`, `[0]`, `{}`, `{"a": 0}`, `null`, `false`, `true`,
}

var lenCases = []string{
	`"héllo"`, `""`, `[]`, `[1, 2, 3]`, `{"a": 1}`, `5`, `null`,
}

var intCases = []string{
	`"42"`, `" 42 "`, `"1_000"`, `"-7"`, `"+3"`, `"4.5"`, `""`, `"0x1F"`,
	`4.9`, `-4.9`, `true`, `false`, `null`, `"123456789012345678901234567890"`, `1e400`,
	`"1__0"`, `[1]`,
}

// TestParityWithPythonOperators evaluates each operator with Python and with
// pyrt on the same inputs. Errors must occur in the same cases.
func TestParityWithPythonOperators(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}

	var cases []pyCase
	for _, c := range inCases {
		cases = append(cases, pyCase{Op: "in", A: json.RawMessage(c[0]), B: json.RawMessage(c[1])})
	}
	for _, c := range eqCases {
		cases = append(cases, pyCase{Op: "eq", A: json.RawMessage(c[0]), B: json.RawMessage(c[1])})
	}
	for _, c := range truthyCases {
		cases = append(cases, pyCase{Op: "truthy", A: json.RawMessage(c)})
	}
	for _, c := range lenCases {
		cases = append(cases, pyCase{Op: "len", A: json.RawMessage(c)})
	}
	for _, c := range intCases {
		cases = append(cases, pyCase{Op: "int", A: json.RawMessage(c)})
	}

	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_pyrt.py"))
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var results []pyResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}

	for i, c := range cases {
		py := results[i]
		switch c.Op {
		case "in":
			got, err := In(loadJSON(t, c.A), loadJSON(t, c.B))
			checkBool(t, c, got, err, py)
		case "eq":
			got := Equal(loadJSON(t, c.A), loadJSON(t, c.B))
			checkBool(t, c, got, nil, py)
		case "truthy":
			checkBool(t, c, Truthy(loadJSON(t, c.A)), nil, py)
		case "len":
			got, err := Len(loadJSON(t, c.A))
			checkInt(t, c, int64(got), err, py)
		case "int":
			got, err := Int(loadJSON(t, c.A))
			if py.Error != "" {
				if err == nil {
					t.Errorf("int %s: python raised %s; go returned %v", c.A, py.Error, got)
				}
				continue
			}
			if err != nil {
				t.Errorf("int %s: go error %v; python %s", c.A, err, py.OK)
				continue
			}
			want := new(big.Int)
			if _, ok := want.SetString(string(py.OK), 10); !ok {
				t.Fatalf("int %s: python value %s is not an integer", c.A, py.OK)
			}
			if fmt.Sprint(got) != want.String() {
				t.Errorf("int %s: go %v; python %s", c.A, got, py.OK)
			}
		}
	}
}

func checkBool(t *testing.T, c pyCase, got bool, err error, py pyResult) {
	t.Helper()
	if py.Error != "" {
		if err == nil {
			t.Errorf("%s %s %s: python raised %s; go returned %v", c.Op, c.A, c.B, py.Error, got)
		}
		return
	}
	if err != nil {
		t.Errorf("%s %s %s: go error %v; python %s", c.Op, c.A, c.B, err, py.OK)
		return
	}
	if want := string(py.OK) == "true"; got != want {
		t.Errorf("%s %s %s: go %v; python %s", c.Op, c.A, c.B, got, py.OK)
	}
}

func checkInt(t *testing.T, c pyCase, got int64, err error, py pyResult) {
	t.Helper()
	if py.Error != "" {
		if err == nil {
			t.Errorf("%s %s: python raised %s; go returned %d", c.Op, c.A, py.Error, got)
		}
		return
	}
	if err != nil {
		t.Errorf("%s %s: go error %v; python %s", c.Op, c.A, err, py.OK)
		return
	}
	if fmt.Sprint(got) != string(py.OK) {
		t.Errorf("%s %s: go %d; python %s", c.Op, c.A, got, py.OK)
	}
}

// TestPythonErrorsAreReported checks the error values, which callers test with
// errors.Is.
func TestPythonErrorsAreReported(t *testing.T) {
	if _, err := In("a", nil); !errors.Is(err, ErrType) {
		t.Errorf("In on None: %v; want TypeError", err)
	}
	if _, err := Get("not a dict", "k"); !errors.Is(err, ErrAttribute) {
		t.Errorf("Get on str: %v; want AttributeError", err)
	}
	if _, err := Index(nil, "k"); err == nil {
		t.Error("Index on None returned no error")
	}
}
