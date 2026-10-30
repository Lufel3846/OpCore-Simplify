package kextmaestro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// wifiBundle writes an itlwm Info.plist whose WiFiConfig is set, so that
// modify_kexts asks for the Wi-Fi profiles.
func wifiBundle(t *testing.T, dir string) string {
	t.Helper()
	info := plist.NewDict()
	info.Set("CFBundleIdentifier", "com.example.itlwm")
	itlwm := plist.NewDict()
	stored := plist.NewDict()
	stored.Set("placeholder", "1")
	itlwm.Set("WiFiConfig", stored)
	personalities := plist.NewDict()
	personalities.Set("itlwm", itlwm)
	info.Set("IOKitPersonalities", personalities)
	data, err := plist.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Info.plist")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestParityModifyKextsWiFi checks the WiFiConfig path of modify_kexts. Both
// sides get the same fixed profile list, so the written Info.plist must match.
func TestParityModifyKextsWiFi(t *testing.T) {
	profiles := [][2]string{{"HomeNet", "correcthorse"}, {"Cafe", ""}}

	pyPath := wifiBundle(t, t.TempDir())
	py := runKextOracle(t, []map[string]any{{
		"op":       "modify_wifi",
		"path":     pyPath,
		"report":   map[string]any{"CPU": map[string]any{"Core Count": 6}},
		"macos":    "25.0.0",
		"profiles": profiles,
	}})
	if py[0].Error != "" {
		t.Fatalf("python raised %s", py[0].Error)
	}

	goPath := wifiBundle(t, t.TempDir())
	report, err := pyjson.Load([]byte(`{"CPU": {"Core Count": 6}}`))
	if err != nil {
		t.Fatal(err)
	}
	m := New(t.TempDir())
	m.WifiProfiles = func() ([][2]string, error) { return profiles, nil }
	if err := m.ModifyKexts(goPath, report, "25.0.0"); err != nil {
		t.Fatal(err)
	}
	goRaw, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatal(err)
	}
	// The oracle wraps its sha256 in json.dumps, so the value arrives quoted.
	var want string
	if err := json.Unmarshal([]byte(*py[0].OK), &want); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(goRaw)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("WiFiConfig plist differs:\n go: %s\n py: %s\n%s", got, want, goRaw)
	}
}

// TestModifyKextsWiFiNeedsProfiles checks that a WiFiConfig without a configured
// profile source fails explicitly instead of writing a file without the profiles.
func TestModifyKextsWiFiNeedsProfiles(t *testing.T) {
	path := wifiBundle(t, t.TempDir())
	report, err := pyjson.Load([]byte(`{"CPU": {"Core Count": 6}}`))
	if err != nil {
		t.Fatal(err)
	}
	m := New(t.TempDir())
	if err := m.ModifyKexts(path, report, "25.0.0"); err != ErrWifiProfilesNotPorted {
		t.Errorf("ModifyKexts without WifiProfiles = %v; want ErrWifiProfilesNotPorted", err)
	}
}
