package smbios

import (
	"errors"
	"fmt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/term"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// ErrExit stands for sys.exit(0) in utils.exit_program.
var ErrExit = errors.New("exit program")

// ACPIPatch is the part of an acpi_guru patch used by SMBIOSSpecificOptions.
// *acpiguru.Patch needs a small adapter to implement it.
type ACPIPatch interface {
	PatchName() string
	SetChecked(bool)
}

// Menu holds the injected collaborators of the interactive SMBIOS functions.
type Menu struct {
	// Log receives the terminal output (nil discards it).
	Log io.Writer
	// RequestInput mirrors utils.request_input.
	RequestInput func(prompt string) (string, error)
	// CheckKext mirrors kext_maestro.check_kext(index, macos_version,
	// force_load). index is -1 when kext_index_by_name.get returned None.
	CheckKext func(index int, macosVersion string, forceLoad bool) error
	// ExitProgram mirrors utils.exit_program; it should return ErrExit (or
	// another error). Nil returns ErrExit directly.
	ExitProgram func() error
}

func (m *Menu) logf(format string, a ...any) {
	if m.Log != nil {
		fmt.Fprintf(m.Log, format, a...)
	}
}

func (m *Menu) exit() error {
	if m.ExitProgram != nil {
		return m.ExitProgram()
	}
	return ErrExit
}

// head mirrors utils.head(text, resize=False).
func (m *Menu) head(text string) {
	if m.Log != nil {
		term.Head(m.Log, text, 68, false)
	}
}

// SMBIOSSpecificOptions mirrors SMBIOS.smbios_specific_options.
func (m *Menu) SMBIOSSpecificOptions(report *plist.Dict, smbiosModel, macosVersion string, patches []ACPIPatch) error {
	for _, patch := range patches {
		if patch.PatchName() == "MCHC" {
			cpu, err := pyrt.Get(report, "CPU")
			if err != nil {
				return err
			}
			manu, err := pyrt.Get(cpu, "Manufacturer")
			if err != nil {
				return err
			}
			intel, err := pyrt.In("Intel", manu)
			if err != nil {
				return err
			}
			patch.SetChecked(intel && !strings.Contains(smbiosModel, "MacPro"))
		}
	}

	var selected []string
	if strings.Contains(smbiosModel, "MacPro7,1") {
		selected = append(selected, "RestrictEvents")
	}

	n := len(devices)
	slice := func(a, b int) []Device {
		if b > n {
			b = n
		}
		if a >= b {
			return nil
		}
		return devices[a:b]
	}
	var touchID []Device
	touchID = append(touchID, slice(31, 34)...)
	touchID = append(touchID, slice(48, 50)...)
	touchID = append(touchID, slice(51, 61)...)
	for _, d := range touchID {
		if d.Name == smbiosModel {
			selected = append(selected, "NoTouchID")
			break
		}
	}

	for _, name := range selected {
		index, ok := datasets.KextIndexByName[name]
		if !ok {
			index = -1
		}
		macName, err := macos.MacOSName(macosVersion)
		if err != nil {
			return err
		}
		if m.CheckKext == nil {
			return errors.New("smbios: CheckKext is not set")
		}
		if err := m.CheckKext(index, macosVersion, strings.Contains(macName, "Beta")); err != nil {
			return err
		}
	}
	return nil
}

// ReportFromDict converts the hardware report into the HardwareReport used by
// SelectDefaultModel. Deliberate difference: values are converted eagerly, so
// a malformed field raises even on paths where Python would not read it. A
// missing "Core Count" becomes 0.
func ReportFromDict(report *plist.Dict) (HardwareReport, error) {
	var r HardwareReport
	str := func(obj any, key string) (string, error) {
		v, err := pyrt.Get(obj, key)
		if err != nil {
			return "", err
		}
		if v == nil {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("%w: %q is %T, expected str", pyrt.ErrType, key, v)
		}
		return s, nil
	}
	mb, err := pyrt.Get(report, "Motherboard")
	if err != nil {
		return r, err
	}
	if r.Motherboard.Name, err = str(mb, "Name"); err != nil {
		return r, err
	}
	if r.Motherboard.Platform, err = str(mb, "Platform"); err != nil {
		return r, err
	}
	cpu, err := pyrt.Get(report, "CPU")
	if err != nil {
		return r, err
	}
	if r.CPU.Codename, err = str(cpu, "Codename"); err != nil {
		return r, err
	}
	if r.CPU.ProcessorName, err = str(cpu, "Processor Name"); err != nil {
		return r, err
	}
	cc, err := pyrt.Get(cpu, "Core Count")
	if err != nil {
		return r, err
	}
	if cc != nil {
		v, err := pyrt.IntValue(cc)
		if err != nil {
			return r, err
		}
		r.CPU.CoreCount = int(v)
	}
	gpu, err := pyrt.Get(report, "GPU")
	if err != nil {
		return r, err
	}
	items, err := pyrt.Items(gpu)
	if err != nil {
		return r, err
	}
	for _, it := range items {
		dt, err := str(it.Value, "Device Type")
		if err != nil {
			return r, err
		}
		r.GPUs = append(r.GPUs, GPU{DeviceType: dt})
	}
	return r, nil
}

// CustomizeSMBIOSModel mirrors SMBIOS.customize_smbios_model.
func (m *Menu) CustomizeSMBIOSModel(report *plist.Dict, selectedModel, macosVersion string) (string, error) {
	hr, err := ReportFromDict(report)
	if err != nil {
		return "", err
	}
	defaultModel, err := SelectDefaultModel(hr, macosVersion)
	if err != nil {
		return "", err
	}
	showAll := false
	mb, err := pyrt.Get(report, "Motherboard")
	if err != nil {
		return "", err
	}
	plat, err := pyrt.Get(mb, "Platform")
	if err != nil {
		return "", err
	}
	isLaptop := pyrt.Equal("Laptop", plat)
	currentCategory := ""
	categorySet := false

	for {
		incompatible := map[int]bool{}
		contents := []string{"", ""}
		if showAll {
			contents[1] = "List of available SMBIOS:"
		} else {
			contents[1] = "List of compatible SMBIOS:"
		}
		cur, err := macos.ParseVersion(macosVersion)
		if err != nil {
			return "", err
		}
		for i, d := range devices {
			index := i + 1
			lo, err := macos.ParseVersion(d.InitialSupport)
			if err != nil {
				return "", err
			}
			hi, err := macos.ParseVersion(d.LastSupportedVersion)
			if err != nil {
				return "", err
			}
			supported := lo.Compare(cur) <= 0 && cur.Compare(hi) <= 0
			isBook := strings.HasPrefix(d.Name, "MacBook")
			if d.Name != defaultModel && d.Name != selectedModel && !showAll &&
				(!supported || (isLaptop && !isBook) || (!isLaptop && isBook)) {
				incompatible[i] = true
				continue
			}

			category := ""
			for _, c := range d.Name {
				if unicode.IsDigit(c) {
					break
				}
				category += string(c)
			}
			if !categorySet || category != currentCategory {
				currentCategory = category
				categorySet = true
				name := currentCategory
				if name == "" {
					name = "Uncategorized"
				}
				header := "Category: " + name
				contents = append(contents, "\n"+header+"\n"+strings.Repeat("=", len([]rune(header))))
			}
			checkbox := "[ ]"
			if d.Name == selectedModel {
				checkbox = "[*]"
			}
			gpu := ""
			if d.DiscreteGPU != "" {
				gpu = " - " + d.DiscreteGPU
			}
			line := fmt.Sprintf("%s %2d. %-15s - %-10s %-20s%s", checkbox, index, d.Name, d.CPU, "("+d.CPUGeneration+")", gpu)
			if d.Name == selectedModel {
				line = "\033[1;32m" + line + "\033[0m"
			} else if !supported {
				line = "\033[90m" + line + "\033[0m"
			}
			contents = append(contents, line)
		}
		macName, err := macos.MacOSName(macosVersion)
		if err != nil {
			return "", err
		}
		contents = append(contents, "", "\033[1;93mNote:\033[0m",
			"- Lines in gray indicate mac models that are not officially supported by "+macName+".", "")
		if !showAll {
			contents = append(contents, "A. Show all models")
		} else {
			contents = append(contents, "C. Show compatible models only")
		}
		if selectedModel != defaultModel {
			contents = append(contents, "R. Restore default SMBIOS model ("+defaultModel+")")
		}
		contents = append(contents, "", "B. Back", "Q. Quit", "")

		m.head("Customize SMBIOS Model")
		m.logf("%s\n", strings.Join(contents, "\n"))
		if m.RequestInput == nil {
			return "", errors.New("smbios: RequestInput is not set")
		}
		option, err := m.RequestInput("Select your option: ")
		if err != nil {
			return "", err
		}
		lower := strings.ToLower(option)
		switch {
		case lower == "q":
			return "", m.exit()
		case lower == "b":
			return selectedModel, nil
		case lower == "r" && selectedModel != defaultModel:
			return defaultModel, nil
		case lower == "a" || lower == "c":
			showAll = !showAll
			continue
		}

		if isDigits(strings.TrimSpace(option)) {
			n, err := strconv.Atoi(strings.TrimSpace(option))
			if err != nil {
				continue // beyond any valid index
			}
			index := n - 1
			if index >= 0 && index < len(devices) {
				if !showAll && incompatible[index] {
					continue
				}
				selectedModel = devices[index].Name
			}
		}
	}
}

// isDigits mirrors str.isdigit for non-empty strings (ASCII and Unicode digits).
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
