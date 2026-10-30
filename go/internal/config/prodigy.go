package config

import (
	"bufio"
	"errors"
	"math/rand"
	"os"
	"strings"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/gathering"
)

// Remote patch lists read by get_kernel_patches. They are the same URLs as in
// gatheringFiles.__init__.
const (
	AMDVanillaPatchesURL  = "https://raw.githubusercontent.com/laobamac/AMD_Vanilla/refs/heads/master/patches.plist"
	AquantiaPatchesURL    = "https://raw.githubusercontent.com/CaseySJ/Aquantia-macOS-Patches/refs/heads/main/CaseySJ-Aquantia-Patch-Sets-1-and-2.plist"
	HyperThreadingPatches = "https://github.com/b00t0x/CpuTopologyRebuild/raw/refs/heads/master/patches_ht.plist"
)

// Prodigy mirrors ConfigProdigy. Its hooks replace the parts of the original
// that depend on the terminal, on randomness, on the network or on macserial.
type Prodigy struct {
	// RequestInput answers the codec layout prompt.
	RequestInput func(prompt string) (string, error)
	// Choose mirrors random.choice: it returns the index of the default layout.
	Choose func(n int) int
	// KernelPatches mirrors gatheringFiles.get_kernel_patches. It never fails;
	// on error it returns an empty list, as the original does.
	KernelPatches func(name, url string) []any
	// GenerateSMBIOS mirrors SMBIOS.generate_smbios (serial, MLB, ROM, UUID).
	GenerateSMBIOS    func(model string) (map[string]any, error)
	AMDVanillaURL     string
	AquantiaURL       string
	HyperThreadingURL string
}

// NewProdigy returns a Prodigy that reads the kernel patches through the
// gatherer. Serial generation is not configured until GenerateSMBIOS is set.
func NewProdigy(g *gathering.Gatherer) *Prodigy {
	return &Prodigy{
		RequestInput:      readLine,
		Choose:            rand.Intn,
		KernelPatches:     g.GetKernelPatches,
		AMDVanillaURL:     AMDVanillaPatchesURL,
		AquantiaURL:       AquantiaPatchesURL,
		HyperThreadingURL: HyperThreadingPatches,
	}
}

func readLine(prompt string) (string, error) {
	os.Stdout.WriteString(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("EOFError: EOF when reading a line")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (p *Prodigy) ask(prompt string) (string, error) {
	if p.RequestInput == nil {
		return "", errors.New("no input source configured")
	}
	return p.RequestInput(prompt)
}

// kextChecked mirrors kexts[kext_data.kext_index_by_name.get(name)].checked.
func kextChecked(kexts []*datasets.KextInfo, name string) (bool, error) {
	idx, ok := datasets.KextIndexByName[name]
	if !ok || idx < 0 || idx >= len(kexts) {
		return false, errors.New("TypeError: kext index is None")
	}
	return kexts[idx].Checked, nil
}

// kextByName returns the catalog entry of name.
func kextByName(kexts []*datasets.KextInfo, name string) (*datasets.KextInfo, error) {
	idx, ok := datasets.KextIndexByName[name]
	if !ok || idx < 0 || idx >= len(kexts) {
		return nil, errors.New("TypeError: kext index is None")
	}
	return kexts[idx], nil
}
