package wifi

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type command struct {
	Args []string `json:"args"`
	Out  string   `json:"out"`
	RC   int      `json:"rc"`
}

// wifiCase is one operation, shared by the Go test and the Python oracle.
type wifiCase struct {
	Name     string    `json:"name"`
	Op       string    `json:"op"`
	OS       string    `json:"os"`
	Args     []any     `json:"args"`
	Commands []command `json:"commands"`
	Inputs   []string  `json:"inputs"`
}

type oracleCase struct {
	OK      *string  `json:"ok"`
	Error   *string  `json:"error"`
	Prompts []string `json:"prompts"`
}

const linuxShow = "802-11-wireless-security.key-mgmt: wpa-psk\n802-11-wireless-security.psk: correcthorse\n"

var wifiCases = []wifiCase{
	// Authentication types.
	{Name: "auth WPA2", Op: "auth", Args: []any{"WPA2-Personal"}},
	{Name: "auth WPA3 SAE", Op: "auth", Args: []any{"WPA3-SAE"}},
	{Name: "auth open", Op: "auth", Args: []any{"Open"}},
	{Name: "auth none", Op: "auth", Args: []any{"None"}},
	{Name: "auth owe", Op: "auth", Args: []any{"OWE transition"}},
	{Name: "auth WEP", Op: "auth", Args: []any{"WEP"}},
	{Name: "auth shared", Op: "auth", Args: []any{"Shared"}},
	{Name: "auth unknown", Op: "auth", Args: []any{"Enterprise"}},
	{Name: "auth empty", Op: "auth", Args: []any{""}},

	// Password validation.
	{Name: "validate none password", Op: "validate", Args: []any{nil, nil}},
	{Name: "validate no type", Op: "validate", Args: []any{nil, "x"}},
	{Name: "validate open", Op: "validate", Args: []any{"open", "anything"}},
	{Name: "validate too short", Op: "validate", Args: []any{"wpa", "short"}},
	{Name: "validate eight", Op: "validate", Args: []any{"wpa", "12345678"}},
	{Name: "validate 63", Op: "validate", Args: []any{"wpa", strings.Repeat("a", 63)}},
	{Name: "validate 64", Op: "validate", Args: []any{"wpa", strings.Repeat("a", 64)}},
	{Name: "validate non-ascii", Op: "validate", Args: []any{"wpa", "pässwörd123"}},
	{Name: "validate control char", Op: "validate", Args: []any{"wep", "tab\tchar12"}},
	{Name: "validate number", Op: "validate", Args: []any{"wpa", int64(12345678)}},

	// Network count.
	{Name: "count all", Op: "count", Args: []any{int64(5)}, Inputs: []string{"A"}},
	{Name: "count default", Op: "count", Args: []any{int64(5)}, Inputs: []string{""}},
	{Name: "count three", Op: "count", Args: []any{int64(5)}, Inputs: []string{"3"}},
	{Name: "count over total", Op: "count", Args: []any{int64(2)}, Inputs: []string{"9"}},
	{Name: "count invalid", Op: "count", Args: []any{int64(8)}, Inputs: []string{"x"}},
	{Name: "count negative", Op: "count", Args: []any{int64(8)}, Inputs: []string{"-2"}},

	// Per-platform password readers.
	{Name: "macos json password", Op: "macpass", Args: []any{"Office"}, Commands: []command{
		{Args: []string{"security", "find-generic-password", "-wa", "Office"}, Out: `{"password": "officepass1"}`, RC: 0}}},
	{Name: "macos plain password", Op: "macpass", Args: []any{"Home"}, Commands: []command{
		{Args: []string{"security", "find-generic-password", "-wa", "Home"}, Out: "homepass12\n", RC: 0}}},
	{Name: "macos missing", Op: "macpass", Args: []any{"Gone"}},
	{Name: "macos empty output", Op: "macpass", Args: []any{"Empty"}, Commands: []command{
		{Args: []string{"security", "find-generic-password", "-wa", "Empty"}, Out: "", RC: 0}}},
	{Name: "windows key content", Op: "winpass", Args: []any{"Office"}, Commands: []command{
		{Args: []string{"netsh", "wlan", "show", "profile", "Office", "key=clear"}, Out: "    Authentication         : WPA2-Personal\n    Key Content            : officepass1\n", RC: 0}}},
	{Name: "windows open no key", Op: "winpass", Args: []any{"Home"}, Commands: []command{
		{Args: []string{"netsh", "wlan", "show", "profile", "Home", "key=clear"}, Out: "    Authentication         : Open\n    Key Content            : \n", RC: 0}}},
	{Name: "windows no colon", Op: "winpass", Args: []any{"Broken"}, Commands: []command{
		{Args: []string{"netsh", "wlan", "show", "profile", "Broken", "key=clear"}, Out: "Authentication without colon\n", RC: 0}}},
	{Name: "linux psk", Op: "linuxpass", Args: []any{"Home"}, Commands: []command{
		{Args: []string{"nmcli", "--show-secrets", "connection", "show", "Home"}, Out: linuxShow, RC: 0}}},
	{Name: "linux open", Op: "linuxpass", Args: []any{"Cafe"}, Commands: []command{
		{Args: []string{"nmcli", "--show-secrets", "connection", "show", "Cafe"}, Out: "802-11-wireless-security.key-mgmt: none\n", RC: 0}}},

	// Full profile scans.
	{Name: "linux scan", Op: "profiles", OS: "Linux", Inputs: []string{"yes", ""}, Commands: []command{
		{Args: []string{"nmcli", "-t", "-f", "NAME", "connection", "show"}, Out: "Home\nOffice\n\nGuest\n", RC: 0},
		{Args: []string{"nmcli", "--show-secrets", "connection", "show", "Home"}, Out: linuxShow, RC: 0},
		{Args: []string{"nmcli", "--show-secrets", "connection", "show", "Office"}, Out: "802-11-wireless-security.key-mgmt: none\n", RC: 0},
		{Args: []string{"nmcli", "--show-secrets", "connection", "show", "Guest"}, Out: "", RC: 1},
	}},
	{Name: "windows scan", Op: "profiles", OS: "Windows", Inputs: []string{"yes", ""}, Commands: []command{
		{Args: []string{"netsh", "wlan", "show", "profiles"}, Out: "Profiles on interface:\n    All User Profile     : Office\n    All User Profile     : Home\n    All User Profile     : NoKey\n    All User Profile     : Broken\n", RC: 0},
		{Args: []string{"netsh", "wlan", "show", "profile", "Office", "key=clear"}, Out: "    Authentication         : WPA2-Personal\n    Key Content            : officepass1\n", RC: 0},
		{Args: []string{"netsh", "wlan", "show", "profile", "Home", "key=clear"}, Out: "    Authentication         : Open\n    Key Content            : \n", RC: 0},
		{Args: []string{"netsh", "wlan", "show", "profile", "NoKey", "key=clear"}, Out: "    Authentication         : WPA2-Personal\n", RC: 0},
		{Args: []string{"netsh", "wlan", "show", "profile", "Broken", "key=clear"}, Out: "Authentication without colon\n", RC: 0},
	}},
	{Name: "macos scan all", Op: "profiles", OS: "Darwin",
		Inputs: []string{"yes", "a", "yes", ""},
		Commands: []command{
			{Args: []string{"networksetup", "-listallhardwareports"}, Out: "Hardware Port: Wi-Fi\nDevice: en0\nEthernet Address: aa\n\nHardware Port: Thunderbolt\nDevice: en5\n", RC: 0},
			{Args: []string{"networksetup", "-listpreferredwirelessnetworks", "en0"}, Out: "Preferred networks on en0:\n\tOffice\n\tGuest\n\tNoPass\n\tGone\n\tHome\n", RC: 0},
			{Args: []string{"networksetup", "-listpreferredwirelessnetworks", "en5"}, Out: "", RC: 1},
			{Args: []string{"security", "find-generic-password", "-wa", "Office"}, Out: `{"password": "officepass1"}`, RC: 0},
			{Args: []string{"security", "find-generic-password", "-wa", "Guest"}, Out: "", RC: 1},
			{Args: []string{"security", "find-generic-password", "-wa", "NoPass"}, Out: "", RC: 0},
			{Args: []string{"security", "find-generic-password", "-wa", "Home"}, Out: "homepass12", RC: 0},
		}},
	{Name: "macos stops at target", Op: "profiles", OS: "Darwin",
		Inputs: []string{"yes", "2"},
		Commands: []command{
			{Args: []string{"networksetup", "-listallhardwareports"}, Out: "Device: en0\n", RC: 0},
			{Args: []string{"networksetup", "-listpreferredwirelessnetworks", "en0"}, Out: "Preferred networks on en0:\n\tA1\n\tB2\n\tC3\n", RC: 0},
			{Args: []string{"security", "find-generic-password", "-wa", "A1"}, Out: "aaaaaaaa", RC: 0},
			{Args: []string{"security", "find-generic-password", "-wa", "B2"}, Out: "bbbbbbbb", RC: 0},
		}},
	{Name: "macos only retrieved prompt", Op: "profiles", OS: "Darwin",
		Inputs: []string{"yes", "3", "no", ""},
		Commands: []command{
			{Args: []string{"networksetup", "-listallhardwareports"}, Out: "Device: en0\n", RC: 0},
			{Args: []string{"networksetup", "-listpreferredwirelessnetworks", "en0"}, Out: "Preferred networks on en0:\n\tOffice\n\tGuest\n\tNoPass\n\tHome\n", RC: 0},
			{Args: []string{"security", "find-generic-password", "-wa", "Office"}, Out: `{"password": "officepass1"}`, RC: 0},
			{Args: []string{"security", "find-generic-password", "-wa", "Guest"}, Out: "", RC: 1},
			{Args: []string{"security", "find-generic-password", "-wa", "NoPass"}, Out: "", RC: 0},
			{Args: []string{"security", "find-generic-password", "-wa", "Home"}, Out: "homepass12", RC: 0},
		}},
	{Name: "macos gives up after failures", Op: "profiles", OS: "Darwin",
		Inputs: []string{"yes", "a", "no", ""},
		Commands: []command{
			{Args: []string{"networksetup", "-listallhardwareports"}, Out: "Device: en0\n", RC: 0},
			{Args: []string{"networksetup", "-listpreferredwirelessnetworks", "en0"}, Out: "Preferred networks on en0:\n\tG1\n\tG2\n\tG3\n\tG4\n", RC: 0},
		}},
	{Name: "scan declined", Op: "profiles", OS: "Linux", Inputs: []string{"no"}},
	{Name: "invalid answers then yes, nothing found", Op: "profiles", OS: "Linux",
		Inputs: []string{"maybe", "", "yes", "", ""}},
}

// TestParityWithPythonWifi runs every case through the original and the port: the
// returned values, the errors and the sequence of prompts must match.
func TestParityWithPythonWifi(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; parity test skipped")
	}
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(wifiCases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join("testdata", "pyref_wifi.py"), repo)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
	}
	var py []oracleCase
	if err := json.Unmarshal(out, &py); err != nil {
		t.Fatal(err)
	}

	for i, c := range wifiCases {
		t.Run(c.Name, func(t *testing.T) {
			var prompts []string
			e := &Extractor{
				OS: c.OS,
				Run: func(args []string) (string, int) {
					for _, cmd := range c.Commands {
						if reflect.DeepEqual(cmd.Args, args) {
							return cmd.Out, cmd.RC
						}
					}
					return "", 1
				},
				Input: func(prompt string) (string, error) {
					prompts = append(prompts, prompt)
					if len(c.Inputs) == 0 {
						return "no", nil
					}
					answer := c.Inputs[0]
					c.Inputs = c.Inputs[1:]
					return answer, nil
				},
			}
			got, goErr := runCase(e, c)

			want := py[i]
			if want.Error != nil {
				if goErr == nil {
					t.Errorf("python raised %s; go returned %v", *want.Error, got)
				}
			} else {
				if goErr != nil {
					t.Errorf("go error %v; python returned %s", goErr, *want.OK)
				} else {
					var expected any
					if err := json.Unmarshal([]byte(*want.OK), &expected); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(normalize(got), expected) {
						t.Errorf("value differs:\n go: %v\n py: %s", normalize(got), *want.OK)
					}
				}
			}
			if !reflect.DeepEqual(prompts, want.Prompts) && !(len(prompts) == 0 && len(want.Prompts) == 0) {
				t.Errorf("prompts differ:\n go: %q\n py: %q", prompts, want.Prompts)
			}
		})
	}
}

// runCase executes one operation. The result uses Go types that normalize to the
// JSON values the original prints.
func runCase(e *Extractor, c wifiCase) (any, error) {
	switch c.Op {
	case "auth":
		value, ok := AuthenticationType(c.Args[0].(string))
		if !ok {
			return nil, nil
		}
		return value, nil
	case "validate":
		var auth *string
		if s, ok := c.Args[0].(string); ok {
			auth = &s
		}
		return ValidatePassword(auth, c.Args[1])
	case "count":
		n, err := e.AskNetworkCount(int(c.Args[0].(int64)))
		return float64(n), err
	case "macpass":
		return e.passwordMacOS(c.Args[0].(string))
	case "winpass":
		return e.passwordWindows(c.Args[0].(string))
	case "linuxpass":
		return e.passwordLinux(c.Args[0].(string))
	default:
		profiles, err := e.GetProfiles()
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, p := range profiles {
			out = append(out, []any{p.SSID, p.Password})
		}
		return out, nil
	}
}

// normalize converts Go values to the shapes encoding/json produces.
func normalize(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, normalize(item))
		}
		return out
	}
	return v
}
