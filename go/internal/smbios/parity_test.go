package smbios

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// serialScenario is shared by the Go test and the Python oracle.
type serialScenario struct {
	Name          string   `json:"name"`
	OS            string   `json:"os"`
	Model         string   `json:"model"`
	Mac           string   `json:"mac"`
	UUID          string   `json:"uuid"`
	Stdout        string   `json:"stdout"`
	RC            int      `json:"rc"`
	Binaries      []string `json:"binaries"`
	GatherCreates []string `json:"gather_creates"`
	History       *string  `json:"history"`
}

func strPtr(s string) *string { return &s }

var serialScenarios = []serialScenario{
	{Name: "linux found", OS: "Linux", Model: "MacPro7,1", Mac: "AABBCCDDEEFF",
		UUID: "0F1E2D3C-4B5A-4978-8695-A4B3C2D1E0F9", Stdout: "C02TEST123 | C02MLBTEST456\nsecond line\n", RC: 0,
		Binaries: []string{"macserial.linux"}},
	{Name: "windows found", OS: "Windows", Model: "iMac20,1", Mac: "0102030405AB",
		UUID: "11111111-2222-4333-8444-555555555555", Stdout: "A1 | B2 | C3", RC: 0,
		Binaries: []string{"macserial.exe"}},
	{Name: "darwin found", OS: "Darwin", Model: "MacBookPro16,1", Mac: "F0E1D2C3B4A5",
		UUID: "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE", Stdout: "SER | MLB", RC: 0,
		Binaries: []string{"macserial"}},
	{Name: "non-zero return code", OS: "Linux", Model: "MacPro7,1", Mac: "00FF00FF00FF",
		UUID: "22222222-3333-4444-8555-666666666666", Stdout: "SER | MLB", RC: 1,
		Binaries: []string{"macserial.linux"}},
	{Name: "no separator", OS: "Linux", Model: "MacPro7,1", Mac: "010203040506",
		UUID: "33333333-4444-4555-8666-777777777777", Stdout: "just text", RC: 0,
		Binaries: []string{"macserial"}},
	{Name: "empty output", OS: "Linux", Model: "MacPro7,1", Mac: "101112131415",
		UUID: "44444444-5555-4666-8777-888888888888", Stdout: "", RC: 0,
		Binaries: []string{"macserial"}},
	{Name: "leading blank line", OS: "Linux", Model: "MacPro7,1", Mac: "202122232425",
		UUID: "55555555-6666-4777-8888-999999999999", Stdout: "\nSER | MLB", RC: 0,
		Binaries: []string{"macserial"}},
	{Name: "missing then gathered", OS: "Linux", Model: "MacPro7,1", Mac: "303132333435",
		UUID: "66666666-7777-4888-8999-aaaaaaaaaaaa", Stdout: "X | Y", RC: 0,
		GatherCreates: []string{"macserial.linux"}},
	{Name: "never appears", OS: "Linux", Model: "MacPro7,1", Mac: "404142434445",
		UUID: "77777777-8888-4999-8aaa-bbbbbbbbbbbb", Stdout: "X | Y", RC: 0},
	{Name: "history cleanup on retry", OS: "Linux", Model: "MacPro7,1", Mac: "505152535455",
		UUID: "88888888-9999-4aaa-8bbb-cccccccccccc", Stdout: "X | Y", RC: 0,
		GatherCreates: []string{"macserial.linux"},
		History: strPtr(`[
    {
        "id": 1,
        "product_name": "Lilu",
        "sha256": null,
        "url": "https://example.test/Lilu.zip"
    },
    {
        "id": 2,
        "product_name": "OpenCorePkg",
        "sha256": null,
        "url": "https://example.test/OpenCore.zip"
    }
]`)},
	{Name: "history item is not a dict", OS: "Linux", Model: "MacPro7,1", Mac: "606162636465",
		UUID: "99999999-aaaa-4bbb-8ccc-dddddddddddd", Stdout: "X | Y", RC: 0,
		History: strPtr(`["not a dict"]`)},
	{Name: "unknown OS", OS: "FreeBSD", Model: "MacPro7,1", Mac: "707172737475",
		UUID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Stdout: "X | Y", RC: 0,
		Binaries: []string{"macserial"}},
}

// TestParityWithPythonSerial runs the same scenarios through the original
// generate_smbios and the Go port, with the executable replaced by a fixed
// result, and compares the returned values, the errors and the history file.
func TestParityWithPythonSerial(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(serialScenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_serial.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
	}
	var py []struct {
		OK      *string `json:"ok"`
		Error   *string `json:"error"`
		History *string `json:"history"`
	}
	if err := json.Unmarshal(out, &py); err != nil {
		t.Fatal(err)
	}

	for i, sc := range serialScenarios {
		t.Run(sc.Name, func(t *testing.T) {
			dir := t.TempDir()
			scriptDir := filepath.Join(dir, "Scripts")
			if err := os.MkdirAll(scriptDir, 0o755); err != nil {
				t.Fatal(err)
			}
			historyFile := filepath.Join(dir, "history.json")
			for _, name := range sc.Binaries {
				writeFile(t, filepath.Join(scriptDir, name), "")
			}
			if sc.History != nil {
				writeFile(t, historyFile, *sc.History)
			}

			s := &Serial{
				ScriptDir:   scriptDir,
				OS:          sc.OS,
				HistoryFile: historyFile,
				Run:         func([]string) (string, int) { return sc.Stdout, sc.RC },
				Gather: func() error {
					for _, name := range sc.GatherCreates {
						writeFile(t, filepath.Join(scriptDir, name), "")
					}
					return nil
				},
				RandomMACPart: func() string { return sc.Mac },
				NewUUID:       func() string { return strings.ToUpper(sc.UUID) }, // str(uuid4()).upper()
			}

			value, goErr := s.GenerateSMBIOS(sc.Model)
			pyCase := py[i]
			if pyCase.Error != nil {
				if goErr == nil {
					t.Errorf("python raised %s; go returned a value", *pyCase.Error)
				}
			} else if goErr != nil {
				t.Errorf("go error %v; python ok", goErr)
			} else {
				got, err := pyjson.Dump(value)
				if err != nil {
					t.Fatal(err)
				}
				if got != *pyCase.OK {
					t.Errorf("values differ:\n go: %s\n py: %s", got, *pyCase.OK)
				}
			}

			goHistory := readOptional(historyFile)
			if !sameOptional(goHistory, pyCase.History) {
				t.Errorf("history differs:\n go: %v\n py: %v", goHistory, pyCase.History)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readOptional(path string) *string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := string(data)
	return &s
}

func sameOptional(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
