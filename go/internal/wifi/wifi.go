// Package wifi ports Scripts/wifi_profile_extractor.py: it reads the Wi-Fi
// networks saved on the machine and their passwords, for the itlwm kext's
// WiFiConfig.
//
// The platform commands (netsh, nmcli, networksetup and security) are run through
// Run, so they can be replaced in tests and on machines where they are not
// available. The parsing, password checks, network count and the retry flow are
// the original's logic. Terminal output is not reproduced; the prompts are
// answered through Input.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2601). See go/LICENSE.
package wifi

import (
	"bufio"
	"errors"
	"fmt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/term"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// PressEnter is the default prompt of utils.request_input().
const PressEnter = "Press Enter to continue..."

// maxConsecutiveFailures mirrors max_consecutive_failures in process_networks.
const maxConsecutiveFailures = 3

// Profile is one network with its password, as (ssid, password) in the original.
type Profile struct {
	SSID     string
	Password string
}

// Extractor holds the command runner, the operating system name and the prompt input.
type Extractor struct {
	// Run executes a command and returns its stdout and return code. A missing
	// command returns ("", 1).
	Run func(args []string) (stdout string, returnCode int)
	// OS mirrors platform.system(): "Windows", "Linux" or "Darwin".
	OS string
	// Input answers a prompt. Nil reads a line from stdin.
	Input func(prompt string) (string, error)
	// Log receives the screen text (title boxes and messages). Nil discards it.
	Log io.Writer
}

func (e *Extractor) head(text string) { term.Head(e.Log, text, 68, true) }

func (e *Extractor) printf(format string, a ...any) {
	if e.Log != nil {
		fmt.Fprintf(e.Log, format, a...)
	}
}

// ask answers a prompt through Input, or from stdin when no input is set.
func (e *Extractor) ask(prompt string) (string, error) {
	if e.Input != nil {
		return e.Input(prompt)
	}
	fmt.Fprint(os.Stdout, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("EOFError: EOF when reading a line")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// askLower mirrors input(...).strip().lower().
func (e *Extractor) askLower(prompt string) (string, error) {
	answer, err := e.ask(prompt)
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(answer)), nil
}

// orDefault mirrors "value or default" for a string.
func orDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

// splitlines mirrors str.splitlines for \n, \r\n and \r.
func splitlines(s string) []string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// colonPart mirrors line.split(":")[1]. A line without a colon is an IndexError.
func colonPart(line string) (string, error) {
	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return "", errors.New("IndexError: list index out of range")
	}
	return parts[1], nil
}

// AuthenticationType mirrors get_authentication_type. ok is false where the
// original returns None.
func AuthenticationType(value string) (string, bool) {
	v := strings.ToLower(value)
	for _, open := range []string{"none", "owe", "open"} {
		if strings.Contains(v, open) {
			return "open", true
		}
	}
	if strings.Contains(v, "wep") || strings.Contains(v, "shared") {
		return "wep", true
	}
	if strings.Contains(v, "wpa") || strings.Contains(v, "sae") {
		return "wpa", true
	}
	return "", false
}

// ValidatePassword mirrors validate_wifi_password. password is a string or nil;
// the result is the password to keep (a string) or nil. authType is nil or one of
// "open", "wep", "wpa". A non-string password with a type set is an error, as in
// Python (encode is missing on it).
func ValidatePassword(authType *string, password any) (any, error) {
	if password == nil {
		return nil, nil
	}
	if authType == nil {
		return password, nil
	}
	if *authType == "open" {
		return "", nil
	}
	text, ok := password.(string)
	if !ok {
		return nil, fmt.Errorf("AttributeError: %T has no attribute 'encode'", password)
	}
	if !utf8.ValidString(text) {
		return nil, nil
	}
	for _, r := range text {
		if r > 126 {
			return nil, nil
		}
	}
	if n := len(text); n >= 8 && n <= 63 {
		for i := 0; i < n; i++ {
			if text[i] < 32 || text[i] > 126 {
				return nil, nil
			}
		}
		return text, nil
	}
	return nil, nil
}

// validate prints the original's "Validating password" line, then runs ValidatePassword.
func (e *Extractor) validate(authType *string, password any) (any, error) {
	shown := "None"
	if authType != nil {
		shown = *authType
	}
	e.printf("Validating password with authentication type: %s\n", shown)
	return ValidatePassword(authType, password)
}

// passwordMacOS mirrors get_wifi_password_macos.
func (e *Extractor) passwordMacOS(ssid string) (any, error) {
	out, rc := e.Run([]string{"security", "find-generic-password", "-wa", ssid})
	if rc != 0 {
		return nil, nil
	}
	stripped := strings.TrimSpace(out)
	var password any
	// try: json.loads(...).get("password") — any failure falls back to the raw text.
	if info, err := pyjson.Load([]byte(stripped)); err == nil {
		if dict, ok := info.(*plist.Dict); ok {
			password, _ = dict.Get("password")
		} else if stripped != "" {
			password = stripped
		}
	} else if stripped != "" {
		password = stripped
	}
	wpa := "wpa"
	return e.validate(&wpa, password)
}

// passwordWindows mirrors get_wifi_password_windows: the authentication type is
// taken from the first "Authentication" line only while it is still unknown.
func (e *Extractor) passwordWindows(ssid string) (any, error) {
	out, rc := e.Run([]string{"netsh", "wlan", "show", "profile", ssid, "key=clear"})
	if rc != 0 {
		return nil, nil
	}
	var authType *string
	var password any
	for _, line := range splitlines(out) {
		if authType == nil && strings.Contains(line, "Authentication") {
			part, err := colonPart(line)
			if err != nil {
				return nil, err
			}
			authType = authTypePtr(strings.TrimSpace(part))
		} else if strings.Contains(line, "Key Content") {
			part, err := colonPart(line)
			if err != nil {
				return nil, err
			}
			password = strings.TrimSpace(part)
		}
	}
	return e.validate(authType, password)
}

// passwordLinux mirrors get_wifi_password_linux: the authentication type is
// replaced by every key-mgmt line, and the password by every psk line.
func (e *Extractor) passwordLinux(ssid string) (any, error) {
	out, rc := e.Run([]string{"nmcli", "--show-secrets", "connection", "show", ssid})
	if rc != 0 {
		return nil, nil
	}
	var authType *string
	var password any
	for _, line := range splitlines(out) {
		if strings.Contains(line, "802-11-wireless-security.key-mgmt:") {
			part, err := colonPart(line)
			if err != nil {
				return nil, err
			}
			authType = authTypePtr(strings.TrimSpace(part))
		} else if strings.Contains(line, "802-11-wireless-security.psk:") {
			part, err := colonPart(line)
			if err != nil {
				return nil, err
			}
			password = strings.TrimSpace(part)
		}
	}
	return e.validate(authType, password)
}

// authTypePtr returns the classified authentication type, or nil (Python None).
func authTypePtr(value string) *string {
	t, ok := AuthenticationType(value)
	if !ok {
		return nil
	}
	return &t
}

// AskNetworkCount mirrors ask_network_count: how many networks to process.
func (e *Extractor) AskNetworkCount(total int) (int, error) {
	e.head("WiFi Network Retrieval")
	e.printf("\nFound %d WiFi networks on this device.\n\nHow many networks would you like to process?\n  1-%d - Specific number (default: 5)\n  A   - All available networks\n\n", total, total)
	choice, err := e.askLower("Enter your choice: ")
	if err != nil {
		return 0, err
	}
	choice = orDefault(choice, "5")
	if choice == "a" {
		e.printf("Will process all available networks.\n")
		return total, nil
	}
	invalid := func() (int, error) {
		m := min(5, total)
		e.printf("Invalid choice. Will process up to %d networks.\n", m)
		return m, nil
	}
	n, err := pyrt.Int(choice)
	if err != nil {
		return invalid()
	}
	value, err := pyrt.IntValue(n)
	if err != nil {
		return invalid()
	}
	if value > int64(total) {
		value = int64(total)
	}
	e.printf("Will process up to %d networks.\n", value)
	return int(value), nil
}

// ProcessNetworks mirrors process_networks. It asks before giving up after
// repeated failures (macOS only, where each failure is a password prompt) and
// before stopping short of the target.
func (e *Extractor) ProcessNetworks(ssids []string, maxNetworks int, getPassword func(string) (any, error)) ([]Profile, error) {
	var networks []Profile
	processed := 0
	consecutive := 0
	darwin := e.OS == "Darwin"
	countFailure := func() {
		if darwin {
			consecutive++
		}
	}
	confirmGiveUp := func() (bool, error) {
		answer, err := e.askLower("\nUnable to retrieve passwords. Continue trying? (Yes/no): ")
		if err != nil {
			return false, err
		}
		if orDefault(answer, "yes") != "yes" {
			return true, nil
		}
		consecutive = 0
		return false, nil
	}

	for len(networks) < maxNetworks && processed < len(ssids) {
		ssid := ssids[processed]
		stop := false

		e.printf("\nProcessing %d/%d: %s\n", processed+1, len(ssids), ssid)
		if darwin {
			e.printf("Please enter your administrator name and password or click 'Deny' to skip this network.\n")
		}
		password, err := getPassword(ssid)
		if err == nil {
			if password != nil {
				text, isText := password.(string)
				if !isText {
					return nil, fmt.Errorf("TypeError: password %v is not a string", password)
				}
				duplicate := false
				for _, n := range networks {
					if n.SSID == ssid && n.Password == text {
						duplicate = true
					}
				}
				if !duplicate {
					consecutive = 0
					networks = append(networks, Profile{SSID: ssid, Password: text})
					e.printf("Successfully retrieved password.\n")
					if len(networks) == maxNetworks {
						stop = true
					}
				}
			} else {
				countFailure()
				e.printf("Could not retrieve password for this network.\n")
				if consecutive >= maxConsecutiveFailures {
					giveUp, err := confirmGiveUp()
					if err != nil {
						return nil, err
					}
					stop = giveUp
				}
			}
		} else {
			countFailure()
			e.printf("Error processing network '%s': %s\n", ssid, err.Error())
			if consecutive >= maxConsecutiveFailures {
				giveUp, err := confirmGiveUp()
				if err != nil {
					return nil, err
				}
				stop = giveUp
			}
		}
		processed++
		if stop {
			break
		}

		if processed >= maxNetworks && len(networks) < maxNetworks && processed < len(ssids) {
			answer, err := e.askLower(fmt.Sprintf("\nOnly retrieved %d/%d networks. Try more to reach your target? (Yes/no): ", len(networks), maxNetworks))
			if err != nil {
				return nil, err
			}
			if orDefault(answer, "yes") != "yes" {
				break
			}
			consecutive = 0
		}
	}
	return networks, nil
}

// preferredMacOS mirrors get_preferred_networks_macos.
func (e *Extractor) preferredMacOS(iface string) ([]Profile, error) {
	out, rc := e.Run([]string{"networksetup", "-listpreferredwirelessnetworks", iface})
	if rc != 0 || !strings.Contains(out, "Preferred networks on") {
		return nil, nil
	}
	var ssids []string
	for i, line := range splitlines(out) {
		if i == 0 {
			continue
		}
		if t := strings.TrimSpace(line); t != "" {
			ssids = append(ssids, t)
		}
	}
	if len(ssids) == 0 {
		return nil, nil
	}
	count, err := e.AskNetworkCount(len(ssids))
	if err != nil {
		return nil, err
	}
	e.head("Administrator Authentication Required")
	e.printf("\nTo retrieve WiFi passwords from the Keychain, macOS will prompt\nyou for administrator credentials for each WiFi network.\n")
	return e.ProcessNetworks(ssids, count, e.passwordMacOS)
}

// preferredWindows mirrors get_preferred_networks_windows.
func (e *Extractor) preferredWindows() ([]Profile, error) {
	out, rc := e.Run([]string{"netsh", "wlan", "show", "profiles"})
	if rc != 0 {
		return nil, nil
	}
	var ssids []string
	for _, line := range splitlines(out) {
		if !strings.Contains(line, "All User Profile") {
			continue
		}
		part, err := colonPart(line)
		if err != nil {
			continue
		}
		if ssid := strings.TrimSpace(part); ssid != "" {
			ssids = append(ssids, ssid)
		}
	}
	if len(ssids) == 0 {
		return nil, nil
	}
	e.head("WiFi Profile Extractor")
	e.printf("\nRetrieving passwords for %d network(s)...\n", len(ssids))
	return e.ProcessNetworks(ssids, len(ssids), e.passwordWindows)
}

// preferredLinux mirrors get_preferred_networks_linux.
func (e *Extractor) preferredLinux() ([]Profile, error) {
	out, rc := e.Run([]string{"nmcli", "-t", "-f", "NAME", "connection", "show"})
	if rc != 0 {
		return nil, nil
	}
	var ssids []string
	for _, line := range splitlines(out) {
		if t := strings.TrimSpace(line); t != "" {
			ssids = append(ssids, t)
		}
	}
	if len(ssids) == 0 {
		return nil, nil
	}
	e.head("WiFi Profile Extractor")
	e.printf("\nRetrieving passwords for %d network(s)...\n", len(ssids))
	return e.ProcessNetworks(ssids, len(ssids), e.passwordLinux)
}

// interfaces mirrors get_wifi_interfaces: the en* devices that have preferred networks.
func (e *Extractor) interfaces() []string {
	out, rc := e.Run([]string{"networksetup", "-listallhardwareports"})
	if rc != 0 {
		return nil
	}
	var found []string
	for _, info := range strings.Split(out, "\n\n") {
		if !strings.Contains(info, "Device: en") {
			continue
		}
		rest := strings.Split(info, "Device: en")[1]
		number := strings.Split(rest, "\n")[0]
		n, err := pyrt.Int(number)
		if err != nil {
			continue
		}
		value, err := pyrt.IntValue(n)
		if err != nil {
			continue
		}
		iface := fmt.Sprintf("en%d", value)
		test, testRC := e.Run([]string{"networksetup", "-listpreferredwirelessnetworks", iface})
		if testRC == 0 && strings.Contains(test, "Preferred networks on") {
			found = append(found, iface)
		}
	}
	return found
}

// GetProfiles mirrors get_profiles. It returns the networks with their passwords,
// or none when the user declines the scan or nothing is found.
func (e *Extractor) GetProfiles() ([]Profile, error) {
	e.head("WiFi Profile Extractor")
	e.printf("\n\033[1;93mNote:\033[0m\n- When using itlwm kext, WiFi appears as Ethernet in macOS\n- You'll need Heliport app to manage WiFi connections in macOS\n- This step will enable auto WiFi connections at boot time\n  and is useful for users installing macOS via Recovery OS\n\n")
	for {
		answer, err := e.askLower("Would you like to scan for WiFi profiles? (Yes/no): ")
		if err != nil {
			return nil, err
		}
		if answer == "yes" {
			break
		}
		if answer == "no" {
			return nil, nil
		}
		e.printf("\033[91mInvalid selection, please try again.\033[0m\n\n\n")
	}

	var profiles []Profile
	var err error
	e.head("Detecting WiFi Profiles")
	e.printf("\nScanning for WiFi profiles...\n")
	switch e.OS {
	case "Windows":
		profiles, err = e.preferredWindows()
	case "Linux":
		profiles, err = e.preferredLinux()
	case "Darwin":
		ifaces := e.interfaces()
		if len(ifaces) == 0 {
			e.printf("No WiFi interfaces detected.\n")
		}
		for _, iface := range ifaces {
			e.printf("Checking interface: %s\n", iface)
			found, err := e.preferredMacOS(iface)
			if err != nil {
				return nil, err
			}
			if len(found) > 0 {
				profiles = found
				break
			}
		}
	}
	if err != nil {
		return nil, err
	}

	if len(profiles) == 0 {
		e.head("WiFi Profile Extractor")
		e.printf("\nNo WiFi profiles with saved passwords were found.\n")
		if _, err := e.ask(PressEnter); err != nil {
			return nil, err
		}
	}
	e.head("WiFi Profile Extractor")
	e.printf("\nFound the following WiFi profiles with saved passwords:\n\nIndex  SSID                             Password\n-------------------------------------------------------\n")
	for i, pr := range profiles {
		ssid, pw := []rune(pr.SSID), []rune(pr.Password)
		ss, ps := string(ssid), string(pw)
		if len(ssid) > 31 {
			ss = string(ssid[:31]) + "..."
		}
		if len(pw) > 12 {
			ps = string(pw[:12]) + "..."
		}
		e.printf("%-6d %-32s %-8s\n", i+1, ss, ps)
	}
	e.printf("\nSuccessfully applied %d WiFi profiles.\n\n", len(profiles))
	if _, err := e.ask(PressEnter); err != nil {
		return nil, err
	}
	return profiles, nil
}

// Pairs returns the profiles as [ssid, password] pairs, the shape the kext
// WiFiConfig generator takes.
func (e *Extractor) Pairs() ([][2]string, error) {
	profiles, err := e.GetProfiles()
	if err != nil {
		return nil, err
	}
	out := make([][2]string, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, [2]string{p.SSID, p.Password})
	}
	return out, nil
}
