package smbios

import "testing"

// TestGenerateMapMatchesDictionary checks that the map form used by the config
// generator carries the same values as the ordered dictionary.
func TestGenerateMapMatchesDictionary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir+"/macserial.linux", "")
	s := &Serial{
		ScriptDir:     dir,
		OS:            "Linux",
		HistoryFile:   dir + "/history.json",
		Run:           func([]string) (string, int) { return "SER123 | MLBXYZ", 0 },
		RandomMACPart: func() string { return "ABCDEF012345" },
		NewUUID:       func() string { return "0F1E2D3C-4B5A-4978-8695-A4B3C2D1E0F9" },
	}
	m, err := s.GenerateMap("MacPro7,1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"MLB": "MLBXYZ", "ROM": "ABCDEF012345", "SystemProductName": "MacPro7,1",
		"SystemSerialNumber": "SER123", "SystemUUID": "0F1E2D3C-4B5A-4978-8695-A4B3C2D1E0F9",
	}
	if len(m) != len(want) {
		t.Fatalf("got %d keys, want %d", len(m), len(want))
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
}
