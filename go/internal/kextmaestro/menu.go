package kextmaestro

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
)

// ErrExit stands for sys.exit(0) in utils.exit_program.
var ErrExit = errors.New("exit program")

func (m *Maestro) logf(format string, a ...any) {
	if m.Log != nil {
		fmt.Fprintf(m.Log, format, a...)
	}
}

func padRight(s string, width int) string {
	if n := width - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func padLeft(s string, width int) string {
	if n := width - len([]rune(s)); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

func pyCenter(s string, width int) string {
	pad := width - len([]rune(s))
	if pad <= 0 {
		return s
	}
	left := pad/2 + (pad & width & 1)
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
}

// head mirrors utils.head (without clearing the screen).
func (m *Maestro) head(text string, width int) {
	separator := strings.Repeat("═", width-2)
	title := " " + text + " "
	if r := []rune(title); len(r) > width-2 {
		title = string(r[:width-4]) + "..."
	}
	title = pyCenter(title, width-2)
	m.logf("╔%s╗\n║%s║\n╚%s╝\n", separator, title, separator)
}

// adjustWindowSize mirrors utils.adjust_window_size.
func (m *Maestro) adjustWindowSize(content string) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if content == "" {
		lines = nil
	} else if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	cols := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > cols {
			cols = n
		}
	}
	rows := len(lines) + 6
	if rows < 30 {
		rows = 30
	}
	cols += 2
	if cols < 100 {
		cols = 100
	}
	m.logf("\033[8;%d;%dt\n", rows, cols)
}

// ExitProgram mirrors utils.exit_program. It returns ErrExit unless reading the
// final Enter fails.
func (m *Maestro) ExitProgram() error {
	const width = 68
	m.head("OpCore Simplify", width)
	m.logf("\n%s\n\n", pyCenter("For more information, to report errors, or to contribute to the product:", width))
	sep := strings.Repeat("─", width-4)
	m.logf(" ┌%s┐ \n", sep)
	for _, c := range [][2]string{
		{"Facebook", "https://www.facebook.com/macforce2601"},
		{"Telegram", "https://t.me/lzhoang2601"},
		{"GitHub", "https://github.com/lzhoang2801/OpCore-Simplify"},
	} {
		line := " * " + c[0] + ": " + c[1]
		m.logf(" │%s│ \n", padRight(line, width-4))
	}
	m.logf(" └%s┘ \n\n%s\n\n", sep, pyCenter("Thank you for using our program!", width))
	if _, err := m.input(pyCenter("Press Enter to exit.", width)); err != nil {
		return err
	}
	return ErrExit
}

// KextConfigurationMenu mirrors kext_configuration_menu. It returns nil on "B"
// and ErrExit on "Q".
func (m *Maestro) KextConfigurationMenu(macosVersion string) error {
	currentCategory := ""
	first := true // current_category starts as None, which differs from any string
	var messages []string

	for {
		contents := []string{"", "List of available kexts:"}
		for i, kext := range m.Kexts {
			index := i + 1
			if first || kext.Category != currentCategory {
				first = false
				currentCategory = kext.Category
				name := currentCategory
				if name == "" {
					name = "Uncategorized"
				}
				header := "Category: " + name
				contents = append(contents, "\n"+header+"\n"+strings.Repeat("=", len([]rune(header))))
			}
			status, err := m.GetKextStatus(kext, macosVersion)
			if err != nil {
				return err
			}
			marker := map[string]string{
				"required":        "[R]",
				"selected":        "[*]",
				"force_loaded":    "[!]",
				"available":       "[ ]",
				"force_available": "[?]",
				"not_needed":      "[-]",
				"requires_newer":  "[-]",
			}[status]
			line := marker + " " + padLeft(strconv.Itoa(index), 2) + ". " + padRight(kext.Name, 35) + " - " + padRight(kext.Description, 60)
			switch status {
			case "required", "selected":
				line = "\033[1;32m" + line + "\033[0m"
			case "force_loaded":
				line = "\033[1;93m" + line + "\033[0m"
			case "not_needed", "requires_newer":
				line = "\033[90m" + line + "\033[0m"
			}
			contents = append(contents, line)
		}
		contents = append(contents,
			"",
			"\033[1;93mStatus:\033[0m",
			"- [R] Required   [*] Selected   [!] Force loaded",
			"- [ ] Available  [?] Force available   [-] Unavailable",
			"- When a plugin of a kext is selected, the entire kext will be automatically selected.",
			"- You can select multiple kexts by entering their indices separated by commas (e.g., '1, 2, 3').",
			"",
			"B. Back",
			"Q. Quit")
		if len(messages) > 0 {
			contents = append(contents, "")
			for _, msg := range messages {
				contents = append(contents, "\033[1;96m"+msg+"\033[0m")
			}
			messages = nil
		}
		contents = append(contents, "")
		content := strings.Join(contents, "\n")

		m.adjustWindowSize(content)
		m.head("Configure Kernel Extensions", 68)
		m.logf("%s\n", content)
		option, err := m.input("Select your option: ")
		if err != nil {
			return err
		}
		switch strings.ToLower(option) {
		case "b":
			return nil
		case "q":
			return m.ExitProgram()
		}

		var indices []int
		for _, part := range strings.Split(option, ",") {
			p := strings.TrimSpace(part)
			if !isDigits(p) {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				continue // huge value: out of range, filtered below
			}
			if n-1 >= 0 && n-1 < len(m.Kexts) {
				indices = append(indices, n-1)
			}
		}
		var kextsToEnable []string
		for _, i := range indices {
			if !m.Kexts[i].Checked {
				kextsToEnable = append(kextsToEnable, m.Kexts[i].Name)
			}
		}

		forceLoad, err := m.VerifyKextCompatibility(kextsToEnable, macosVersion)
		if err != nil {
			return err
		}

		for _, index := range indices {
			kext := m.Kexts[index]
			if kext.Checked && !kext.Required {
				var dependents []string
				for _, o := range m.Kexts {
					if o.Checked && containsString(o.RequiresKexts, kext.Name) {
						dependents = append(dependents, o.Name)
					}
				}
				if _, err := m.UncheckKext(index); err != nil {
					return err
				}
				if len(dependents) > 0 {
					s := ""
					if len(dependents) > 1 {
						s = "s"
					}
					messages = append(messages, fmt.Sprintf("%s and dependent kext%s %s disabled.", kext.Name, s, strings.Join(dependents, ", ")))
				}
				continue
			}
			status, err := m.GetKextStatus(kext, macosVersion)
			if err != nil {
				return err
			}
			if status == "not_needed" {
				name, err := macos.MacOSName(macosVersion)
				if err != nil {
					return err
				}
				messages = append(messages, fmt.Sprintf("%s is not needed on %s.", kext.Name, name))
				continue
			}
			if status == "requires_newer" {
				messages = append(messages, fmt.Sprintf("%s requires a newer macOS version.", kext.Name))
				continue
			}

			var conflicts []string
			for _, o := range m.Kexts {
				if o.Checked && kext.ConflictGroupID != "" && o.ConflictGroupID == kext.ConflictGroupID && o.Name != kext.Name {
					conflicts = append(conflicts, o.Name)
				}
			}
			ok, err := m.CheckKext(index, macosVersion, forceLoad[kext.Name], nil)
			if err != nil {
				return err
			}
			if ok {
				if len(conflicts) > 0 {
					s := ""
					if len(conflicts) > 1 {
						s = "s"
					}
					messages = append(messages, fmt.Sprintf("%s selected; mutually exclusive kext%s disabled: %s.", kext.Name, s, strings.Join(conflicts, ", ")))
				}
			} else {
				messages = append(messages, fmt.Sprintf("%s could not be selected because a required dependency is unavailable.", kext.Name))
			}
		}
	}
}
