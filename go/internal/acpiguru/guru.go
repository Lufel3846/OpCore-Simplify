// Package acpiguru ports Scripts/acpi_guru.py (ACPIGuru). This file holds the
// table loading, the patch selection and the helpers shared by the individual
// ACPI patches; each patch lives in its own file.
package acpiguru

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/datasets"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/dsdt"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// ErrExit stands for sys.exit(0) in utils.exit_program.
var ErrExit = errors.New("exit program")

// Patch mirrors acpi_patch_data.PatchInfo, including the checked flag.
type Patch struct {
	datasets.ACPIPatchInfo
	Checked bool
}

// Guru mirrors ACPIGuru.
type Guru struct {
	// Acpi is the DSDT helper (self.acpi).
	Acpi *dsdt.DSDT
	// Patches is self.patches, in the order of acpi_patch_data.
	Patches []*Patch

	HardwareReport  any
	DisabledDevices any
	ACPIDirectory   string
	SMBIOSModel     string
	// Dsdt is self.dsdt: the table the patches work on.
	Dsdt *dsdt.Table

	// RequestInput mirrors utils.request_input.
	RequestInput func(prompt string) (string, error)
	// Run mirrors Run.run for one command: stdout, stderr and the return code.
	Run func(args []string) (stdout, stderr string, returnCode int)
	// Log receives what the original prints. Nil discards it.
	Log io.Writer
}

// New returns a Guru with the patch list of acpi_patch_data, all unchecked.
func New(acpi *dsdt.DSDT, requestInput func(string) (string, error)) *Guru {
	g := &Guru{Acpi: acpi, RequestInput: requestInput}
	for _, p := range datasets.ACPIPatches {
		g.Patches = append(g.Patches, &Patch{ACPIPatchInfo: p})
	}
	g.Run = func(args []string) (string, string, int) { return acpi.Run("", args) }
	return g
}

func (g *Guru) logf(format string, a ...any) {
	if g.Log != nil {
		fmt.Fprintf(g.Log, format, a...)
	}
}

func (g *Guru) pause() error {
	_, err := g.RequestInput("Press Enter to continue...")
	return err
}

// ---- small Python helpers ---------------------------------------------------

// pyBasename is os.path.basename (posix).
func pyBasename(p string) string { return p[strings.LastIndex(p, "/")+1:] }

// pyDirname is os.path.dirname (posix).
func pyDirname(p string) string {
	i := strings.LastIndex(p, "/") + 1
	head := p[:i]
	if head != "" && head != strings.Repeat("/", len(head)) {
		head = strings.TrimRight(head, "/")
	}
	return head
}

func pyIsSpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

func pyStrip(s string) string { return strings.TrimFunc(s, pyIsSpace) }

// pySliceStrings is l[lo:hi] for a list of strings; hi < 0 or hi >= len clamp
// like Python (use hi = len for an open end).
func pySliceStrings(l []string, lo, hi int) []string {
	n := len(l)
	clamp := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		} else if i > n {
			i = n
		}
		return i
	}
	lo, hi = clamp(lo), clamp(hi)
	if lo >= hi {
		return nil
	}
	return l[lo:hi]
}

func hasString(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// HexToBytes mirrors utils.hex_to_bytes: the decoded bytes, or the input string
// when it has characters other than hex digits and whitespace or an odd number
// of digits. v must be a string; anything else raises TypeError in Python.
func HexToBytes(v any) (any, error) {
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("%w: expected string or bytes-like object, got %T", pyrt.ErrType, v)
	}
	var digits strings.Builder
	nonSpace := 0
	for _, r := range s {
		if !pyIsSpace(r) {
			nonSpace++
		}
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	if nonSpace != len(d) {
		return s, nil
	}
	if len(d)%2 != 0 {
		return s, nil
	}
	b := make([]byte, len(d)/2)
	for i := range b {
		n, _ := strconv.ParseUint(d[2*i:2*i+2], 16, 8)
		b[i] = byte(n)
	}
	return b, nil
}

type nicelyToken struct {
	num  *big.Int
	text string
}

var digitsSplit = regexp.MustCompile(`([0-9]+)`)

func nicelyKey(s string) []nicelyToken {
	s = strings.ToLower(s)
	var out []nicelyToken
	last := 0
	for _, m := range digitsSplit.FindAllStringIndex(s, -1) {
		out = append(out, nicelyToken{text: s[last:m[0]]})
		n, _ := new(big.Int).SetString(s[m[0]:m[1]], 10)
		out = append(out, nicelyToken{num: n})
		last = m[1]
	}
	out = append(out, nicelyToken{text: s[last:]})
	return out
}

func nicelyLess(a, b []nicelyToken) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		x, y := a[i], b[i]
		if x.num != nil && y.num != nil {
			if c := x.num.Cmp(y.num); c != 0 {
				return c < 0
			}
			continue
		}
		if x.text != y.text {
			return x.text < y.text
		}
	}
	return len(a) < len(b)
}

// SortedNicely mirrors sorted_nicely: natural ordering, case-insensitive.
// Digits are the ASCII digits only (Python's isdigit also accepts others).
func SortedNicely(l []string) []string {
	out := append([]string{}, l...)
	keys := map[string][]nicelyToken{}
	for _, s := range out {
		keys[s] = nicelyKey(s)
	}
	sort.SliceStable(out, func(i, j int) bool { return nicelyLess(keys[out[i]], keys[out[j]]) })
	return out
}

// GetUniqueName mirrors get_unique_name.
func GetUniqueName(name, targetFolder, nameAppend string) string {
	name = pyBasename(name)
	ext := ""
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		ext = parts[len(parts)-1]
	}
	if ext != "" {
		name = name[:len(name)-len(ext)-1]
	}
	if nameAppend != "" {
		name += nameAppend
	}
	check := name
	if ext != "" {
		check = name + "." + ext
	}
	if !pathExists(filepath.Join(targetFolder, check)) {
		return check
	}
	for num := 1; ; num++ {
		check = fmt.Sprintf("%s-%d", name, num)
		if ext != "" {
			check += "." + ext
		}
		if !pathExists(filepath.Join(targetFolder, check)) {
			return check
		}
	}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// GetUniqueDevice mirrors get_unique_device. The path argument of the original
// is unused and omitted.
func (g *Guru) GetUniqueDevice(baseName string, startingNumber int, usedNames []string) (string, int) {
	for {
		hexNum := strings.ToUpper(strconv.FormatInt(int64(startingNumber), 16))
		cut := len(baseName) - len(hexNum)
		if cut < 0 {
			cut = 0
		}
		name := baseName[:cut] + hexNum
		if len(g.Acpi.GetDevicePaths("."+name, nil)) == 0 && !hasString(usedNames, name) {
			return name, startingNumber
		}
		startingNumber++
	}
}

// GetData mirrors get_data: str is encoded as UTF-8, then padded with zero bytes.
func GetData(data any, padTo int) ([]byte, error) {
	var b []byte
	switch x := data.(type) {
	case string:
		b = []byte(x)
	case []byte:
		b = append([]byte{}, x...)
	default:
		return nil, fmt.Errorf("%w: cannot encode %T", pyrt.ErrAttribute, data)
	}
	if n := padTo - len(b); n > 0 {
		b = append(b, make([]byte, n)...)
	}
	return b, nil
}

// ---- loading tables ---------------------------------------------------------

// ReadACPITables mirrors read_acpi_tables. It returns the folder that holds the
// tables, or ok=false where the original returns None.
//
// The original compares _table_signature(...) (bytes) with the str "DSDT", which
// is never true in Python 3. The "pre-patch" branch for a DSDT that does not
// disassemble is therefore unreachable and is not ported: such a table is
// reported as not disassembled, exactly as the original does.
func (g *Guru) ReadACPITables(path string) (string, bool, error) {
	if path == "" {
		return "", false, nil
	}
	g.logf("Loading ACPI Table(s)\nby CorpNewt\n\n")
	var tables []string
	prior := g.Acpi.AcpiTables
	g.Acpi.AcpiTables = dsdt.NewTables()
	restore := func() { g.Acpi.AcpiTables = prior }

	st, statErr := os.Stat(path)
	isDir := statErr == nil && st.IsDir()
	isFile := statErr == nil && st.Mode().IsRegular()

	switch {
	case isDir:
		g.logf("Gathering valid tables from %s...\n\n", pyBasename(path))
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", false, err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		for _, t := range SortedNicely(names) {
			if strings.Contains(t, "Patched") {
				continue
			}
			ok, err := g.Acpi.TableIsValid(path, t)
			if err != nil {
				return "", false, err
			}
			if ok {
				g.logf(" - %s\n", t)
				tables = append(tables, t)
			}
		}
		if len(tables) == 0 {
			if st, err := os.Stat(filepath.Join(path, "ACPI")); err == nil && st.IsDir() {
				return g.ReadACPITables(filepath.Join(path, "ACPI"))
			}
			g.logf(" - No valid .aml files were found!\n\n")
			if err := g.pause(); err != nil {
				return "", false, err
			}
			restore()
			return "", false, nil
		}
		g.logf("\n")
	case !strings.Contains(path, "Patched") && isFile:
		g.logf("Loading %s...\n", pyBasename(path))
		if loaded, _ := g.Acpi.Load(path); loaded.Len() > 0 {
			g.logf("\nDone.\n")
			return pyDirname(path), true, nil
		}
		g.logf("\n%s could not be disassembled!\n\n", pyBasename(path))
		if err := g.pause(); err != nil {
			return "", false, err
		}
		restore()
		return "", false, nil
	default:
		g.logf("Passed file/folder does not exist!\n\n")
		if err := g.pause(); err != nil {
			return "", false, err
		}
		restore()
		return "", false, nil
	}

	if len(tables) > 1 {
		g.logf("Loading valid tables in %s...\n", path)
	}
	loaded, failed := g.Acpi.Load(path)
	if loaded.Len() == 0 || len(failed) > 0 {
		where := path
		if isFile {
			where = pyDirname(path)
		}
		colon := ""
		if len(failed) > 0 {
			colon = ":"
		}
		g.logf("\nFailed to load tables in %s%s\n\n", where, colon)
		for _, t := range SortedNicely(failed) {
			g.logf(" - %s\n", t)
		}
		if loaded.Len() == 0 {
			restore()
		}
	} else {
		if len(tables) > 1 {
			g.logf("\n")
		}
		g.logf("Done.\n")
	}
	g.Dsdt = g.Acpi.GetDSDTOrOnly()
	return path, true, nil
}

func (g *Guru) ensured(allowAny bool) bool {
	if g.Dsdt == nil {
		return false
	}
	if allowAny && g.Acpi.AcpiTables.Len() > 0 {
		return true
	}
	return !allowAny && g.Acpi.GetDSDTOrOnly() != nil
}

// EnsureDSDT mirrors ensure_dsdt: it asks for the tables when none are loaded.
func (g *Guru) EnsureDSDT(allowAny bool) (bool, error) {
	if g.ensured(allowAny) {
		return true, nil
	}
	if _, _, err := g.SelectACPITables(); err != nil {
		return false, err
	}
	g.Dsdt = g.Acpi.GetDSDTOrOnly()
	return g.ensured(allowAny), nil
}

// SelectACPITables mirrors select_acpi_tables.
func (g *Guru) SelectACPITables() (string, bool, error) {
	for {
		g.logf("\nQ. Quit\n \n")
		menu, err := g.RequestInput("Please drag and drop ACPI Tables folder here: ")
		if err != nil {
			return "", false, err
		}
		if strings.ToLower(menu) == "q" {
			return "", false, g.ExitProgram()
		}
		path, err := NormalizePath(menu)
		if err != nil {
			return "", false, err
		}
		if path == "" {
			continue
		}
		return g.ReadACPITables(path)
	}
}

// ExitProgram mirrors utils.exit_program: it prints the farewell, waits for
// Enter and exits. It always returns ErrExit unless reading the input fails.
func (g *Guru) ExitProgram() error {
	const width = 68
	center := func(s string) string {
		pad := width - len([]rune(s))
		if pad <= 0 {
			return s
		}
		// str.center puts the extra space on the left when the padding is odd
		// and the width is odd; for even total padding both sides are equal.
		left := pad/2 + (pad & width & 1)
		return strings.Repeat(" ", left) + s + strings.Repeat(" ", pad-left)
	}
	g.logf("\n%s\n\n", center("For more information, to report errors, or to contribute to the product:"))
	sep := strings.Repeat("─", width-4)
	g.logf(" ┌%s┐ \n", sep)
	for _, c := range [][2]string{
		{"Facebook", "https://www.facebook.com/macforce2601"},
		{"Telegram", "https://t.me/lzhoang2601"},
		{"GitHub", "https://github.com/lzhoang2801/OpCore-Simplify"},
	} {
		line := " * " + c[0] + ": " + c[1]
		pad := width - 4 - len([]rune(line))
		if pad < 0 {
			pad = 0
		}
		g.logf(" │%s%s│ \n", line, strings.Repeat(" ", pad))
	}
	g.logf(" └%s┘ \n\n%s\n\n", sep, center("Thank you for using our program!"))
	if _, err := g.RequestInput(center("Press Enter to exit.")); err != nil {
		return err
	}
	return ErrExit
}

// ---- table-dropping and patch helpers ---------------------------------------

// WriteSSDT mirrors write_ssdt. With compile it runs iasl on the .dsl file,
// removes it on success and reports whether the .aml exists.
func (g *Guru) WriteSSDT(name, content string, compile bool) (bool, error) {
	dslPath := filepath.Join(g.ACPIDirectory, name+".dsl")
	amlPath := filepath.Join(g.ACPIDirectory, name+".aml")
	if !pathExists(g.ACPIDirectory) {
		if err := os.MkdirAll(g.ACPIDirectory, 0o777); err != nil {
			return false, err
		}
	}
	if err := os.WriteFile(dslPath, []byte(content), 0o666); err != nil {
		return false, err
	}
	if !compile {
		return false, nil
	}
	if _, _, rc := g.Run([]string{g.Acpi.Iasl, dslPath}); rc != 0 {
		return false, nil
	}
	if err := os.Remove(dslPath); err != nil {
		return false, err
	}
	return pathExists(amlPath), nil
}

// ApplyACPIPatches mirrors apply_acpi_patches: it fills the defaults, converts
// the hex strings to bytes and sorts by Comment. Each patch is a *plist.Dict.
// Sorting supports all-string or all-integer comments; mixed types are the
// TypeError Python raises.
func (g *Guru) ApplyACPIPatches(acpiPatches []any) ([]any, error) {
	out := make([]*plist.Dict, 0, len(acpiPatches))
	for _, p := range acpiPatches {
		get := func(key string, def any) (any, error) { return pyrt.GetDefault(p, key, def) }
		d := plist.NewDict()
		for _, f := range []struct {
			key string
			def any
			hex bool
			req bool
		}{
			{"Base", "", false, false}, {"BaseSkip", int64(0), false, false},
			{"Comment", "", false, false}, {"Count", int64(0), false, false},
			{"Enabled", true, false, false},
			{"Find", nil, true, true}, {"Limit", int64(0), false, false},
			{"Mask", "", true, false}, {"OemTableId", "", true, false},
			{"Replace", nil, true, true}, {"ReplaceMask", "", true, false},
			{"Skip", int64(0), false, false}, {"TableLength", int64(0), false, false},
			{"TableSignature", "", true, false},
		} {
			if f.key == "Enabled" {
				d.Set("Enabled", true)
				continue
			}
			var v any
			var err error
			if f.req {
				v, err = pyrt.Index(p, f.key)
			} else {
				v, err = get(f.key, f.def)
			}
			if err != nil {
				return nil, err
			}
			if f.hex {
				if v, err = HexToBytes(v); err != nil {
					return nil, err
				}
			}
			d.Set(f.key, v)
		}
		out = append(out, d)
	}
	if len(out) > 1 {
		allStr, allInt := true, true
		for _, d := range out {
			c, _ := d.Get("Comment")
			if _, ok := c.(string); !ok {
				allStr = false
			}
			if _, ok := c.(int64); !ok {
				allInt = false
			}
		}
		if !allStr && !allInt {
			return nil, fmt.Errorf("%w: '<' not supported between instances of different types", pyrt.ErrType)
		}
		sort.SliceStable(out, func(i, j int) bool {
			a, _ := out[i].Get("Comment")
			b, _ := out[j].Get("Comment")
			if allStr {
				return a.(string) < b.(string)
			}
			return a.(int64) < b.(int64)
		})
	}
	res := make([]any, len(out))
	for i, d := range out {
		res[i] = d
	}
	return res, nil
}

// DroppingTheTable mirrors dropping_the_table. Nil arguments are Python's None;
// the result is nil when no table matches.
func (g *Guru) DroppingTheTable(signature, oemTableID []byte) (*plist.Dict, error) {
	var t *dsdt.Table
	if signature != nil {
		t = g.Acpi.GetTableWithSignature(signature)
	}
	if t == nil && oemTableID != nil {
		t = g.Acpi.GetTableWithID(oemTableID)
	}
	if t == nil {
		return nil, nil
	}
	name := oemTableID
	if len(signature) > 0 {
		name = signature
	}
	if name == nil {
		return nil, fmt.Errorf("%w: 'NoneType' object has no attribute 'rstrip'", pyrt.ErrAttribute)
	}
	trimmed := strings.TrimRight(string(name), "\x00")
	if !isValidUTF8(trimmed) {
		return nil, errors.New("UnicodeDecodeError: 'utf-8' codec can't decode")
	}
	d := plist.NewDict()
	d.Set("All", true)
	d.Set("Comment", "Delete "+trimmed)
	d.Set("Enabled", true)
	d.Set("OemTableId", append([]byte{}, t.ID...))
	d.Set("TableLength", int64(t.Length))
	d.Set("TableSignature", append([]byte{}, t.Signature...))
	return d, nil
}

func isValidUTF8(s string) bool { return utf8.ValidString(s) }

// DropCPUTables mirrors drop_cpu_tables.
func (g *Guru) DropCPUTables() (*plist.Dict, error) {
	deletes := []any{}
	for _, name := range []string{"CpuPm", "Cpu0Ist"} {
		padded, err := GetData(name, 8)
		if err != nil {
			return nil, err
		}
		entry, err := g.DroppingTheTable(nil, padded)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			deletes = append(deletes, entry)
		}
	}
	d := plist.NewDict()
	d.Set("Delete", deletes)
	return d, nil
}

// GetPatchIndex mirrors get_patch_index.
func (g *Guru) GetPatchIndex(name string) (int, bool) {
	for i, p := range g.Patches {
		if p.Name == name {
			return i, true
		}
	}
	return 0, false
}
