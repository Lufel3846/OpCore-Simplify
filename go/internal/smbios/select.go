// Package smbios ports the SMBIOS model selection of OpCore Simplify
// (Scripts/datasets/mac_model_data.py and SMBIOS.select_smbios_model in
// Scripts/smbios.py). Serial and UUID generation depend on the macserial
// binary and are not ported yet; see go/README.md.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2801). See go/LICENSE.
package smbios

import (
	"errors"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
)

// Motherboard is the motherboard section of the hardware report.
type Motherboard struct {
	Name     string
	Platform string
}

// CPU is the CPU section of the hardware report. CoreCount mirrors the
// int(...) conversion done by the original.
type CPU struct {
	Codename      string
	ProcessorName string
	CoreCount     int
}

// GPU is one entry of the report's GPU section.
type GPU struct {
	DeviceType string
}

// HardwareReport is the subset of the hardware report used for model selection.
// GPUs keeps the report order, which the original relies on through dict
// insertion order ("first" and "last" GPU).
type HardwareReport struct {
	Motherboard Motherboard
	CPU         CPU
	GPUs        []GPU
}

// ErrNoGPU is returned when the selection needs the first or last GPU and the
// report has none. The original fails with IndexError in the same case.
var ErrNoGPU = errors.New("hardware report has no GPU entries")

// SelectDefaultModel mirrors SMBIOS.select_smbios_model. Like the original, it
// evaluates the macOS version and the GPU list lazily, so an error is returned
// only when the Python code would have raised.
func SelectDefaultModel(report HardwareReport, macosVersion string) (string, error) {
	s := &selector{report: report, macosVersion: macosVersion}
	platform := report.Motherboard.Platform
	if strings.Contains(report.Motherboard.Name, "NUC") {
		platform = "NUC"
	}
	codename := report.CPU.Codename
	cores := report.CPU.CoreCount

	model := "MacBookPro16,2"
	if !strings.Contains(platform, "Laptop") {
		model = "MacPro7,1"
		if s.darwinBelow("25.0.0") {
			model = "iMacPro1,1"
		}
	}

	switch {
	case (codename == "Lynnfield" || codename == "Clarkdale") &&
		!strings.Contains(report.CPU.ProcessorName, "Xeon") &&
		s.darwinBelow("19.0.0"):
		model = "iMac11,2"
		if codename == "Lynnfield" {
			model = "iMac11,1"
		}
	case oneOf(codename, "Beckton", "Westmere-EX", "Gulftown", "Westmere-EP", "Clarkdale", "Lynnfield", "Jasper Forest", "Gainestown", "Bloomfield"):
		model = "MacPro6,1"
		if s.darwinBelow("19.0.0") {
			model = "MacPro5,1"
		}
	case (strings.Contains(codename, "Sandy Bridge") || strings.Contains(codename, "Ivy Bridge")) &&
		s.darwinBelow("22.0.0"):
		model = "MacPro6,1"
	}

	if platform != "Laptop" && s.lastGPU().DeviceType != "Integrated GPU" {
		return s.done(model)
	}

	switch {
	case oneOf(codename, "Arrandale", "Clarksfield"):
		model = "MacBookPro6,1"
	case strings.Contains(codename, "Sandy Bridge"):
		switch {
		case strings.Contains(platform, "Desktop"):
			model = "iMac12,2"
		case strings.Contains(platform, "NUC"):
			model = pick(cores < 4, "Macmini5,1", "Macmini5,3")
		case strings.Contains(platform, "Laptop"):
			model = pick(cores < 4, "MacBookPro8,1", "MacBookPro8,2")
		}
	case strings.Contains(codename, "Ivy Bridge"):
		switch {
		case strings.Contains(platform, "Desktop"):
			model = pick(s.firstIsIntegrated(), "iMac13,1", "iMac13,2")
		case strings.Contains(platform, "NUC"):
			model = pick(cores < 4, "Macmini6,1", "Macmini6,2")
		case strings.Contains(platform, "Laptop"):
			model = pick(cores < 4, "MacBookPro10,2", "MacBookPro10,1")
		}
	case strings.Contains(codename, "Haswell"):
		switch {
		case strings.Contains(platform, "Desktop"):
			model = pick(s.firstIsIntegrated(), "iMac14,4", "iMac15,1")
		case strings.Contains(platform, "NUC"):
			model = "Macmini7,1"
		case strings.Contains(platform, "Laptop"):
			model = pick(cores < 4, "MacBookPro11,1", "MacBookPro11,5")
		}
	case strings.Contains(codename, "Broadwell"):
		switch {
		case strings.Contains(platform, "Desktop"):
			model = pick(s.firstIsIntegrated(), "iMac16,2", "iMac17,1")
		case strings.Contains(platform, "NUC"):
			model = "iMac16,1"
		case strings.Contains(platform, "Laptop"):
			model = pick(cores < 4, "MacBookPro12,1", "MacBookPro11,5")
		}
	case strings.Contains(codename, "Skylake"):
		model = "iMac17,1"
		if strings.Contains(platform, "Laptop") {
			model = pick(cores < 4, "MacBookPro13,1", "MacBookPro13,3")
		}
	case strings.Contains(codename, "Amber Lake") || strings.Contains(codename, "Kaby Lake"):
		model = pick(s.firstIsIntegrated(), "iMac18,1", "iMac18,3")
		if strings.Contains(platform, "Laptop") {
			model = pick(cores < 4, "MacBookPro14,1", "MacBookPro14,3")
		}
	case containsAny(codename, "Cannon Lake", "Whiskey Lake", "Coffee Lake", "Comet Lake"):
		model = "Macmini8,1"
		if strings.Contains(platform, "Desktop") {
			model = pick(s.darwinBelow("18.0.0"), "iMac18,3", "iMac19,1")
			if strings.Contains(codename, "Comet Lake") {
				model = pick(cores < 10, "iMac20,1", "iMac20,2")
			}
		} else if strings.Contains(platform, "Laptop") {
			if strings.Contains(report.CPU.ProcessorName, "-8") {
				model = pick(cores < 6, "MacBookPro15,2", "MacBookPro15,3")
			} else {
				model = pick(cores < 6, "MacBookPro16,3", "MacBookPro16,1")
			}
		}
	case strings.Contains(codename, "Ice Lake"):
		model = "MacBookAir9,1"
	}

	return s.done(model)
}

// selector holds the lazy state of one selection. Errors are recorded when the
// original would have raised, and returned at the end.
type selector struct {
	report       HardwareReport
	macosVersion string
	err          error
}

func (s *selector) fail(err error) {
	if s.err == nil {
		s.err = err
	}
}

func (s *selector) done(model string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return model, nil
}

// darwinBelow reports whether the selected macOS version is lower than limit.
func (s *selector) darwinBelow(limit string) bool {
	current, err := macos.ParseVersion(s.macosVersion)
	if err != nil {
		s.fail(err)
		return false
	}
	return current.Compare(mustVersion(limit)) < 0
}

func (s *selector) firstGPU() GPU {
	if len(s.report.GPUs) == 0 {
		s.fail(ErrNoGPU)
		return GPU{}
	}
	return s.report.GPUs[0]
}

func (s *selector) lastGPU() GPU {
	if len(s.report.GPUs) == 0 {
		s.fail(ErrNoGPU)
		return GPU{}
	}
	return s.report.GPUs[len(s.report.GPUs)-1]
}

// firstIsIntegrated mirrors the original's substring test on the first GPU.
func (s *selector) firstIsIntegrated() bool {
	return strings.Contains(s.firstGPU().DeviceType, "Integrated GPU")
}

// mustVersion parses a constant Darwin version from this package. A failure is
// a programming error, so it panics.
func mustVersion(s string) macos.Version {
	v, err := macos.ParseVersion(s)
	if err != nil {
		panic("smbios: invalid static darwin version " + s + ": " + err.Error())
	}
	return v
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

// oneOf is Python's `codename in (...)` for exact matches.
func oneOf(codename string, options ...string) bool {
	for _, o := range options {
		if codename == o {
			return true
		}
	}
	return false
}

// containsAny is Python's `"A" in codename or "B" in codename ...` (substring match).
func containsAny(codename string, substrings ...string) bool {
	for _, sub := range substrings {
		if strings.Contains(codename, sub) {
			return true
		}
	}
	return false
}
