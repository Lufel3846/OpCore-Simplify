package config

import (
	"errors"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// newEmptyDict returns the {} default used by report.get(key, {}).
func newEmptyDict() *plist.Dict { return plist.NewDict() }

// gpuItems mirrors list(hardware_report.get("GPU").items()). The key has no
// default, so a report without GPU data fails, as in the original.
func gpuItems(report any) ([]pyrt.Pair, error) {
	gpuObj, err := pyrt.Get(report, "GPU")
	if err != nil {
		return nil, err
	}
	return pyrt.Items(gpuObj)
}

// firstGPU mirrors list(GPU.items())[0]; an empty GPU list is an IndexError.
func firstGPU(report any) (pyrt.Pair, error) {
	items, err := gpuItems(report)
	if err != nil {
		return pyrt.Pair{}, err
	}
	if len(items) == 0 {
		return pyrt.Pair{}, errors.New("IndexError: list index out of range")
	}
	return items[0], nil
}

// lastGPU mirrors list(GPU.items())[-1].
func lastGPU(report any) (pyrt.Pair, error) {
	items, err := gpuItems(report)
	if err != nil {
		return pyrt.Pair{}, err
	}
	if len(items) == 0 {
		return pyrt.Pair{}, errors.New("IndexError: list index out of range")
	}
	return items[len(items)-1], nil
}

// whateverGreenArgs appends the WhateverGreen boot arguments, mirroring the
// block of Prodigy.boot_args that runs when that kext is checked.
func whateverGreenArgs(args *[]string, report any, macosVersion string) error {
	monitorsObj, err := pyrt.GetDefault(report, "Monitor", newEmptyDict())
	if err != nil {
		return err
	}
	fourK, err := anyMonitor(monitorsObj, func(_ string, info any) (bool, error) {
		return resolutionAtLeast(info, "3840x2160")
	})
	if err != nil {
		return err
	}
	if fourK {
		below, err := darwinBelow(macosVersion, "20.0.0")
		if err != nil {
			return err
		}
		if below {
			*args = append(*args, "-cdfon")
		}
	}

	cpu, err := pyrt.Get(report, "CPU")
	if err != nil {
		return err
	}
	mfr, err := pyrt.Get(cpu, "Manufacturer")
	if err != nil {
		return err
	}
	intel, err := pyrt.In("Intel", mfr)
	if err != nil {
		return err
	}
	if intel {
		igpu, err := lastGPU(report)
		if err != nil {
			return err
		}
		igpuType, err := pyrt.Get(igpu.Value, "Device Type")
		if err != nil {
			return err
		}
		integrated, err := pyrt.In("Integrated GPU", igpuType)
		if err != nil {
			return err
		}
		if integrated {
			if err := integratedGPUArgs(args, report, igpu, macosVersion); err != nil {
				return err
			}
		}
	}

	discrete, err := firstGPU(report)
	if err != nil {
		return err
	}
	deviceType, err := pyrt.Get(discrete.Value, "Device Type")
	if err != nil {
		return err
	}
	if pyrt.Equal(deviceType, "Discrete GPU") {
		return discreteGPUArgs(args, discrete, macosVersion)
	}
	return nil
}

// integratedGPUArgs is the Intel integrated GPU part of the WhateverGreen block.
func integratedGPUArgs(args *[]string, report any, igpu pyrt.Pair, macosVersion string) error {
	compat, err := pyrt.Get(igpu.Value, "OCLP Compatibility")
	if err != nil {
		return err
	}
	if pyrt.Truthy(compat) {
		*args = append(*args, "ipc_control_port_options=0")
	}

	codename, err := pyrt.Get(igpu.Value, "Codename")
	if err != nil {
		return err
	}
	iceLake, err := pyrt.In("Ice Lake", codename)
	if err != nil {
		return err
	}
	if iceLake {
		*args = append(*args, "-noDC9", "-igfxblr")
	}

	board, err := pyrt.Get(report, "Motherboard")
	if err != nil {
		return err
	}
	platform, err := pyrt.Get(board, "Platform")
	if err != nil {
		return err
	}
	monitor, err := pyrt.GetDefault(report, "Monitor", newEmptyDict())
	if err != nil {
		return err
	}
	rawID, err := pyrt.Get(igpu.Value, "Device ID")
	if err != nil {
		return err
	}
	deviceID, err := pyrt.Str(rawID)
	if err != nil {
		return err
	}
	shortID := sliceRunes(deviceID, 5, len([]rune(deviceID)))

	desktop, err := pyrt.In("Desktop", platform)
	if err != nil {
		return err
	}
	if desktop {
		connected, err := anyMonitor(monitor, func(_ string, info any) (bool, error) {
			cg, err := pyrt.Get(info, "Connected GPU")
			if err != nil {
				return false, err
			}
			return pyrt.Equal(cg, igpu.Key), nil
		})
		if err != nil {
			return err
		}
		if connected && startsWithAny(shortID, "3E", "87", "9B") {
			atLeast, err := darwinAtLeast(macosVersion, "19.4.0")
			if err != nil {
				return err
			}
			if atLeast {
				*args = append(*args, "igfxonln=1")
			}
		}
		vesa, err := anyMonitor(monitor, func(_ string, info any) (bool, error) {
			cg, err := pyrt.Get(info, "Connected GPU")
			if err != nil {
				return false, err
			}
			if !pyrt.Equal(cg, igpu.Key) {
				return false, nil
			}
			ct, err := pyrt.Get(info, "Connector Type")
			if err != nil {
				return false, err
			}
			return inList(ct, "DVI", "HDMI"), nil
		})
		if err != nil {
			return err
		}
		if vesa {
			*args = append(*args, "-igfxvesa")
		}
		return nil
	}

	laptop, err := pyrt.In("Laptop", platform)
	if err != nil {
		return err
	}
	if laptop && startsWithAny(shortID, "59", "8C", "3E", "87", "9B") && !strings.HasSuffix(deviceID, "5917") {
		suffix := "r"
		atLeast, err := darwinAtLeast(macosVersion, "22.5.0")
		if err != nil {
			return err
		}
		if atLeast {
			suffix = "t"
		}
		*args = append(*args, "-igfxbl"+suffix)
	}
	return nil
}

// discreteGPUArgs is the discrete GPU part of the WhateverGreen block.
func discreteGPUArgs(args *[]string, gpu pyrt.Pair, macosVersion string) error {
	codename, err := pyrt.Get(gpu.Value, "Codename")
	if err != nil {
		return err
	}
	navi, err := pyrt.In("Navi", codename)
	if err != nil {
		return err
	}
	if navi {
		*args = append(*args, "agdpmod=pikera")
	}

	devID, err := pyrt.Get(gpu.Value, "Device ID")
	if err != nil {
		return err
	}
	if _, found := datasets.LookupID(datasets.SpoofGPUIDs, devID); found {
		*args = append(*args, "-radcodec")
	}
	if inList(devID, "1002-6610", "1002-682B", "1002-6837", "1002-683D", "1002-683F") {
		*args = append(*args, "radpg=15")
	}
	if inList(devID, "1002-67B0", "1002-67B1", "1002-67B8", "1002-6810", "1002-6811") {
		*args = append(*args, "-raddvi")
	}

	compat, err := pyrt.Get(gpu.Value, "OCLP Compatibility")
	if err != nil {
		return err
	}
	if !pyrt.Truthy(compat) {
		return nil
	}
	mfr, err := pyrt.Get(gpu.Value, "Manufacturer")
	if err != nil {
		return err
	}
	if pyrt.Equal(mfr, "AMD") {
		below, err := darwinBelow(macosVersion, "23.0.0")
		if err != nil {
			return err
		}
		if below {
			*args = append(*args, "-radvesa")
		} else {
			*args = append(*args, "-amd_no_dgpu_accel")
		}
		return nil
	}
	if pyrt.Equal(mfr, "NVIDIA") {
		kepler, err := pyrt.In("Kepler", codename)
		if err != nil {
			return err
		}
		if !kepler {
			*args = append(*args, "nvda_drv_vrl=1", "ngfxcompat=1", "ngfxgl=1")
		}
	}
	return nil
}
