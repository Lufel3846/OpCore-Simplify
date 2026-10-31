package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/acpiguru"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/compat"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/config"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/customizer"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/efi"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/gathering"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/github"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/kextmaestro"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/smbios"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/validator"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/wifi"
)

// App mirrors OCPE: it owns the collaborators and the interactive flow.
type App struct {
	U          *Utils
	AC         *acpiguru.Guru
	C          *compat.Checker
	CO         *config.Prodigy
	O          *gathering.Gatherer
	H          *customizer.Customizer
	K          *kextmaestro.Maestro
	SMenu      *smbios.Menu
	SSerial    *smbios.Serial
	V          *validator.Validator
	ResultDir  string
	ScriptsDir string
	BaseDir    string
}

func osName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "Darwin"
	}
	return "Linux"
}

func pyPlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	}
	return "linux"
}

// New mirrors OCPE.__init__. A missing iasl is the exception DSDT.__init__ raises.
func New() (*App, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	base := filepath.Dir(exe)
	scripts := filepath.Join(base, "Scripts")

	a := &App{U: NewUtils("OpCore Simplify"), BaseDir: base, ScriptsDir: scripts}
	a.U.CleanTemporaryDir()
	out := a.U.Out
	in := a.U.RequestInput

	iasl := dsdt.IaslFinder{ScriptDir: scripts, Platform: pyPlatform(), Log: out}.Check(false)
	if iasl == "" {
		return nil, dsdt.MissingIaslError(scripts, pyPlatform())
	}
	d := dsdt.New(iasl)
	d.Log = out
	a.AC = acpiguru.New(d, in)
	a.AC.Log = out

	a.C = compat.NewChecker()
	a.C.Log = out
	a.C.RequestInput = in

	g, err := gathering.New(scripts, out)
	if err != nil {
		return nil, err
	}
	a.O = g
	a.O.RequestInput = func() { _ = a.U.Pause() }

	a.CO = config.NewProdigy(g)
	a.CO.RequestInput = in

	a.H = &customizer.Customizer{CompatibilityChecker: a.C, RequestInput: in, Log: out}

	a.K = kextmaestro.New(g.OckFilesDir)
	a.K.RequestInput = in
	a.K.Log = out
	wf := &wifi.Extractor{
		OS:    osName(),
		Input: in,
		Run: func(args []string) (string, int) {
			so, _, rc := runCommand(args)
			return so, rc
		},
	}
	a.K.WifiProfiles = wf.Pairs
	// gatheringFiles -> kext_maestro.process_kext
	g.ProcessKext = a.K.ProcessKextAccepted

	a.SSerial = smbios.NewSerial(scripts, osName(), g.DownloadHistoryFile, func() error {
		_, err := g.GatherBootloaderKexts(nil, datasets.KextIndexByName, "")
		return err
	})
	a.CO.GenerateSMBIOS = a.SSerial.GenerateMap
	a.AC.RandomMAC = a.SSerial.RandomMAC
	a.SMenu = &smbios.Menu{
		Log:          out,
		RequestInput: in,
		CheckKext: func(index int, macosVersion string, forceLoad bool) error {
			_, err := a.K.CheckKext(index, macosVersion, forceLoad, map[int]bool{})
			return err
		},
		ExitProgram: a.U.ExitProgram,
	}

	a.V = validator.New()
	a.ResultDir = filepath.Join(base, "Results")
	return a, nil
}

// ---- adapters -----------------------------------------------------------------

type smbiosPatch struct{ p *acpiguru.Patch }

func (s smbiosPatch) PatchName() string { return s.p.Name }
func (s smbiosPatch) SetChecked(c bool) { s.p.Checked = c }

func (a *App) smbiosPatches() []smbios.ACPIPatch {
	var l []smbios.ACPIPatch
	for _, p := range a.AC.Patches {
		l = append(l, smbiosPatch{p})
	}
	return l
}

func (a *App) kextPatches() []kextmaestro.ACPIPatch {
	var l []kextmaestro.ACPIPatch
	for _, p := range a.AC.Patches {
		l = append(l, kextmaestro.ACPIPatch{Name: p.Name, Checked: p.Checked})
	}
	return l
}

func (a *App) selectSMBIOSModel(customized *plist.Dict, macosVersion string) (string, error) {
	hr, err := smbios.ReportFromDict(customized)
	if err != nil {
		return "", err
	}
	return smbios.SelectDefaultModel(hr, macosVersion)
}

func (a *App) smbiosSpecificOptions(customized *plist.Dict, model, macosVersion string) error {
	return a.SMenu.SMBIOSSpecificOptions(customized, model, macosVersion, a.smbiosPatches())
}

func (a *App) selectRequiredKexts(customized *plist.Dict, macosVersion string, needsOCLP bool) (bool, error) {
	return a.K.SelectRequiredKexts(customized, macosVersion, needsOCLP, a.kextPatches())
}

func (a *App) println(s string)                 { a.U.printf("%s\n", s) }
func (a *App) printf(f string, args ...any)     { a.U.printf(f, args...) }
func (a *App) in(prompt string) (string, error) { return a.U.RequestInput(prompt) }

// ---- OCPE methods ---------------------------------------------------------------

// SelectHardwareReport mirrors select_hardware_report. data is the report.
func (a *App) SelectHardwareReport() (string, any, error) {
	a.AC.Dsdt = nil
	a.AC.Acpi.AcpiTables = dsdt.NewTables()
	nt := runtime.GOOS == "windows"

	for {
		a.U.Head("Select hardware report")
		a.println("")
		if nt {
			a.println("\033[1;93mNote:\033[0m")
			a.println("- Ensure you are using the latest version of Hardware Sniffer before generating the hardware report.")
			a.println("- Hardware Sniffer will not collect information related to Resizable BAR option of GPU (disabled by default) and monitor connections in Windows PE.")
			a.println("")
			a.println("E. Export hardware report (Recommended)")
			a.println("")
		}
		a.println("Q. Quit")
		a.println("")

		suffix := ""
		if nt {
			suffix = " or type \"E\" to export"
		}
		userInput, err := a.in(fmt.Sprintf("Drag and drop your hardware report here (.JSON)%s: ", suffix))
		if err != nil {
			return "", nil, err
		}
		if strings.ToLower(userInput) == "q" {
			return "", nil, a.U.ExitProgram()
		}
		if strings.ToLower(userInput) == "e" {
			if osName() == "Windows" {
				a.U.Head("Gathering Hardware Sniffer")
			}
			sniffer, err := a.O.GatherHardwareSniffer(osName(), func(owner, repo string) (*github.Release, error) {
				return github.GetLatestRelease(a.O.Fetcher, owner, repo)
			})
			if err != nil {
				return "", nil, err
			}
			if sniffer == "" {
				continue
			}
			reportDir := filepath.Join(a.BaseDir, "SysReport")

			a.U.Head("Exporting Hardware Report")
			a.println("")
			a.printf("Exporting hardware report to %s...\n", reportDir)

			_, _, rc := runCommand([]string{sniffer, "-e", "-o", reportDir})
			if rc != 0 {
				var msg string
				switch rc {
				case 3:
					msg = "Error collecting hardware."
				case 4:
					msg = "Error generating hardware report."
				case 5:
					msg = "Error dumping ACPI tables."
				default:
					msg = "Unknown error."
				}
				a.println("")
				a.printf("Could not export the hardware report. %s\n", msg)
				a.println("Please try again or using Hardware Sniffer manually.")
				a.println("")
				if err := a.U.Pause(); err != nil {
					return "", nil, err
				}
				continue
			}
			reportPath := filepath.Join(reportDir, "Report.json")
			acpiDir := filepath.Join(reportDir, "ACPI")
			data, err := fsutil.ReadFile(reportPath)
			if err != nil {
				return "", nil, err
			}
			if _, _, err := a.AC.ReadACPITables(acpiDir); err != nil {
				return "", nil, err
			}
			return reportPath, data, nil
		}

		path, err := a.U.NormalizePath(userInput)
		if err != nil {
			return "", nil, err
		}
		isValid, errs, warns, data := a.V.ValidateReport(path)

		a.U.Head("Validation Report")
		validator.ShowValidationReport(a.U.Out, path, isValid, errs, warns)
		if !isValid || len(errs) > 0 {
			a.println("")
			a.println("\033[32mSuggestion:\033[0m Please re-export the hardware report and try again.")
			a.println("")
			if _, err := a.in("Press Enter to go back..."); err != nil {
				return "", nil, err
			}
		} else {
			return path, data, nil
		}
	}
}

// ShowOCLPWarning mirrors show_oclp_warning.
func (a *App) ShowOCLPWarning(macosVersion string) (bool, error) {
	cur, err := a.U.ParseDarwinVersion(macosVersion)
	if err != nil {
		return false, err
	}
	tahoe, _ := a.U.ParseDarwinVersion("25.0.0")
	isTahoe := cur.Compare(tahoe) >= 0

	for {
		a.U.Head("OpenCore Legacy Patcher Warning")
		a.println("")
		a.println("\033[1;96mWhy it is required:\033[0m")
		a.println("- Restores compatibility for GPUs and Broadcom WiFi.")
		if isTahoe {
			a.println("- Rolls back AppleHDA to restore audio support through AppleALC.")
		}
		a.println("")
		a.println("\033[1;93mRisks and limitations:\033[0m")
		a.println("- Disables macOS security features, including SIP and AMFI.")
		a.println("- macOS updates require full installers, apps may crash or the system may become unstable.")
		a.println("- OpenCore Legacy Patcher does not officially support Hackintosh systems.")
		a.println("")
		a.println("\033[1;91mReview these risks before continuing.\033[0m")
		a.println("")
		option, err := a.in("Continue with OpenCore Legacy Patcher? (yes/No): ")
		if err != nil {
			return false, err
		}
		option = strings.ToLower(strings.TrimSpace(option))
		if option == "yes" {
			return true, nil
		} else if option == "no" {
			return false, nil
		}
	}
}

var versionPattern = regexp.MustCompile(`^(\d+)(?:\.(\d+)(?:\.(\d+))?)?$`)

func isNoneNone(v any) bool {
	l, ok := v.([]any)
	return ok && len(l) == 2 && l[0] == nil && l[1] == nil
}

func (a *App) cmpVer(x, y string) (int, error) {
	vx, err := a.U.ParseDarwinVersion(x)
	if err != nil {
		return 0, err
	}
	vy, err := a.U.ParseDarwinVersion(y)
	if err != nil {
		return 0, err
	}
	return vx.Compare(vy), nil
}

func major2(s string) (int, error) {
	r := []rune(s)
	if len(r) > 2 {
		r = r[:2]
	}
	n, err := strconv.Atoi(string(r))
	if err != nil {
		return 0, fmt.Errorf("ValueError: invalid literal for int() with base 10: %q", string(r))
	}
	return n, nil
}

func macName(v string) (string, error) { return macos.MacOSName(v) }

// SelectMacOSVersion mirrors select_macos_version. native is
// (min, max); oclp is (max, min) or nil.
func (a *App) SelectMacOSVersion(report any, native [2]string, oclp *[2]string) (string, error) {
	suggested := native[1]

	for _, deviceType := range []string{"GPU", "Network", "Bluetooth", "SD Controller"} {
		present, err := pyrt.In(deviceType, report)
		if err != nil {
			return "", err
		}
		if !present {
			continue
		}
		devs, err := pyrt.Index(report, deviceType)
		if err != nil {
			return "", err
		}
		items, err := pyrt.Items(devs)
		if err != nil {
			return "", err
		}
		for _, item := range items {
			props := item.Value
			compatV, err := pyrt.GetDefault(props, "Compatibility", []any{nil, nil})
			if err != nil {
				return "", err
			}
			if isNoneNone(compatV) {
				continue
			}
			if deviceType == "GPU" {
				dt, err := pyrt.Get(props, "Device Type")
				if err != nil {
					return "", err
				}
				if dt == "Integrated GPU" {
					idv, err := pyrt.GetDefault(props, "Device ID", "")
					if err != nil {
						return "", err
					}
					ids, ok := idv.(string)
					if !ok {
						return "", fmt.Errorf("%w: %T is not subscriptable", pyrt.ErrType, idv)
					}
					r := []rune(ids)
					if len(r) > 5 {
						ids = string(r[5:])
					} else {
						ids = ""
					}
					manu, err := pyrt.Get(props, "Manufacturer")
					if err != nil {
						return "", err
					}
					if manu == "AMD" || strings.HasPrefix(ids, "59") || strings.HasPrefix(ids, "87C0") {
						suggested = "22.99.99"
					} else if strings.HasPrefix(ids, "09") || strings.HasPrefix(ids, "19") {
						suggested = "21.99.99"
					}
				}
			}
			cl, ok := compatV.([]any)
			if !ok || len(cl) == 0 {
				return "", errors.New("TypeError: 'Compatibility' is not a sequence")
			}
			first, ok := cl[0].(string)
			if !ok {
				return "", fmt.Errorf("%w: %T has no attribute 'split'", pyrt.ErrAttribute, cl[0])
			}
			c, err := a.cmpVer(suggested, first)
			if err != nil {
				return "", err
			}
			if c > 0 {
				suggested = first
			}
		}
	}

	for {
		name, err := macName(suggested)
		if err != nil {
			return "", err
		}
		if !strings.Contains(name, "Beta") {
			break
		}
		m, err := major2(suggested)
		if err != nil {
			return "", err
		}
		r := []rune(suggested)
		rest := ""
		if len(r) > 2 {
			rest = string(r[2:])
		}
		suggested = fmt.Sprintf("%d%s", m-1, rest)
	}

	for {
		a.U.Head("Select macOS Version")
		nm, _ := major2(native[1])
		sm, err := major2(suggested)
		if err != nil {
			return "", err
		}
		if nm != sm {
			sname, err := macName(suggested)
			if err != nil {
				return "", err
			}
			a.println("")
			a.println("\033[1;36mSuggested macOS version:\033[0m")
			a.printf("- For better compatibility and stability, we suggest you to use only %s or older.\n", sname)
		}
		a.println("")
		a.println("Available macOS versions:")
		a.println("")

		oclpMin, oclpMax := 99, 0
		if oclp != nil {
			if oclpMin, err = major2(oclp[1]); err != nil {
				return "", err
			}
			if oclpMax, err = major2(oclp[0]); err != nil {
				return "", err
			}
		}
		nMin, err := major2(native[0])
		if err != nil {
			return "", err
		}
		nMax, err := major2(native[1])
		if err != nil {
			return "", err
		}
		minV, maxV := nMin, nMax
		if oclpMin < minV {
			minV = oclpMin
		}
		if oclpMax > maxV {
			maxV = oclpMax
		}
		for dv := minV; dv <= maxV; dv++ {
			name, err := macName(strconv.Itoa(dv))
			if err != nil {
				return "", err
			}
			label := ""
			if oclpMin <= dv && dv <= oclpMax {
				label = " (\033[1;93mRequires OpenCore Legacy Patcher\033[0m)"
			}
			a.printf("   %d. %s%s\n", dv, name, label)
		}

		a.println("")
		a.println("\033[1;93mNote:\033[0m")
		a.println("- To select a major version, enter the number (e.g., 19).")
		a.println("- To specify a full version, use the Darwin version format (e.g., 22.4.6).")
		a.println("")
		a.println("Q. Quit")
		a.println("")
		defName, err := macName(suggested)
		if err != nil {
			return "", err
		}
		option, err := a.in(fmt.Sprintf("Please enter the macOS version you want to use (default: %s): ", defName))
		if err != nil {
			return "", err
		}
		if option == "" {
			option = suggested
		}
		if strings.ToLower(option) == "q" {
			return "", a.U.ExitProgram()
		}

		m := versionPattern.FindStringSubmatch(option)
		if m == nil {
			continue
		}
		g2, g3 := m[2], m[3]
		if g2 == "" {
			g2 = "99"
		}
		if g3 == "" {
			g3 = "99"
		}
		target := fmt.Sprintf("%s.%s.%s", m[1], g2, g3)

		if oclp != nil {
			lo, err := a.cmpVer(oclp[1], target)
			if err != nil {
				return "", err
			}
			if lo <= 0 {
				hi, err := a.cmpVer(target, oclp[0])
				if err != nil {
					return "", err
				}
				if hi <= 0 {
					return target, nil
				}
			}
		}
		lo, err := a.cmpVer(native[0], target)
		if err != nil {
			return "", err
		}
		if lo <= 0 {
			hi, err := a.cmpVer(target, native[1])
			if err != nil {
				return "", err
			}
			if hi <= 0 {
				return target, nil
			}
		}
	}
}

// BuildOpenCoreEFI mirrors build_opencore_efi through efi.Builder. Deliberate
// difference: the builder reports no intermediate steps, so the progress screen
// shows step 0 while it runs and the finished list afterwards.
func (a *App) BuildOpenCoreEFI(report any, disabled any, smbiosModel, macosVersion string, needsOCLP bool) error {
	steps := []string{
		"Copying EFI base to results folder",
		"Applying ACPI patches",
		"Copying kexts and snapshotting to config.plist",
		"Generating config.plist",
		"Cleaning up unused drivers, resources, and tools",
	}
	title := "Building OpenCore EFI"
	a.U.ProgressBar(title, steps, 0, false)

	b := &efi.Builder{
		OCKDir:    a.K.OckFilesDir,
		ResultDir: a.ResultDir,
		Kexts:     a.K,
		Config:    a.CO,
		ACPI:      a.AC,
	}
	if err := b.Build(report, disabled, macosVersion, needsOCLP, smbiosModel); err != nil {
		return err
	}
	a.U.ProgressBar(title, steps, len(steps), true)
	a.println("OpenCore EFI build complete.")
	time.Sleep(2 * time.Second)
	return nil
}

// CheckBIOSRequirements mirrors check_bios_requirements.
func (a *App) CheckBIOSRequirements(orgReport, report any) ([]string, error) {
	var requirements []string
	sub := func(r any, key string) (any, error) { return pyrt.GetDefault(r, key, plist.NewDict()) }

	bios, err := sub(orgReport, "BIOS")
	if err != nil {
		return nil, err
	}
	orgFW, err := pyrt.GetDefault(bios, "Firmware Type", "Unknown")
	if err != nil {
		return nil, err
	}
	bios, err = sub(report, "BIOS")
	if err != nil {
		return nil, err
	}
	fw, err := pyrt.GetDefault(bios, "Firmware Type", "Unknown")
	if err != nil {
		return nil, err
	}
	if orgFW == "Legacy" && fw == "UEFI" {
		requirements = append(requirements, "Enable UEFI mode (disable Legacy/CSM (Compatibility Support Module))")
	}

	bios, err = sub(report, "BIOS")
	if err != nil {
		return nil, err
	}
	sb, err := pyrt.GetDefault(bios, "Secure Boot", "Unknown")
	if err != nil {
		return nil, err
	}
	if sb != "Disabled" {
		requirements = append(requirements, "Disable Secure Boot")
	}

	mb, err := sub(report, "Motherboard")
	if err != nil {
		return nil, err
	}
	platform, err := pyrt.Get(mb, "Platform")
	if err != nil {
		return nil, err
	}
	if platform == "Desktop" {
		mb, err = sub(report, "Motherboard")
		if err != nil {
			return nil, err
		}
		chipset, err := pyrt.Get(mb, "Chipset")
		if err != nil {
			return nil, err
		}
		if pyrt.InStrings(chipset, datasets.IntelChipsets[112:]) {
			gpus, err := sub(report, "GPU")
			if err != nil {
				return nil, err
			}
			vals, err := pyrt.Values(gpus)
			if err != nil {
				return nil, err
			}
			enabled := false
			for _, props := range vals {
				rb, err := pyrt.GetDefault(props, "Resizable BAR", "Disabled")
				if err != nil {
					return nil, err
				}
				if rb == "Enabled" {
					enabled = true
					break
				}
			}
			if !enabled {
				requirements = append(requirements, "Enable Above 4G Decoding")
				requirements = append(requirements, "Disable Resizable BAR/Smart Access Memory")
			}
		}
	}
	return requirements, nil
}

// BeforeUsingEFI mirrors before_using_efi.
func (a *App) BeforeUsingEFI(orgReport, report any) error {
	a.U.Head("Before Using EFI")
	a.println("")
	a.println("\033[93mPlease complete the following steps:\033[0m")
	a.println("")

	reqs, err := a.CheckBIOSRequirements(orgReport, report)
	if err != nil {
		return err
	}
	if len(reqs) > 0 {
		a.println("* BIOS/UEFI Settings Required:")
		for _, r := range reqs {
			a.printf("    - %s\n", r)
		}
		a.println("")
	}

	kextDir := "EFI/OC/Kexts"
	if runtime.GOOS == "windows" {
		kextDir = "EFI\\OC\\Kexts"
	}
	a.println("* USB Mapping:")
	a.println("    - Use USBToolBox tool to map USB ports.")
	a.printf("    - Add created UTBMap.kext into the %s folder.\n", kextDir)
	a.printf("    - Remove UTBDefault.kext in the %s folder.\n", kextDir)
	a.println("    - Edit config.plist:")
	a.println("        - Use ProperTree to open your config.plist.")
	a.println("        - Run OC Snapshot by pressing Command/Ctrl + R.")
	a.println("        - If you have more than 15 ports on a single controller, enable the XhciPortLimit patch.")
	a.println("        - Save the file when finished.")
	a.println("")
	if err := a.U.Pause(); err != nil {
		return err
	}
	return a.U.OpenFolder(a.ResultDir)
}

func (a *App) gatherKexts(macosVersion string) error {
	var ks []*gathering.Kext
	for _, k := range a.K.Kexts {
		gk := &gathering.Kext{Name: k.Name, Checked: k.Checked, DownloadInfo: k.DownloadInfo}
		if k.GithubRepo != nil {
			gk.GithubRepo = &gathering.Repo{Owner: k.GithubRepo.Owner, Repo: k.GithubRepo.Repo}
		}
		ks = append(ks, gk)
	}
	_, err := a.O.GatherBootloaderKexts(ks, datasets.KextIndexByName, macosVersion)
	return err
}

func unbound(name string) error {
	return fmt.Errorf("UnboundLocalError: local variable '%s' referenced before assignment", name)
}

// Main mirrors OCPE.main. Its state is local, as in the original, so it is
// lost when an error ends the call.
func (a *App) Main() error {
	var (
		reportPath string
		report     any
		native     [2]string
		nativeSet  bool
		oclp       *[2]string
		disabled   *plist.Dict
		macosVer   string
		needsOCLP  bool
		smbiosMod  string
		customized *plist.Dict
	)

	// reselect repeats the version-dependent steps shared by options 1, 2 and 6.
	reselect := func() error {
		var err error
		if !nativeSet {
			return errors.New("TypeError: 'NoneType' object is not subscriptable")
		}
		macosVer, err = a.SelectMacOSVersion(report, native, oclp)
		if err != nil {
			return err
		}
		customized, disabled, needsOCLP, err = a.H.HardwareCustomization(report, macosVer)
		if err != nil {
			return err
		}
		smbiosMod, err = a.selectSMBIOSModel(customized, macosVer)
		return err
	}
	tail := func() error {
		var err error
		needsOCLP, err = a.selectRequiredKexts(customized, macosVer, needsOCLP)
		if err != nil {
			return err
		}
		return a.smbiosSpecificOptions(customized, smbiosMod, macosVer)
	}

	for {
		a.U.Head("")
		a.println("")
		sel := reportPath
		if sel == "" {
			sel = "Not selected"
		}
		a.printf("  Hardware Report: %s\n", sel)
		if reportPath != "" {
			a.println("")
			line := "Not selected"
			if macosVer != "" {
				n, err := macName(macosVer)
				if err != nil {
					return err
				}
				line = n
			}
			line = "  macOS Version:   " + line
			if macosVer != "" {
				line += " (" + macosVer + ")"
			}
			if needsOCLP {
				line += ". \033[1;93mRequires OpenCore Legacy Patcher\033[0m"
			}
			a.println(line)
			sm := smbiosMod
			if sm == "" {
				sm = "Not selected"
			}
			a.printf("  SMBIOS:          %s\n", sm)
			if disabled != nil && disabled.Len() > 0 {
				a.println("  Disabled Devices:")
				for _, d := range disabled.Keys() {
					a.printf("    - %s\n", d)
				}
			}
		}
		a.println("")

		a.println("1. Select Hardware Report")
		a.println("2. Select macOS Version")
		a.println("3. Customize ACPI Patch")
		a.println("4. Customize Kexts")
		a.println("5. Customize SMBIOS Model")
		a.println("6. Build OpenCore EFI")
		a.println("")
		a.println("Q. Quit")
		a.println("")

		option, err := a.in("Select an option: ")
		if err != nil {
			return err
		}
		if strings.ToLower(option) == "q" {
			return a.U.ExitProgram()
		}

		if option == "1" {
			reportPath, report, err = a.SelectHardwareReport()
			if err != nil {
				return err
			}
			var rep any
			rep, native, oclp, err = a.C.CheckCompatibility(report)
			if err != nil {
				return err
			}
			report, nativeSet = rep, true
			if err := reselect(); err != nil {
				return err
			}
			ok, err := a.AC.EnsureDSDT(false)
			if err != nil {
				return err
			}
			if !ok {
				if _, _, err := a.AC.SelectACPITables(); err != nil {
					return err
				}
			}
			if err := a.AC.SelectACPIPatches(customized, disabled); err != nil {
				return err
			}
			if err := tail(); err != nil {
				return err
			}
		}

		if reportPath == "" {
			a.U.Head("")
			a.println("\n")
			a.println("\033[1;93mPlease select a hardware report first.\033[0m")
			a.println("\n")
			if _, err := a.in("Press Enter to go back..."); err != nil {
				return err
			}
			continue
		}

		switch option {
		case "2":
			if err := reselect(); err != nil {
				return err
			}
			if err := tail(); err != nil {
				return err
			}
		case "3":
			if err := a.AC.CustomizePatchSelection(); err != nil {
				return err
			}
		case "4":
			if err := a.K.KextConfigurationMenu(macosVer); err != nil {
				return err
			}
		case "5":
			if customized == nil {
				return unbound("customized_hardware")
			}
			smbiosMod, err = a.SMenu.CustomizeSMBIOSModel(customized, smbiosMod, macosVer)
			if err != nil {
				return err
			}
			if err := a.smbiosSpecificOptions(customized, smbiosMod, macosVer); err != nil {
				return err
			}
		case "6":
			if needsOCLP {
				ok, err := a.ShowOCLPWarning(macosVer)
				if err != nil {
					return err
				}
				if !ok {
					if err := reselect(); err != nil {
						return err
					}
					if err := tail(); err != nil {
						return err
					}
					continue
				}
			}

			if err := a.gatherKexts(macosVer); err != nil {
				if isExit(err) {
					return err
				}
				a.printf("\033[91mError: %s\033[0m\n", err.Error())
				a.println("")
				if _, err := a.in("Press Enter to continue..."); err != nil {
					return err
				}
				continue
			}

			if customized == nil {
				return unbound("customized_hardware")
			}
			if err := a.BuildOpenCoreEFI(customized, disabled, smbiosMod, macosVer, needsOCLP); err != nil {
				return err
			}
			if err := a.BeforeUsingEFI(report, customized); err != nil {
				return err
			}

			a.U.Head("Result")
			a.println("")
			mb, err := pyrt.Get(customized, "Motherboard")
			if err != nil {
				return err
			}
			name, err := pyrt.Get(mb, "Name")
			if err != nil {
				return err
			}
			a.printf("Your OpenCore EFI for %s has been built at:\n", pyStr(name))
			a.printf("\t%s\n", a.ResultDir)
			a.println("")
			if _, err := a.in("Press Enter to main menu..."); err != nil {
				return err
			}
		}
	}
}

func pyStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	}
	return fmt.Sprint(v)
}

// Run mirrors the __main__ block (without the updater) and returns the process
// exit code. Any error is shown on an error screen and the loop restarts; an
// exit request ends the program with 0.
func Run() int {
	a, err := New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Traceback:", err)
		return 1
	}
	for {
		err := a.Main()
		if err == nil {
			continue
		}
		if isExit(err) {
			return 0
		}
		a.U.Head("An Error Occurred")
		a.println("")
		a.println(err.Error())
		if e := a.U.Pause(); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
	}
}
