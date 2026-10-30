package fsutil

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

var pyjsonLoad = pyjson.Load

// TestParityWithPythonUtils compares FindMatchingPaths with the original
// find_matching_paths on a tree that has nested folders, hidden entries and
// mixed-case extensions, and compares the bytes written by WriteFile with the
// original write_file for .json and .plist.
func TestParityWithPythonUtils(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	for _, p := range []string{
		"Kexts/Lilu.kext/Contents/Info.plist", "Kexts/VirtualSMC.KEXT/x", "Kexts/.hidden.kext/y",
		"EFI/Drivers/HfsPlus.efi", "EFI/Drivers/ApfsDriverLoader.efi", "EFI/Tools/ChipTune.EFI",
		"EFI/OC/Resources/Image/Acidanthera/GoldenGate/Left.icns", "readme.md", ".DS_Store",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("x"), 0o644)
	}

	cases := []struct{ ext, name, typ string }{
		{"", "", ""}, {"", "", "dir"}, {"", "", "file"},
		{".kext", "", ""}, {".efi", "", "file"}, {"", "EFI", "dir"}, {"", "Resources", "dir"},
		{"", "Driver", ""},
	}
	var finds []map[string]string
	for _, c := range cases {
		finds = append(finds, map[string]string{"root": root, "ext": c.ext, "name": c.name, "type": c.typ})
	}

	plistSource := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleName</key><string>Lilu &amp; co</string><key>Values</key><array><integer>7</integer><real>0.1</real><true/></array></dict></plist>`

	pyOut := t.TempDir()
	jsonText := `{"zeta": 1, "alpha": [true, "ü", 2.5], "nested": {"b": null}}`
	request := map[string]any{
		"find": finds,
		"write": map[string]string{
			"out_json":  filepath.Join(pyOut, "out.json"),
			"out_plist": filepath.Join(pyOut, "out.plist"),
			"json_text": jsonText,
			"plist_b64": base64.StdEncoding.EncodeToString([]byte(plistSource)),
		},
	}
	input, _ := json.Marshal(request)
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_fsutil.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var py struct {
		Find  [][][2]string `json:"find"`
		JSON  string        `json:"json"`
		Plist string        `json:"plist"`
	}
	if err := json.Unmarshal(out, &py); err != nil {
		t.Fatal(err)
	}

	for i, c := range cases {
		got, err := FindMatchingPaths(root, c.ext, c.name, c.typ)
		if err != nil {
			t.Fatal(err)
		}
		var goPairs [][2]string
		for _, m := range got {
			goPairs = append(goPairs, [2]string{filepath.ToSlash(m.Path), m.Type})
		}
		var pyPairs [][2]string
		for _, p := range py.Find[i] {
			pyPairs = append(pyPairs, [2]string{p[0], p[1]})
		}
		if len(goPairs) == 0 && len(pyPairs) == 0 {
			continue
		}
		if !reflect.DeepEqual(goPairs, pyPairs) {
			t.Errorf("find %+v:\n go: %v\n py: %v", c, goPairs, pyPairs)
		}
	}

	goJSON := filepath.Join(t.TempDir(), "out.json")
	data, err := parseJSONForWrite(jsonText)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(goJSON, data); err != nil {
		t.Fatal(err)
	}
	goBytes, _ := os.ReadFile(goJSON)
	if string(goBytes) != py.JSON {
		t.Errorf("write_file json differs:\n go: %q\n py: %q", goBytes, py.JSON)
	}

	goPlistPath := filepath.Join(t.TempDir(), "out.plist")
	pdata, err := plist.Unmarshal([]byte(plistSource))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(goPlistPath, pdata); err != nil {
		t.Fatal(err)
	}
	goPlist, _ := os.ReadFile(goPlistPath)
	if base64.StdEncoding.EncodeToString(goPlist) != py.Plist {
		t.Errorf("write_file plist differs:\n go: %s\n py: %s", goPlist, py.Plist)
	}
}

// parseJSONForWrite decodes the JSON text with the same loader used by ReadFile.
func parseJSONForWrite(text string) (any, error) {
	return pyjsonLoad([]byte(text))
}
