package compat

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

func emptyDict() any { return plist.NewDict() }

// CheckCPUCompatibility mirrors check_cpu_compatibility. Returns ErrExit where
// Python calls exit_program().
func (c *Checker) CheckCPUCompatibility() (err error) {
	defer recoverErr(&err)
	c.checkCPU()
	return nil
}

func (c *Checker) checkCPU() {
	maxV := macos.LatestDarwinVersion(true)
	minV := macos.LowestDarwinVersion()
	cpu := func() any { return get(c.hardwareReport, "CPU") }
	simd := func() any { return get(cpu(), "SIMD Features") }

	if !in("SSE4", simd()) {
		maxV, minV = "", ""
	} else if !in("SSE4.2", simd()) {
		minV = "18.0.0"
		if in("SSE4.1", simd()) {
			maxV = "21.99.99"
		}
	}

	asDict(cpu()).Set("Compatibility", pair(maxV, minV))
	cp, _ := asDict(cpu()).Get("Compatibility")
	c.printf("%s- %s: %s\n", "   ", pyStr(get(cpu(), "Processor Name")), c.showMacOSCompatibility(cp))

	if maxV == minV && maxV == "" {
		c.println("")
		c.println("Missing required SSE4.x instruction set.")
		c.println("Your CPU is not supported by macOS versions newer than Sierra (10.12).")
		c.println("")
		c.requestInputExit()
	}
	c.maxNative = maxV
	c.minNative = minV
}

// CheckGPUCompatibility mirrors check_gpu_compatibility.
func (c *Checker) CheckGPUCompatibility() (err error) {
	defer recoverErr(&err)
	c.checkGPU()
	return nil
}

func (c *Checker) checkGPU() {
	report := c.hardwareReport
	if !pyrt.Truthy(get(report, "GPU")) {
		c.println("")
		c.println("No GPU found!")
		c.println("Please make sure to export the hardware report with the GPU information")
		c.println("and try again.")
		c.println("")
		c.requestInputExit()
	}
	simd := func() any { return get(get(report, "CPU"), "SIMD Features") }
	noAVX2 := func() bool { return !in("AVX2", simd()) }

	gpuIdx, err := pyrt.Index(report, "GPU")
	check(err)
	for _, p := range items(gpuIdx) {
		gpuName := p.Key
		gpuProps := asDict(p.Value)
		manufacturer := get(gpuProps, "Manufacturer")
		codename := get(gpuProps, "Codename")
		idRunes := []rune(str(get(gpuProps, "Device ID")))
		deviceID := ""
		if len(idRunes) > 5 {
			deviceID = string(idRunes[5:])
		}

		maxV := macos.LatestDarwinVersion(true)
		minV := macos.LowestDarwinVersion()
		oclMax := macos.LatestDarwinVersion(false)
		oclMin := "20.0.0"
		naviDev := false

		if in("Intel", manufacturer) {
			secondLast := func() string {
				r := []rune(deviceID)
				if len(r) < 2 {
					check(fmt.Errorf("IndexError: string index out of range"))
				}
				return string(r[len(r)-2])
			}
			if hasAnyPrefix(deviceID, "0042", "0046") && !pyrt.Equal(get(get(report, "Motherboard"), "Platform"), "Desktop") {
				maxV = "17.99.99"
			} else if strings.HasPrefix(deviceID, "01") && !inList(secondLast(), "5", "6") && !inList(deviceID, "0102", "0106", "010A") {
				maxV = "17.99.99"
			} else if strings.HasPrefix(deviceID, "01") && !inList(deviceID, "0152", "0156") {
				maxV = "20.99.99"
			} else if hasAnyPrefix(deviceID, "04", "0A", "0C", "0D", "0B", "16") {
				maxV = "21.99.99"
			} else if hasAnyPrefix(deviceID, "09", "19", "59", "3E", "87", "9B") && !inList(deviceID, "3E90", "3E93", "3E99", "3E9C", "3EA1", "3EA4", "9B21", "9BA0", "9BA2", "9BA4", "9BA5", "9BA8", "9BAA", "9BAB", "9BAC") {
				// pass
			} else if strings.HasPrefix(deviceID, "8A") {
				minV = "19.4.0"
			} else {
				maxV, minV = "", ""
			}

			if c.isLowEndIntelCPU(get(get(report, "CPU"), "Processor Name")) {
				maxV, minV = "", ""
			}
		} else if in("AMD", manufacturer) {
			if in("Navi 2", codename) {
				if noAVX2() {
					maxV = "21.99.99"
					oclMin = "22.0.0"
					naviDev = true
				} else {
					if inList(pyStrOrEmpty(codename), "Navi 23", "Navi 22") {
						minV = "21.2.0"
					} else if in("Navi 21", codename) {
						minV = "20.5.0"
					} else {
						maxV, minV = "", ""
					}
				}
			} else if in("Navi 1", codename) {
				if noAVX2() {
					maxV = "21.99.99"
					oclMin = "22.0.0"
					naviDev = true
				}
				minV = "19.0.0"
			} else if in("Vega 20", codename) {
				if noAVX2() {
					maxV = "21.99.99"
					oclMin = "22.0.0"
				}
				minV = "18.6.0"
			} else if inList(pyStrOrEmpty(codename), "Vega 10", "Polaris 22", "Polaris 20", "Baffin", "Ellesmere") || inList(deviceID, "6995", "699F") {
				if noAVX2() {
					maxV = "21.99.99"
					oclMin = "22.0.0"
				}
				minV = "17.0.0"
			} else if containsAny(datasets.AMDCodenames, codename) {
				maxV = "21.99.99"
			} else if inList(deviceID, "15D8", "15DD", "15E7", "1636", "1638", "164C") {
				minV = "19.0.0"
			} else {
				maxV, minV = "", ""
			}
		} else if in("NVIDIA", manufacturer) {
			if in("Kepler", codename) {
				maxV = "20.99.99"
			} else if inList(pyStrOrEmpty(codename), "Pascal", "Maxwell", "Fermi", "Tesla") {
				maxV = "17.99.99"
				minV = "17.0.0"
			} else {
				maxV, minV = "", ""
			}
		}

		unsupported := (maxV == minV && maxV == "")
		if !unsupported {
			if in("Intel", manufacturer) && hasAnyPrefix(deviceID, "01", "04", "0A", "0C", "0D") {
				all := true
				for _, mp := range items(getD(report, "Monitor", emptyDict())) {
					if !(pyrt.Equal(get(mp.Value, "Connector Type"), "VGA") && pyrt.Equal(getD(mp.Value, "Connected GPU", gpuName), gpuName)) {
						all = false
						break
					}
				}
				unsupported = all
			}
		}
		if unsupported {
			gpuProps.Set("Compatibility", pair("", ""))
		} else {
			gpuProps.Set("Compatibility", pair(maxV, minV))
			if less(maxV, oclMax) {
				n, err := strconv.Atoi(string([]rune(maxV)[:2]))
				check(err)
				next := fmt.Sprintf("%d.%d.%d", n+1, 0, 0)
				m := next
				if greater(oclMin, next) {
					m = oclMin
				}
				gpuProps.Set("OCLP Compatibility", []any{oclMax, m})
			}
		}

		cp, _ := gpuProps.Get("Compatibility")
		c.status(gpuName, cp)

		if in("OCLP Compatibility", gpuProps) {
			suffix := ""
			if naviDev {
				suffix = " (requires developer mode)"
			}
			oc, _ := gpuProps.Get("OCLP Compatibility")
			c.printf("%s- OCLP Compatibility%s: %s\n", "      ", suffix, c.showMacOSCompatibility(oc))
		}
		var monitors []string
		for _, mp := range items(getD(report, "Monitor", emptyDict())) {
			if pyrt.Equal(get(mp.Value, "Connected GPU"), gpuName) {
				monitors = append(monitors, fmt.Sprintf("%s (%s)", mp.Key, pyStr(get(mp.Value, "Connector Type"))))
				if in("Intel", manufacturer) && hasAnyPrefix(deviceID, "01", "04", "0A", "0C", "0D") {
					if pyrt.Equal(get(mp.Value, "Connector Type"), "VGA") {
						r := []rune(monitors[len(monitors)-1])
						monitors[len(monitors)-1] = fmt.Sprintf("\033[0;31m%s%s\033[0m", string(r[:len(r)-1]), ", unsupported)")
					}
				}
			}
		}
		if len(monitors) > 0 {
			s := ""
			if len(monitors) > 1 {
				s = "s"
			}
			c.printf("%s- Connected Monitor%s: %s\n", "      ", s, strings.Join(monitors, ", "))
		}
	}

	for _, p := range items(get(report, "GPU")) {
		gpuProps := asDict(p.Value)
		gpuName := p.Key
		if !isNoneNone(get(gpuProps, "Compatibility")) {
			allOtherNone := true
			for _, o := range values(get(report, "GPU")) {
				if pyrt.Equal(o, gpuProps) {
					continue
				}
				if !isNoneNone(get(o, "Compatibility")) {
					allOtherNone = false
					break
				}
			}
			if allOtherNone {
				// pass
			} else {
				anyOther := false
				for _, m := range values(getD(report, "Monitor", emptyDict())) {
					if pyrt.Equal(get(m, "Connector Type"), "Internal") && !pyrt.Equal(getD(m, "Connected GPU", gpuName), gpuName) {
						anyOther = true
						break
					}
				}
				if anyOther {
					gpuProps.Set("Compatibility", pair("", ""))
					if pyrt.Truthy(get(gpuProps, "OCLP Compatibility")) {
						gpuProps.Delete("OCLP Compatibility")
					}
				}
			}
		}

		if oc := get(gpuProps, "OCLP Compatibility"); pyrt.Truthy(oc) {
			l := oc.([]any)
			c.updateOCLP(l[0], l[len(l)-1])
		}
	}

	var compats []any
	for _, v := range values(get(report, "GPU")) {
		compats = append(compats, get(v, "Compatibility"))
	}
	gpuCompat := c.widestCompatibility(compats)

	if isNoneNone(gpuCompat) {
		c.println("")
		c.println("You cannot install macOS without a supported GPU.")
		c.println("Please do NOT spam my inbox or issue tracker about this issue anymore!")
		c.println("")
		c.requestInputExit()
	}
	c.restrictNativeCompatibility(gpuCompat)
}

func pyStrOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

// containsAny mirrors Utils.contains_any(data, search_item) truthiness.
func containsAny(data []string, search any) bool {
	s := str(search)
	for _, item := range data {
		if strings.Contains(strings.ToLower(s), strings.ToLower(item)) {
			return true
		}
	}
	return false
}

// CheckSoundCompatibility mirrors check_sound_compatibility.
func (c *Checker) CheckSoundCompatibility() (err error) {
	defer recoverErr(&err)
	c.checkSound()
	return nil
}

func (c *Checker) checkSound() {
	for _, p := range items(getD(c.hardwareReport, "Sound", emptyDict())) {
		props := asDict(p.Value)
		codecID := get(props, "Device ID")
		maxV, minV := "", ""

		ok := in("USB", get(props, "Bus Type"))
		if !ok {
			ok = strings.HasPrefix(str(codecID), "1002")
		}
		if !ok {
			ok = strings.HasPrefix(str(codecID), "8086") && !pyrt.InStrings(codecID, datasets.IntelSSTIDs)
		}
		if !ok {
			_, ok = datasets.CodecLayouts[str(codecID)]
		}
		if ok {
			maxV, minV = macos.LatestDarwinVersion(true), macos.LowestDarwinVersion()
		}
		props.Set("Compatibility", pair(maxV, minV))
		cp, _ := props.Get("Compatibility")
		c.status(p.Key, cp)

		endpoints := get(props, "Audio Endpoints")
		if pyrt.Truthy(endpoints) {
			n, err := pyrt.Len(endpoints)
			check(err)
			s := ""
			if n > 1 {
				s = "s"
			}
			c.printf("%s- Audio Endpoint%s: %s\n", "      ", s, joinStrings(endpoints))
		}
	}
}

func joinStrings(v any) string {
	l, ok := v.([]any)
	if !ok {
		if s, isStr := v.(string); isStr {
			parts := make([]string, 0, len(s))
			for _, r := range s {
				parts = append(parts, string(r))
			}
			return strings.Join(parts, ", ")
		}
		check(fmt.Errorf("%w: cannot join %T", pyrt.ErrType, v))
	}
	parts := make([]string, len(l))
	for i, e := range l {
		parts[i] = str(e)
	}
	return strings.Join(parts, ", ")
}

// CheckBiometricCompatibility mirrors check_biometric_compatibility.
func (c *Checker) CheckBiometricCompatibility() (err error) {
	defer recoverErr(&err)
	c.println("   \033[1;93mNote:\033[0m Biometric authentication in macOS requires Apple T2 Chip,")
	c.println("     which is not available for Hackintosh systems.")
	c.println("")
	for _, p := range items(getD(c.hardwareReport, "Biometric", emptyDict())) {
		props := asDict(p.Value)
		props.Set("Compatibility", pair("", ""))
		cp, _ := props.Get("Compatibility")
		c.status(p.Key, cp)
	}
	return nil
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// CheckNetworkCompatibility mirrors check_network_compatibility.
func (c *Checker) CheckNetworkCompatibility() (err error) {
	defer recoverErr(&err)
	c.checkNetwork()
	return nil
}

func (c *Checker) checkNetwork() {
	for _, p := range items(getD(c.hardwareReport, "Network", emptyDict())) {
		deviceName := p.Key
		props := asDict(p.Value)
		busType := get(props, "Bus Type")
		deviceID := get(props, "Device ID")

		maxV := macos.LatestDarwinVersion(true)
		minV := macos.LowestDarwinVersion()
		oclMax := macos.LatestDarwinVersion(false)
		oclMin := "20.0.0"

		ath := datasets.AtherosWiFiIDs
		if pyrt.InStrings(deviceID, datasets.BroadcomWiFiIDs) {
			idx := indexOf(datasets.BroadcomWiFiIDs, deviceID.(string))
			if idx == 13 || (17 < idx && idx < 21) {
				maxV = "22.99.99"
				oclMin = "23.0.0"
			} else if idx < 12 {
				maxV = "17.99.99"
			}
		} else if pyrt.InStrings(deviceID, ath[:8]) {
			maxV = "17.99.99"
		} else if pyrt.InStrings(deviceID, ath[8:]) {
			maxV = "20.99.99"
		} else if pyrt.InStrings(deviceID, datasets.IntelI22XIDs) {
			minV = "19.0.0"
		} else if pyrt.InStrings(deviceID, datasets.AquantiaAqtionIDs) {
			minV = "21.0.0"
		}

		if pyrt.InStrings(deviceID, datasets.EthernetIDs) || pyrt.InStrings(deviceID, datasets.WirelessUSBIDs) {
			props.Set("Compatibility", pair(maxV, minV))
		} else if pyrt.InStrings(deviceID, datasets.WirelessCardIDs) {
			if !pyrt.InStrings(deviceID, datasets.IntelWiFiIDs) && !pyrt.InStrings(deviceID, ath[8:]) && !pyrt.InStrings(deviceID, datasets.Rtw88WiFiIDs) {
				props.Set("OCLP Compatibility", pair(oclMax, oclMin))
				c.updateOCLP(oclMax, oclMin)
			}
			props.Set("Compatibility", pair(maxV, minV))
		}

		bt := str(busType)
		if strings.HasPrefix(bt, "PCI") && !pyrt.Truthy(get(props, "Compatibility")) {
			props.Set("Compatibility", pair("", ""))
		}

		cp, _ := props.Get("Compatibility")
		c.status(deviceName, cp)

		if pyrt.InStrings(deviceID, datasets.WirelessCardIDs) {
			if pyrt.InStrings(deviceID, datasets.BroadcomWiFiIDs) {
				c.printf("%s- Continuity Support: \033[1;32mFull\033[0m (AirDrop, Handoff, Universal Clipboard, Instant Hotspot,...)\n", "      ")
			} else if pyrt.InStrings(deviceID, datasets.IntelWiFiIDs) {
				c.printf("%s- Continuity Support: \033[1;33mPartial\033[0m (Handoff and Universal Clipboard with AirportItlwm)\n", "      ")
				c.printf("%s\033[1;93mNote:\033[0m AirDrop, Universal Clipboard, Instant Hotspot,... not available\n", "      ")
			} else if pyrt.InStrings(deviceID, ath) {
				c.printf("%s- Continuity Support: \033[1;31mLimited\033[0m (No Continuity features available)\n", "      ")
				c.printf("%s\033[1;93mNote:\033[0m Atheros cards are not recommended for macOS\n", "      ")
			}
		}

		if in("OCLP Compatibility", props) {
			oc, _ := props.Get("OCLP Compatibility")
			c.printf("%s- OCLP Compatibility: %s\n", "      ", c.showMacOSCompatibility(oc))
		}
	}
}

// CheckStorageCompatibility mirrors check_storage_compatibility.
func (c *Checker) CheckStorageCompatibility() (err error) {
	defer recoverErr(&err)
	c.checkStorage()
	return nil
}

func (c *Checker) checkStorage() {
	report := c.hardwareReport
	if !pyrt.Truthy(get(report, "Storage Controllers")) {
		c.println("")
		c.println("No storage controller found!")
		c.println("Please make sure to export the hardware report with the storage controller information")
		c.println("and try again.")
		c.println("")
		c.requestInputExit()
	}

	idx, err := pyrt.Index(report, "Storage Controllers")
	check(err)
	var controllers []pyrt.Pair
	for _, p := range items(idx) {
		if pyrt.Equal(get(p.Value, "Bus Type"), "PCI") && pyrt.Truthy(get(p.Value, "Disk Drives")) {
			controllers = append(controllers, p)
		}
	}

	for _, p := range controllers {
		props := asDict(p.Value)
		deviceID := get(props, "Device ID")
		subsystemID := getD(props, "Subsystem ID", strings.Repeat("0", 8))

		maxV := macos.LatestDarwinVersion(true)
		minV := macos.LowestDarwinVersion()

		if pyrt.InStrings(deviceID, datasets.IntelVMDIDs) {
			c.println("")
			c.println("Intel VMD controllers are not supported in macOS.")
			c.println("Please disable Intel VMD in the BIOS settings and try again with new hardware report.")
			c.println("")
			c.requestInputExit()
		}

		unsupported, found := datasets.LookupID(datasets.UnsupportedNVMeSSDIDs, deviceID)
		if found && pyrt.Truthy(unsupported.Value()) && in(subsystemID, unsupported.Value()) {
			maxV, minV = "", ""
		} else if pyrt.Equal(deviceID, "1C5C-174A") {
			minV = "20.0.0"
		}

		props.Set("Compatibility", pair(maxV, minV))
		cp, _ := props.Get("Compatibility")
		c.status(p.Key, cp)
	}

	var compats []any
	for _, p := range controllers {
		compats = append(compats, get(p.Value, "Compatibility"))
	}
	storageCompat := c.widestCompatibility(compats)

	if len(controllers) > 0 && isNoneNone(storageCompat) {
		c.println("")
		c.println("No compatible storage controller for macOS was found!")
		c.println("Consider purchasing a compatible SSD NVMe for your system.")
		c.println("Western Digital NVMe SSDs are generally recommended for good macOS compatibility.")
		c.println("")
		c.requestInputExit()
	}
	if len(controllers) > 0 {
		c.restrictNativeCompatibility(storageCompat)
	}
}

// CheckBluetoothCompatibility mirrors check_bluetooth_compatibility.
func (c *Checker) CheckBluetoothCompatibility() (err error) {
	defer recoverErr(&err)
	for _, p := range items(getD(c.hardwareReport, "Bluetooth", emptyDict())) {
		props := asDict(p.Value)
		deviceID := get(props, "Device ID")
		maxV := macos.LatestDarwinVersion(true)
		minV := macos.LowestDarwinVersion()

		if pyrt.InStrings(deviceID, datasets.AtherosBluetoothIDs) {
			maxV = "20.99.99"
		} else if pyrt.InStrings(deviceID, datasets.BluetoothIDs) {
			// pass
		} else {
			maxV, minV = "", ""
		}
		props.Set("Compatibility", pair(maxV, minV))
		cp, _ := props.Get("Compatibility")
		c.status(p.Key, cp)
	}
	return nil
}

// CheckSDControllerCompatibility mirrors check_sd_controller_compatibility.
func (c *Checker) CheckSDControllerCompatibility() (err error) {
	defer recoverErr(&err)
	for _, p := range items(getD(c.hardwareReport, "SD Controller", emptyDict())) {
		props := asDict(p.Value)
		deviceID := get(props, "Device ID")
		maxV := macos.LatestDarwinVersion(true)
		minV := macos.LowestDarwinVersion()

		if pyrt.InStrings(deviceID, datasets.RealtekCardReaderIDs) {
			if pyrt.InStrings(deviceID, datasets.RealtekCardReaderIDs[:5]) {
				maxV = "23.99.99"
			}
		} else {
			maxV, minV = "", ""
		}
		props.Set("Compatibility", pair(maxV, minV))
		cp, _ := props.Get("Compatibility")
		c.status(p.Key, cp)
	}
	return nil
}

// CheckCompatibility mirrors check_compatibility. report must be the
// *plist.Dict hardware report (it is modified in place and returned as the
// first value). The second value is (min_native, max_native) and the third is
// ocl_patched_macos_version as (max, min), or nil when Python holds None.
// A Python exit_program() surfaces as ErrExit.
func (c *Checker) CheckCompatibility(report any) (rep any, native [2]string, oclp *[2]string, err error) {
	defer recoverErr(&err)
	c.hardwareReport = asDict(report)
	c.oclpSet = false
	c.oclpMax, c.oclpMin = "", ""

	c.head("Compatibility Checker")
	c.println("")
	c.println("Checking compatibility with macOS for the following devices:")
	c.println("")

	steps := []struct {
		name string
		fn   func() error
	}{
		{"CPU", c.CheckCPUCompatibility},
		{"GPU", c.CheckGPUCompatibility},
		{"Sound", c.CheckSoundCompatibility},
		{"Biometric", c.CheckBiometricCompatibility},
		{"Network", c.CheckNetworkCompatibility},
		{"Storage Controllers", c.CheckStorageCompatibility},
		{"Bluetooth", c.CheckBluetoothCompatibility},
		{"SD Controller", c.CheckSDControllerCompatibility},
	}

	index := 0
	for _, s := range steps {
		if pyrt.Truthy(get(c.hardwareReport, s.name)) {
			index++
			c.printf("%d. %s:\n", index, s.name)
			time.Sleep(c.Delay)
			check(s.fn())
		}
	}

	c.println("")
	c.requestInput()

	if c.maxNative == "" || c.minNative == "" {
		check(fmt.Errorf("AttributeError: native macOS versions were not determined"))
	}
	native = [2]string{c.minNative, c.maxNative}
	if c.oclpSet {
		oclp = &[2]string{c.oclpMax, c.oclpMin}
	}
	return report, native, oclp, nil
}
