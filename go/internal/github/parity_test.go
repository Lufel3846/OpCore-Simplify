package github

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// assetsPage is a synthetic expanded-assets fragment with the markup shape the
// parser reads: one <li> block per asset, a download link, a sha256 line and a
// relative-time element.
const assetsPage = `<ul>
<li class="Box-row">
<a href="/o/r/releases/download/v1/Lilu-1.6.2.zip">Lilu</a>
<span>sha256: 1a2b3c4d<br></span>
<relative-time datetime="2024-05-01T10:11:12Z"></relative-time>
</li>
<li class="Box-row">
<a href="/o/r/releases/download/v1/AirportItlwm_Sonoma14.4_RESEARCH.zip">x</a>
<relative-time datetime="2023-01-09T08:07:06Z"></relative-time>
</li>
<li class="Box-row">
<a href="/o/r/releases/download/v1/AirportItlwm_Sonoma14.4.zip">itlwm</a>
<span>sha256: ffee00<br></span>
<relative-time datetime="2025-10-02T03:04:05Z"></relative-time>
</li>
<li class="Box-row">
<a href="/o/r/releases/download/v1/Hardware-Sniffer.zip">no time</a>
</li>
</ul>`

const releasesPage = `<html>
<a href="/o/r/releases/tag/v3.1.4">v3.1.4</a>
<div class="markdown body-content">
Release notes text
</div>
</html>`

func TestParityWithPythonGithubParsers(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	type req struct {
		Op   string `json:"op"`
		Text string `json:"text"`
	}
	var reqs []req
	add := func(op, text string) { reqs = append(reqs, req{op, text}) }

	add("payload", `<script type="application/json">{"payload": {"k": [1, 2]}}</script>`)
	add("payload", "<p>\n<script type=\"application/json\">{\"other\": 1}</script>\n<script type=\"application/json\">{\"payload\": \"second\"}</script>")
	add("payload", `<script type="application/json">{"payload": 1}`+"\n"+`type="application/json" but no bracket`)
	add("payload", `<script type="application/json">[1,2]</script>`)
	add("payload", `<script type="application/json">not json</script>`)
	add("payload", "no island here")
	add("payload", `type="application/json"`)

	add("tag", releasesPage)
	add("tag", "<a href=\"/x\">no release link</a>")

	add("body", releasesPage)
	add("body", "<div class=\"body-content\">first line only")
	add("body", "plain text, no marker")

	add("assets", assetsPage)
	add("assets", "")
	add("assets", "<li>\n<a href=\"/o/r/releases/download/v/RealtekRTL8111-rtsx.zip\">r</a>\n<relative-time datetime=\"2022-12-31T23:59:58Z\"></relative-time>\n</li>")
	add("assets", "<li>\n<a href=\"/o/r/releases/download/v/AppleALC-DEBUG.zip\">d</a>\n<relative-time datetime=\"2022-12-31T23:59:58Z\"></relative-time>\n</li>")

	for _, name := range []string{
		"VoodooPS2Controller-2.2.1.zip", "Hardware-Sniffer.exe", "itlwm_v2.0_Tahoe.zip",
		"itlwm_Sonoma14.0.zip", "IntelBluetooth-1.zip", "RealtekRTL8111-rtsx.zip",
		"AppleALC-1.9.0-unsupported.zip", "1.zip", ".hidden", "plain", "a_b-c.d.zip",
		"Lilu-1.6.2_x.zip", "Hardware-Sniffer-1.2.exe", "name.1.2.zip", "",
	} {
		add("name", name)
	}

	py := runGithubOracle(t, python, repo, reqs)
	for i, r := range reqs {
		pyRes := py[i]
		switch r.Op {
		case "name":
			compareText(t, r.Op, r.Text, ExtractAssetName(r.Text), pyRes)
		case "tag":
			got := ExtractTagName(r.Text)
			compareText(t, r.Op, r.Text, got, pyRes)
		case "body":
			got, err := ExtractBody(r.Text)
			if err != nil {
				if pyRes.Error == "" {
					t.Errorf("body: go error %v; python %v", err, pyRes.OK)
				}
				continue
			}
			compareText(t, r.Op, r.Text, got, pyRes)
		case "payload":
			got, err := ExtractPayload(r.Text)
			comparePayload(t, r.Text, got, err, pyRes)
		case "assets":
			got, err := ExtractAssets(r.Text)
			if err != nil {
				t.Errorf("assets: go error %v", err)
				continue
			}
			comparePayload(t, r.Text, dictsToList(got), nil, pyRes)
		}
	}
}

// compareText compares a string result with the Python json.dumps of the same
// value. Python's None is represented as an empty string on the Go side.
func compareText(t *testing.T, op, input, got string, py oracle) {
	t.Helper()
	if py.Error != "" {
		t.Errorf("%s %q: python raised %s", op, input, py.Error)
		return
	}
	if py.OK == nil {
		if got != "" {
			t.Errorf("%s %q: go %q; python None", op, input, got)
		}
		return
	}
	var want string
	if err := json.Unmarshal([]byte(*py.OK), &want); err != nil {
		t.Fatalf("%s %q: python result is not a string: %s", op, input, *py.OK)
	}
	if got != want {
		t.Errorf("%s %q: go %q; python %q", op, input, got, want)
	}
}

func comparePayload(t *testing.T, input string, got any, err error, py oracle) {
	t.Helper()
	if py.Error != "" {
		if err == nil {
			t.Errorf("%q: python raised %s; go returned %v", input, py.Error, got)
		}
		return
	}
	if err != nil {
		t.Errorf("%q: go error %v; python %v", input, err, py.OK)
		return
	}
	if py.OK == nil {
		if got != nil {
			t.Errorf("%q: go %v; python None", input, got)
		}
		return
	}
	text, derr := pyjson.Dump(got)
	if derr != nil {
		t.Fatalf("%q: dump %v", input, derr)
	}
	if text != *py.OK {
		t.Errorf("%q:\n go: %s\n py: %s", input, text, *py.OK)
	}
}

func dictsToList(dicts []*plist.Dict) any {
	out := []any{}
	for _, d := range dicts {
		out = append(out, d)
	}
	return out
}

type oracle struct {
	OK    *string `json:"ok"`
	Error string  `json:"error"`
}

func runGithubOracle(t *testing.T, python, repo string, reqs any) []oracle {
	t.Helper()
	input, err := json.Marshal(reqs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_github.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v", err)
	}
	var results []oracle
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatal(err)
	}
	return results
}
