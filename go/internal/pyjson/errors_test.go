package pyjson

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"os/exec"
	"testing"
)

type errCase struct {
	Hex  string `json:"hex"`
	Mode string `json:"mode"`
}

var errSeeds = []string{
	`{"a": 1, "b": [true, false, null, 1.5, "x"], "c": {"d": "e\n\u00e9\ud83d\ude00"}}`,
	`[1, 2, {"k": "v"}, [], {}]`,
	`{"Motherboard": {"Name": "Board", "Platform": "Desktop"}, "CPU": {"Core Count": "8"}}`,
	"{\n    \"a\": 1,\n    \"b\": [\n        2,\n        3\n    ]\n}\n",
	`"just a string with \\ and \/ and \b\f\n\r\t"`,
	`[-0, 0, 10, -12.5e3, 1E+2, 2e-3, 1e400, NaN, Infinity, -Infinity]`,
}

var errPieces = []string{
	"", " ", "\n", "\r\n", "\r", "\t", "\x00", "\x01", "\x1f", "\"", "\\", "\\u", "\\u12", "\\ud800", "\\udc00", "\\ud800\\u0041",
	"\\ud800\\udc00", "\\x", "\\\"", "/", "{", "}", "[", "]", ",", ":", ",,", ",}", ",]", "-", "-0", "0", "01", "1.", ".5", "1e", "1e+", "e5",
	"nul", "null", "tru", "true", "fals", "false", "NaN", "Nan", "Infinity", "-Infinity", "-Inf", "é", "日本", "😀", "\ufeff",
	"\xff", "\xc3", "\xc3\x28", "\xe2\x82", "\xe2\x28\xa1", "\xe0\x80\x80", "\xed\xa0\x80", "\xf0\x80\x80\x80", "\xf4\x90\x80\x80", "\xf8", "\x80",
	"\xe2\x82\xac", "\xf0\x9f\x98\x80", "\xf0\x9f\x98", "\xf0\x9f", "\xf0",
}

func mutate(rng *rand.Rand, seed []byte) []byte {
	b := append([]byte{}, seed...)
	for n := rng.Intn(3) + 1; n > 0; n-- {
		piece := []byte(errPieces[rng.Intn(len(errPieces))])
		switch rng.Intn(4) {
		case 0: // insert
			at := rng.Intn(len(b) + 1)
			b = append(b[:at:at], append(piece, b[at:]...)...)
		case 1: // replace one byte
			if len(b) > 0 {
				at := rng.Intn(len(b))
				b = append(b[:at:at], append(piece, b[at+1:]...)...)
			}
		case 2: // delete a range
			if len(b) > 0 {
				at := rng.Intn(len(b))
				end := at + rng.Intn(4) + 1
				if end > len(b) {
					end = len(b)
				}
				b = append(b[:at:at], b[end:]...)
			}
		default: // truncate
			if len(b) > 0 {
				b = b[:rng.Intn(len(b))]
			}
		}
	}
	return b
}

// TestParityJSONErrors compares the exception text of json.load/json.loads
// with the Go decoder's, on curated and mutated documents.
func TestParityJSONErrors(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var inputs [][]byte
	for _, p := range errPieces {
		inputs = append(inputs, []byte(p))
		inputs = append(inputs, []byte(`{"a":`+p+`}`), []byte(`[`+p+`]`), []byte(`"`+p+`"`), []byte(`{"a": 1`+p), []byte(`[1`+p))
	}
	for i := 0; i < 4000; i++ {
		inputs = append(inputs, mutate(rng, []byte(errSeeds[rng.Intn(len(errSeeds))])))
	}
	var cases []errCase
	for _, in := range inputs {
		cases = append(cases, errCase{hex.EncodeToString(in), "file"})
		// json.loads(bytes) guesses UTF-16/32 from NUL bytes and BOMs; only UTF-8 is supported.
		head := in[:min(4, len(in))]
		if !bytes.Contains(head, []byte{0}) && !bytes.HasPrefix(in, []byte{0xff, 0xfe}) && !bytes.HasPrefix(in, []byte{0xfe, 0xff}) {
			cases = append(cases, errCase{hex.EncodeToString(in), "loads"})
		}
	}
	payload, _ := json.Marshal(cases)
	cmd := exec.Command("python3", "-I", "testdata/pyref_jsonerr.py")
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	var results []struct {
		OK      *string `json:"ok"`
		Type    string  `json:"type"`
		Message string  `json:"message"`
	}
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	failures, errorsSeen, okSeen := 0, 0, 0
	for i, c := range cases {
		in, _ := hex.DecodeString(c.Hex)
		var v any
		var gerr error
		if c.Mode == "file" {
			v, gerr = LoadFile(in)
		} else {
			v, gerr = Load(in)
		}
		py := results[i]
		if py.OK != nil {
			okSeen++
			if gerr != nil {
				t.Errorf("%s %q: Go error %q; Python ok", c.Mode, in, gerr)
				failures++
			} else if got, _ := Dump(v); got != *py.OK {
				t.Errorf("%s %q: value differs\n go: %s\n py: %s", c.Mode, in, got, *py.OK)
				failures++
			}
		} else {
			errorsSeen++
			want := py.Message
			var typ string
			var de *DecodeError
			var ue *UnicodeDecodeError
			switch {
			case errors.As(gerr, &de):
				typ = "JSONDecodeError"
			case errors.As(gerr, &ue):
				typ = "UnicodeDecodeError"
			}
			if gerr == nil || typ != py.Type || gerr.Error() != want {
				t.Errorf("%s %q:\n go: %v (%s)\n py: %s (%s)", c.Mode, in, gerr, typ, want, py.Type)
				failures++
			}
		}
		if failures > 25 {
			t.Fatal("too many differences")
		}
	}
	if errorsSeen < 2000 || okSeen < 500 {
		t.Errorf("weak coverage: %d errors, %d successes", errorsSeen, okSeen)
	}
}
