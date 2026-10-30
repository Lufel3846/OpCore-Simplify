package smbios

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	mrand "math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// maxMacserialAttempts mirrors the max_retries of SMBIOS.check_macserial.
const maxMacserialAttempts = 3

// Serial mirrors the macserial part of SMBIOS (check_macserial and
// generate_smbios). The executable is found in ScriptDir and run with
// "-g --model <model>". When it is missing, the bootloader gather step is run
// again to install it, as the original does.
type Serial struct {
	// ScriptDir is the Scripts folder where macserial is installed.
	ScriptDir string
	// OS mirrors platform.system(): "Windows", "Linux" or "Darwin".
	OS string
	// HistoryFile is the download history that the gather step maintains.
	HistoryFile string
	// Run executes a command and returns its stdout and return code. A missing
	// command returns ("", 1), as Run.run does.
	Run func(args []string) (stdout string, returnCode int)
	// Gather mirrors gatheringFiles.gather_bootloader_kexts([], "").
	Gather func() error
	// RandomMACPart mirrors generate_random_mac. Nil uses math/rand.
	RandomMACPart func() string
	// NewUUID mirrors str(uuid.uuid4()).upper(). Nil uses crypto/rand.
	NewUUID func() string
}

// NewSerial returns a Serial with the default executable runner and random sources.
func NewSerial(scriptDir, osName, historyFile string, gather func() error) *Serial {
	return &Serial{
		ScriptDir:   scriptDir,
		OS:          osName,
		HistoryFile: historyFile,
		Run:         runCommand,
		Gather:      gather,
	}
}

// runCommand runs args[0] with the remaining arguments, without a shell.
func runCommand(args []string) (string, int) {
	if len(args) == 0 {
		return "", 1
	}
	out, err := execCommand(args[0], args[1:]...)
	if err != nil {
		var code int = 1
		if exit, ok := err.(interface{ ExitCode() int }); ok {
			code = exit.ExitCode()
		}
		return out, code
	}
	return out, 0
}

// CheckMacserial mirrors SMBIOS.check_macserial.
func (s *Serial) CheckMacserial(retry int) (string, error) {
	var binaries []string
	switch s.OS {
	case "Windows":
		binaries = []string{"macserial.exe"}
	case "Linux":
		binaries = []string{"macserial.linux", "macserial"}
	case "Darwin":
		binaries = []string{"macserial"}
	default:
		return "", errors.New("Unknown OS for macserial")
	}
	for _, binary := range binaries {
		path := filepath.Join(s.ScriptDir, binary)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	if retry >= maxMacserialAttempts {
		return "", fmt.Errorf("Failed to find macserial after %d attempts", maxMacserialAttempts)
	}

	if err := s.dropOpenCorePkgHistory(); err != nil {
		return "", err
	}
	if s.Gather == nil {
		return "", errors.New("macserial is missing and no gather step is configured")
	}
	if err := s.Gather(); err != nil {
		return "", err
	}
	return s.CheckMacserial(retry + 1)
}

// dropOpenCorePkgHistory removes the OpenCorePkg entry from the download
// history, so the next gather step downloads it again.
func (s *Serial) dropOpenCorePkgHistory() error {
	current, err := fsutil.ReadFile(s.HistoryFile)
	if err != nil {
		return err
	}
	list, ok := current.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	var kept []any
	for _, item := range list {
		d, ok := item.(*plist.Dict)
		if !ok {
			return errors.New("AttributeError: history item has no attribute 'get'")
		}
		name, _ := d.Get("product_name")
		if name != "OpenCorePkg" {
			kept = append(kept, item)
		}
	}
	if len(kept) != len(list) {
		return fsutil.WriteFile(s.HistoryFile, kept)
	}
	return nil
}

// RandomMAC mirrors generate_random_mac: six random bytes in uppercase hex.
func (s *Serial) RandomMAC() string {
	if s.RandomMACPart != nil {
		return s.RandomMACPart()
	}
	var b strings.Builder
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&b, "%02X", mrand.Intn(256))
	}
	return b.String()
}

// uuidV4 returns a random UUID version 4, in uppercase.
func uuidV4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// GenerateSMBIOS mirrors SMBIOS.generate_smbios. The keys are in the same order
// as the original dictionary.
func (s *Serial) GenerateSMBIOS(model string) (*plist.Dict, error) {
	macserial, err := s.CheckMacserial(0)
	if err != nil {
		return nil, err
	}
	randomMAC := s.RandomMAC()

	output, rc := s.Run([]string{macserial, "-g", "--model", model})

	var serial []string
	if rc == 0 && output != "" && strings.Contains(output, " | ") {
		first := strings.Split(normalizeNewlines(output), "\n")[0]
		serial = strings.Split(first, " | ")
	}

	mlb := "A" + strings.Repeat("0", 15) + "Z"
	serialNumber := "A" + strings.Repeat("0", 10) + "9"
	if len(serial) > 0 {
		mlb = serial[len(serial)-1]
		serialNumber = serial[0]
	}

	newUUID := s.NewUUID
	if newUUID == nil {
		newUUID = uuidV4
	}
	out := plist.NewDict()
	out.Set("MLB", mlb)
	out.Set("ROM", randomMAC)
	out.Set("SystemProductName", model)
	out.Set("SystemSerialNumber", serialNumber)
	out.Set("SystemUUID", newUUID())
	return out, nil
}

// normalizeNewlines converts \r\n and \r to \n, as str.splitlines does for them.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// execCommand runs a program without a shell and returns its stdout.
func execCommand(name string, args ...string) (string, error) {
	var stdout bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.String(), err
}

// GenerateMap returns GenerateSMBIOS as a map, the shape the config generator takes.
func (s *Serial) GenerateMap(model string) (map[string]any, error) {
	dict, err := s.GenerateSMBIOS(model)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, k := range dict.Keys() {
		v, _ := dict.Get(k)
		out[k] = v
	}
	return out, nil
}
