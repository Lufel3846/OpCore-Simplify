// Package dsdt ports Scripts/dsdt.py: it loads ACPI tables (disassembled with
// iasl) and answers the text/hex/path queries that acpi_guru.py is built on.
package dsdt

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Table mirrors one entry of DSDT.acpi_tables.
type Table struct {
	// Name is the key under which the table is stored (the .aml file name).
	Name   string
	Raw    []byte
	Table  string
	Lines  []string
	Scopes []Scope
	Paths  []Path

	Signature    []byte
	OEM          []byte
	ID           []byte
	Revision     int
	OEMRevision  *big.Int
	Length       int
	SignatureAsc *string // "signature_ascii", set only when unprintable bytes exist
	OEMAsc       *string
	IDAsc        *string
}

// Scope is a (line, index) tuple of get_scopes.
type Scope struct {
	Line  string
	Index int
}

// Path is a (path, line index, type) tuple of get_paths.
type Path struct {
	Path  string
	Index int
	Type  string
}

// Device is a (device line, device index, matched index) tuple of get_devices.
// HasDevice is false where the original has None (a hit before any device).
type Device struct {
	Line        string
	HasDevice   bool
	DeviceIndex int
	Index       int
}

// Tables is the insertion-ordered dict DSDT.acpi_tables.
type Tables struct {
	order []string
	m     map[string]*Table
}

// NewTables returns an empty table dict.
func NewTables() *Tables { return &Tables{m: map[string]*Table{}} }

// Set assigns a table; an existing key keeps its position, as a dict does.
func (t *Tables) Set(name string, tbl *Table) {
	if _, ok := t.m[name]; !ok {
		t.order = append(t.order, name)
	}
	t.m[name] = tbl
}

// Get returns the table stored under name.
func (t *Tables) Get(name string) (*Table, bool) {
	v, ok := t.m[name]
	return v, ok
}

// Pop removes a key, if present.
func (t *Tables) Pop(name string) {
	if _, ok := t.m[name]; !ok {
		return
	}
	delete(t.m, name)
	for i, n := range t.order {
		if n == name {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
}

// Len is len(acpi_tables).
func (t *Tables) Len() int { return len(t.order) }

// Names lists the keys in insertion order.
func (t *Tables) Names() []string { return append([]string{}, t.order...) }

// Values lists the tables in insertion order.
func (t *Tables) Values() []*Table {
	out := make([]*Table, 0, len(t.order))
	for _, n := range t.order {
		out = append(out, t.m[n])
	}
	return out
}

// Direction selects where get_unique_pad grows the pattern.
type Direction int

const (
	Both     Direction = iota // direction=None
	Backward                  // direction=False
	Forward                   // direction=True
)

// DSDT mirrors the DSDT class.
type DSDT struct {
	// Iasl is the path of the iasl executable.
	Iasl string
	// Run executes a command in dir. A missing command returns
	// ("", "Command not found!", 1), as Run.run does.
	Run func(dir string, args []string) (stdout, stderr string, returnCode int)
	// Log receives what the original prints. Nil discards it.
	Log io.Writer
	// AcpiTables is acpi_tables. Callers may swap the pointer (the original
	// keeps the prior dict and restores it on failure).
	AcpiTables *Tables

	allowedSignatures [][]byte
	mixedListing      [][]byte
	hexMatch          *regexp.Regexp
	typeMatch         *regexp.Regexp
}

// pySpace is the set of characters for which Python's str.isspace is true.
const pySpace = `\t\n\v\f\r \x1c-\x1f\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`

// New returns a DSDT that uses iasl at the given path.
func New(iasl string) *DSDT {
	sp := `[` + pySpace + `]`
	return &DSDT{
		Iasl:              iasl,
		Run:               runCommand,
		AcpiTables:        NewTables(),
		allowedSignatures: [][]byte{[]byte("APIC"), []byte("DMAR"), []byte("DSDT"), []byte("SSDT")},
		mixedListing:      [][]byte{[]byte("DSDT"), []byte("SSDT")},
		hexMatch:          regexp.MustCompile(`^` + sp + `*[0-9A-F]{4,}:(` + sp + `[0-9A-F]{2})+(` + sp + `+//.*)?$`),
		typeMatch:         regexp.MustCompile(`^.*(Processor|Scope|Device|Method|Name) \(([^,\)]+).*`),
	}
}

// IaslFinder mirrors the lookup part of check_iasl.
type IaslFinder struct {
	// ScriptDir is the folder that holds the iasl executables (Scripts/).
	ScriptDir string
	// Platform is Python's sys.platform: "win32", "darwin" or "linux".
	Platform string
	// Download mirrors _download_and_extract: it fetches iasl into ScriptDir.
	// Nil means the download step is unavailable and a missing iasl stays missing.
	Download func(legacy bool) error
	Log      io.Writer
}

// Check mirrors check_iasl(legacy, try_downloading=True).
func (f IaslFinder) Check(legacy bool) string {
	return f.check(legacy, true)
}

func (f IaslFinder) check(legacy, tryDownloading bool) string {
	var targets []string
	if f.Platform == "win32" {
		name := "iasl.exe"
		if legacy {
			name = "iasl-legacy.exe"
		}
		targets = []string{filepath.Join(f.ScriptDir, name)}
	} else if legacy {
		targets = []string{filepath.Join(f.ScriptDir, "iasl-legacy")}
	} else {
		targets = []string{
			filepath.Join(f.ScriptDir, "iasl-dev"),
			filepath.Join(f.ScriptDir, "iasl-stable"),
			filepath.Join(f.ScriptDir, "iasl"),
		}
	}
	for _, t := range targets {
		if _, err := os.Stat(t); err == nil {
			return t
		}
	}
	if !tryDownloading {
		return ""
	}
	if f.Download != nil {
		if err := f.Download(legacy); err != nil && f.Log != nil {
			fmt.Fprintf(f.Log, "An error occurred :(\n - %v\n", err)
		}
	}
	return f.check(legacy, false)
}

// MissingIaslError builds the exception text of DSDT.__init__ when iasl is not found.
func MissingIaslError(scriptDir, platform string) error {
	msg := "Could not locate or download iasl!"
	url := ""
	switch {
	case platform == "win32":
		url = "https://github.com/acpica/acpica/releases"
	case platform == "darwin":
		url = "https://raw.githubusercontent.com/acidanthera/MaciASL/master/Dist/iasl-stable"
	case strings.HasPrefix(platform, "linux"):
		url = "https://raw.githubusercontent.com/corpnewt/linux_iasl/main/iasl.zip"
	}
	if url != "" {
		what := "iasl"
		if platform == "win32" {
			what = `"iasl-win-YYYYMMDD.zip" and extract iasl.exe`
		}
		msg += fmt.Sprintf("\n\nPlease manually download %s from:\n - %s\n\nAnd place in:\n - %s\n", what, url, scriptDir)
	}
	return errors.New(msg)
}

func runCommand(dir string, args []string) (string, string, int) {
	if len(args) == 0 {
		return "", "Command not found!", 1
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return toUTF8(stdout.Bytes()), toUTF8(stderr.Bytes()), ee.ExitCode()
		}
		return "", "Command not found!", 1
	}
	return toUTF8(stdout.Bytes()), toUTF8(stderr.Bytes()), 0
}

// toUTF8 mirrors bytes.decode("utf-8", "ignore").
func toUTF8(b []byte) string {
	return strings.ToValidUTF8(string(b), "")
}

func (d *DSDT) logf(format string, a ...any) {
	if d.Log != nil {
		fmt.Fprintf(d.Log, format, a...)
	}
}

// pyJoin is os.path.join for two parts: an absolute second part wins.
func pyJoin(a, b string) string {
	if filepath.IsAbs(b) {
		return b
	}
	if a == "" {
		return b
	}
	if strings.HasSuffix(a, "/") {
		return a + b
	}
	return a + "/" + b
}

func pyIsSpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

// pyStrip is str.strip().
func pyStrip(s string) string { return strings.TrimFunc(s, pyIsSpace) }

// pyRstrip is str.rstrip().
func pyRstrip(s string) string { return strings.TrimRightFunc(s, pyIsSpace) }

// TableSignature mirrors _table_signature. ok is false where the original
// returns None (not a file); err is an exception the original lets propagate.
func (d *DSDT) TableSignature(tablePath, tableName string) (sig []byte, ok bool, err error) {
	path := tablePath
	if tableName != "" {
		path = pyJoin(tablePath, tableName)
	}
	st, serr := os.Stat(path)
	if serr != nil || !st.Mode().IsRegular() {
		return nil, false, nil
	}
	f, oerr := os.Open(path)
	if oerr != nil {
		return nil, false, oerr
	}
	defer f.Close()
	buf := make([]byte, 4)
	n, _ := io.ReadFull(f, buf)
	return buf[:n], true, nil
}

// TableIsValid mirrors table_is_valid.
func (d *DSDT) TableIsValid(tablePath, tableName string) (bool, error) {
	sig, ok, err := d.TableSignature(tablePath, tableName)
	if err != nil || !ok {
		return false, err
	}
	return inList(d.allowedSignatures, sig), nil
}

func inList(list [][]byte, v []byte) bool {
	for _, x := range list {
		if bytes.Equal(x, v) {
			return true
		}
	}
	return false
}

// GetASCIIPrint mirrors get_ascii_print.
func GetASCIIPrint(data []byte) (bool, string) {
	unprintables := false
	var sb strings.Builder
	for _, b := range data {
		if ' ' <= b && b < '~' {
			sb.WriteByte(b)
		} else {
			sb.WriteByte('?')
			unprintables = true
		}
	}
	return unprintables, sb.String()
}

func exists(folder, name string) bool {
	st, err := os.Stat(pyJoin(folder, name))
	return err == nil && st.Mode().IsRegular() && st.Size() > 0
}

func copyFile(src, dstDir string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dstDir, filepath.Base(src)), data, st.Mode().Perm())
}

type target struct {
	name   string
	disasm string
}

// Load mirrors DSDT.load: it disassembles the table (or every valid table of a
// directory) and adds the result to AcpiTables. It returns the newly loaded
// tables and the names that failed to disassemble. Errors are reported through
// Log and yield an empty result, as in the original.
func (d *DSDT) Load(tablePath string) (*Tables, []string) {
	loaded, failed, err := d.load(tablePath)
	if err != nil {
		d.logf("%v\n", err)
		return NewTables(), failed
	}
	for _, name := range loaded.order {
		d.AcpiTables.Set(name, loaded.m[name])
	}
	return loaded, failed
}

func (d *DSDT) load(tablePath string) (*Tables, []string, error) {
	var failed []string
	var validFiles []string
	st, serr := os.Stat(tablePath)
	switch {
	case serr == nil && st.IsDir():
		entries, err := os.ReadDir(tablePath)
		if err != nil {
			return nil, failed, err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, x := range names {
			ok, err := d.TableIsValid(tablePath, x)
			if err != nil {
				return nil, failed, err
			}
			if ok {
				validFiles = append(validFiles, x)
			}
		}
	case serr == nil && st.Mode().IsRegular():
		validFiles = []string{tablePath}
	default:
		return nil, failed, fmt.Errorf("[Errno 2] No such file or directory: '%s'", tablePath)
	}
	if len(validFiles) == 0 {
		return nil, failed, fmt.Errorf("[Errno 2] No such file or directory: 'No valid .aml/.dat files found at %s'", tablePath)
	}
	temp, err := os.MkdirTemp("", "")
	if err != nil {
		return nil, failed, err
	}
	defer os.RemoveAll(temp)
	for _, file := range validFiles {
		if err := copyFile(pyJoin(tablePath, file), temp); err != nil {
			return nil, failed, err
		}
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		return nil, failed, err
	}
	listDir := make([]string, 0, len(entries))
	for _, e := range entries {
		listDir = append(listDir, e.Name())
	}
	sort.Strings(listDir)

	var targets []*target
	for _, x := range listDir {
		if len(listDir) > 1 {
			ok, err := d.TableIsValid(temp, x)
			if err != nil {
				return nil, failed, err
			}
			if !ok {
				continue
			}
		}
		var nameExt []string
		for _, y := range strings.Split(filepath.Base(x), ".") {
			if y != "" {
				nameExt = append(nameExt, y)
			}
		}
		if len(nameExt) > 0 {
			ext := strings.ToLower(nameExt[len(nameExt)-1])
			if ext == "asl" || ext == "dsl" {
				continue
			}
		}
		parts := strings.Split(x, ".")
		targets = append(targets, &target{
			name:   x,
			disasm: strings.Join(parts[:len(parts)-1], ".") + ".dsl",
		})
	}
	if len(targets) == 0 {
		return nil, failed, fmt.Errorf("[Errno 2] No such file or directory: 'No valid .aml/.dat files found at %s'", tablePath)
	}

	var dsdtOrSsdt, otherTables []string
	byName := map[string]*target{}
	for _, t := range targets {
		byName[t.name] = t
	}
	for _, t := range targets {
		sig, _, err := d.TableSignature(temp, t.name)
		if err != nil {
			return nil, failed, err
		}
		if inList(d.mixedListing, sig) {
			dsdtOrSsdt = append(dsdtOrSsdt, t.name)
		}
	}
	for _, t := range targets {
		found := false
		for _, n := range dsdtOrSsdt {
			if n == t.name {
				found = true
				break
			}
		}
		if !found {
			otherTables = append(otherTables, t.name)
		}
	}

	if len(dsdtOrSsdt) > 0 {
		args := append([]string{d.Iasl, "-da", "-dl", "-l"}, dsdtOrSsdt...)
		_, _, rc := d.Run(temp, args)
		if rc != 0 {
			args = append([]string{d.Iasl, "-dl", "-l"}, dsdtOrSsdt...)
			d.Run(temp, args)
		}
		var failTemp []string
		for _, x := range dsdtOrSsdt {
			if !exists(temp, byName[x].disasm) {
				failTemp = append(failTemp, x)
			}
		}
		for _, x := range failTemp {
			d.Run(temp, []string{d.Iasl, "-dl", "-l", x})
			if !exists(temp, byName[x].disasm) {
				failed = append(failed, x)
			}
		}
	}
	if len(otherTables) > 0 {
		args := append([]string{d.Iasl}, otherTables...)
		d.Run(temp, args)
		for _, x := range otherTables {
			if !exists(temp, byName[x].disasm) {
				failed = append(failed, x)
			}
		}
	}
	if len(failed) == len(targets) {
		return nil, failed, fmt.Errorf("Failed to disassemble - %s", strings.Join(failed, ", "))
	}

	result := NewTables()
	for _, t := range targets {
		if !exists(temp, t.disasm) {
			continue
		}
		tbl, err := d.readTable(temp, t)
		if err != nil {
			return nil, failed, err
		}
		result.Set(t.name, tbl)
	}
	return result, failed, nil
}

// readTable builds the table dict for one disassembled file.
func (d *DSDT) readTable(temp string, t *target) (*Table, error) {
	rawText, err := os.ReadFile(filepath.Join(temp, t.disasm))
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(rawText) {
		return nil, errors.New("'utf-8' codec can't decode bytes: invalid utf-8")
	}
	// open(..., "r") translates \r\n and \r into \n.
	text := strings.ReplaceAll(string(rawText), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	tbl := &Table{Name: t.name}
	if strings.HasPrefix(text, "/*") {
		parts := strings.Split(text, "*/")
		text = pyStrip(strings.Join(parts[1:], "*/"))
	}
	for _, h := range []string{"\nTable Header:", "\nRaw Table Data: Length"} {
		if strings.Contains(text, h) {
			parts := strings.Split(text, h)
			text = pyRstrip(strings.Join(parts[:len(parts)-1], h))
			break
		}
	}
	tbl.Table = text
	tbl.Lines = strings.Split(text, "\n")
	tbl.Scopes = d.GetScopes(tbl)
	tbl.Paths = d.GetPaths(tbl)

	raw, err := os.ReadFile(filepath.Join(temp, t.name))
	if err != nil {
		return nil, err
	}
	if len(raw) < 9 {
		return nil, errors.New("index out of range")
	}
	tbl.Raw = raw
	tbl.Signature = raw[0:4]
	tbl.Revision = int(raw[8])
	tbl.OEM = raw[10:min(16, len(raw))]
	tbl.ID = raw[16:min(24, len(raw))]
	if len(raw) <= 24 {
		return nil, errors.New("invalid literal for int() with base 16: ''")
	}
	rev := raw[24:min(28, len(raw))]
	be := make([]byte, len(rev))
	for i := range rev {
		be[len(rev)-1-i] = rev[i]
	}
	tbl.OEMRevision = new(big.Int).SetBytes(be)
	tbl.Length = len(raw)
	if u, s := GetASCIIPrint(tbl.Signature); u {
		tbl.SignatureAsc = &s
	}
	if u, s := GetASCIIPrint(tbl.OEM); u {
		tbl.OEMAsc = &s
	}
	if u, s := GetASCIIPrint(tbl.ID); u {
		tbl.IDAsc = &s
	}

	// The disassembler omits the last line of hex data in a mixed listing.
	lastHex := ""
	found := false
	for i := len(tbl.Lines) - 1; i >= 0; i-- {
		if d.IsHex(tbl.Lines[i]) {
			lastHex = tbl.Lines[i]
			found = true
			break
		}
	}
	if found {
		colon := strings.Split(lastHex, ":")
		addr, ok := new(big.Int).SetString(pyStrip(colon[0]), 16)
		if !ok {
			return nil, errors.New("invalid literal for int() with base 16")
		}
		hexs := pyStrip(strings.Split(colon[1], "//")[0])
		nextAddr := new(big.Int).Add(addr, big.NewInt(int64(len(strings.Fields(hexs)))))
		hexb, err := hex.DecodeString(strings.ReplaceAll(hexs, " ", ""))
		if err != nil {
			return nil, err
		}
		pieces := bytes.Split(tbl.Raw, hexb)
		remaining := pieces[len(pieces)-1]
		for i := 0; i < len(remaining); i += 16 {
			chunk := remaining[i:min(i+16, len(remaining))]
			hs := strings.ToUpper(hex.EncodeToString(chunk))
			var pairs []string
			for j := 0; j < len(hs); j += 2 {
				pairs = append(pairs, hs[j:j+2])
			}
			a := strings.ToUpper(nextAddr.Text(16))
			for len(a) < 4 {
				a = "0" + a
			}
			l := "   " + a + ": " + strings.Join(pairs, " ")
			nextAddr.Add(nextAddr, big.NewInt(int64(len(chunk))))
			tbl.Lines = append(tbl.Lines, l)
			tbl.Table += "\n" + l
		}
	}
	return tbl, nil
}

// GetHexFromInt mirrors get_hex_from_int.
func GetHexFromInt(total int64, padTo int) string {
	var s string
	if total < 0 {
		// hex(-5) is "-0x5"; the original slices off two characters and upper-cases the rest.
		s = "X" + strings.ToUpper(strconv.FormatInt(-total, 16))
	} else {
		s = strings.ToUpper(strconv.FormatInt(total, 16))
	}
	for len(s) < padTo {
		s = "0" + s
	}
	var chunks []string
	for i := 0; i < len(s); i += 2 {
		chunks = append(chunks, s[i:min(i+2, len(s))])
	}
	for i, j := 0, len(chunks)-1; i < j; i, j = i+1, j-1 {
		chunks[i], chunks[j] = chunks[j], chunks[i]
	}
	return strings.Join(chunks, "")
}

// GetHex mirrors get_hex.
func GetHex(line string) string {
	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		panic("IndexError: list index out of range")
	}
	return strings.ReplaceAll(strings.Split(parts[1], "//")[0], " ", "")
}

// GetLine mirrors get_line.
func GetLine(line string) string {
	line = strings.Split(line, "//")[0]
	if strings.Contains(line, ":") {
		return strings.Split(line, ":")[1]
	}
	return line
}

// GetHexBytes mirrors get_hex_bytes (binascii.unhexlify).
func GetHexBytes(line string) ([]byte, error) { return hex.DecodeString(line) }

// GetTableWithID mirrors get_table_with_id.
func (d *DSDT) GetTableWithID(id []byte) *Table {
	for _, v := range d.AcpiTables.Values() {
		if bytes.Equal(id, v.ID) {
			return v
		}
	}
	return nil
}

// GetTableWithSignature mirrors get_table_with_signature.
func (d *DSDT) GetTableWithSignature(sig []byte) *Table {
	for _, v := range d.AcpiTables.Values() {
		if bytes.Equal(sig, v.Signature) {
			return v
		}
	}
	return nil
}

// GetTable mirrors get_table.
func (d *DSDT) GetTable(idOrSig []byte) *Table {
	for _, v := range d.AcpiTables.Values() {
		if bytes.Equal(idOrSig, v.Signature) || bytes.Equal(idOrSig, v.ID) {
			return v
		}
	}
	return nil
}

// GetDSDT mirrors get_dsdt.
func (d *DSDT) GetDSDT() *Table { return d.GetTableWithSignature([]byte("DSDT")) }

// GetDSDTOrOnly mirrors get_dsdt_or_only.
func (d *DSDT) GetDSDTOrOnly() *Table {
	if t := d.GetDSDT(); t != nil {
		return t
	}
	if d.AcpiTables.Len() != 1 {
		return nil
	}
	return d.AcpiTables.Values()[0]
}

func (d *DSDT) tableOrDefault(t *Table) *Table {
	if t != nil {
		return t
	}
	return d.GetDSDTOrOnly()
}

// IsHex mirrors is_hex.
func (d *DSDT) IsHex(line string) bool { return d.hexMatch.MatchString(line) }

// pyClampStart is the start of lines[start:].
func pyClampStart(n, start int) int {
	if start < 0 {
		start += n
		if start < 0 {
			start = 0
		}
	} else if start > n {
		start = n
	}
	return start
}

// pyReverseFrom lists the indexes visited by lines[start::-1].
func pyReverseFrom(n, start int) []int {
	if start < 0 {
		start += n
		if start < 0 {
			return nil
		}
	}
	if start >= n {
		start = n - 1
	}
	var out []int
	for i := start; i >= 0; i-- {
		out = append(out, i)
	}
	return out
}

// FindPreviousHex mirrors find_previous_hex. It returns (hex, start, end).
func (d *DSDT) FindPreviousHex(index int, table *Table) (string, int, int) {
	table = d.tableOrDefault(table)
	if table == nil {
		return "", -1, -1
	}
	startIndex, endIndex := -1, -1
	oldHex := true
	for i, li := range pyReverseFrom(len(table.Lines), index) {
		line := table.Lines[li]
		if oldHex {
			if !d.IsHex(line) {
				oldHex = false
			}
			continue
		}
		if d.IsHex(line) {
			endIndex = index - i
			hexText, s := d.GetHexEndingAt(endIndex, table)
			return hexText, s, endIndex
		}
	}
	return "", startIndex, endIndex
}

// FindNextHex mirrors find_next_hex. It returns (hex, start, end).
func (d *DSDT) FindNextHex(index int, table *Table) (string, int, int) {
	table = d.tableOrDefault(table)
	if table == nil {
		return "", -1, -1
	}
	startIndex, endIndex := -1, -1
	oldHex := true
	lines := table.Lines[pyClampStart(len(table.Lines), index):]
	for i, line := range lines {
		if oldHex {
			if !d.IsHex(line) {
				oldHex = false
			}
			continue
		}
		if d.IsHex(line) {
			startIndex = i + index
			hexText, e := d.GetHexStartingAt(startIndex, table)
			return hexText, startIndex, e
		}
	}
	return "", startIndex, endIndex
}

// GetHexStartingAt mirrors get_hex_starting_at. It returns (hex, last index).
func (d *DSDT) GetHexStartingAt(startIndex int, table *Table) (string, int) {
	table = d.tableOrDefault(table)
	if table == nil {
		return "", -1
	}
	hexText := ""
	index := -1
	lines := table.Lines[pyClampStart(len(table.Lines), startIndex):]
	for i, x := range lines {
		if !d.IsHex(x) {
			break
		}
		hexText += GetHex(x)
		index = i + startIndex
	}
	return hexText, index
}

// GetHexEndingAt mirrors get_hex_ending_at. It returns (hex, first index).
func (d *DSDT) GetHexEndingAt(startIndex int, table *Table) (string, int) {
	table = d.tableOrDefault(table)
	if table == nil {
		return "", -1
	}
	hexText := ""
	index := -1
	for i, li := range pyReverseFrom(len(table.Lines), startIndex) {
		x := table.Lines[li]
		if !d.IsHex(x) {
			break
		}
		hexText = GetHex(x) + hexText
		index = startIndex - i
	}
	return hexText, index
}

// Pad is the (left, right) result of the unique-pad searches.
type Pad struct{ Left, Right string }

// GetShortestUniquePad mirrors get_shortest_unique_pad. A nil result with a nil
// error is the original's None (no table).
func (d *DSDT) GetShortestUniquePad(currentHex string, index, instance int, table *Table) (*Pad, error) {
	table = d.tableOrDefault(table)
	if table == nil {
		return nil, nil
	}
	var best *Pad
	var any bool
	for _, dir := range []Direction{Backward, Forward, Both} {
		l, r, err := d.GetUniquePad(currentHex, index, dir, instance, table)
		if err != nil {
			continue
		}
		any = true
		p := Pad{l, r}
		if best == nil || len(p.Left+p.Right) < len(best.Left+best.Right) {
			cp := p
			best = &cp
		}
	}
	if !any {
		return nil, errors.New("No unique pad found!")
	}
	return best, nil
}

func pySlice(s []string, lo, hi int) []string {
	n := len(s)
	lo, hi = pyClampStart(n, lo), pyClampStart(n, hi)
	if lo >= hi {
		return nil
	}
	return s[lo:hi]
}

// GetUniquePad mirrors get_unique_pad.
func (d *DSDT) GetUniquePad(currentHex string, index int, direction Direction, instance int, table *Table) (string, string, error) {
	table = d.tableOrDefault(table)
	if table == nil {
		return "", "", errors.New("No valid table passed!")
	}
	startIndex := index
	line, lastIndex := d.GetHexStartingAt(index, table)
	if lastIndex == -1 {
		return "", "", fmt.Errorf("Could not find hex starting at index %d!", index)
	}
	firstLine := line
	for {
		if strings.Contains(line, currentHex) || len(line) >= len(firstLine)+len(currentHex) {
			break
		}
		newLine, _, li := d.FindNextHex(lastIndex, table)
		lastIndex = li
		if lastIndex == -1 {
			return "", "", errors.New("Hit end of file before passed hex was located!")
		}
		line += newLine
	}
	if !strings.Contains(line, currentHex) {
		return "", "", fmt.Errorf("%s not found in table at index %d-%d!", currentHex, startIndex, lastIndex)
	}
	if currentHex == "" {
		return "", "", errors.New("empty separator")
	}
	var padl, padr string
	parts := strings.Split(line, currentHex)
	if instance >= len(parts)-1 {
		return "", "", errors.New("Instance out of range!")
	}
	linel := strings.Join(pySlice(parts, 0, instance+1), currentHex)
	liner := strings.Join(pySlice(parts, instance+1, len(parts)), currentHex)
	for {
		check, err := GetHexBytes(padl + currentHex + padr)
		if err != nil {
			return "", "", err
		}
		if bytes.Count(table.Raw, check) == 1 {
			break
		}
		if direction == Forward || (direction == Both && len(padr) <= len(padl)) {
			if len(liner) == 0 {
				var li int
				liner, _, li = d.FindNextHex(lastIndex, table)
				lastIndex = li
				if lastIndex == -1 {
					return "", "", errors.New("Hit end of file before unique hex was found!")
				}
			}
			padr += liner[:min(2, len(liner))]
			liner = liner[min(2, len(liner)):]
			continue
		}
		if direction == Backward || (direction == Both && len(padl) <= len(padr)) {
			if len(linel) == 0 {
				var ei int
				linel, startIndex, ei = d.FindPreviousHex(startIndex, table)
				if ei == -1 {
					return "", "", errors.New("Hit end of file before unique hex was found!")
				}
			}
			cut := max(0, len(linel)-2)
			padl = linel[cut:] + padl
			linel = linel[:cut]
			continue
		}
		break
	}
	return padl, padr, nil
}

// GetDevices mirrors get_devices. The original's search=None (which returns an
// empty list) is not representable; callers always pass a string.
func (d *DSDT) GetDevices(search string, types []string, stripComments bool, table *Table) []Device {
	table = d.tableOrDefault(table)
	if table == nil {
		return nil
	}
	var last string
	hasLast := false
	deviceIndex := 0
	var devices []Device
	for index, line := range table.Lines {
		if d.IsHex(line) {
			continue
		}
		if stripComments {
			line = GetLine(line)
		}
		for _, x := range types {
			if strings.Contains(line, x) {
				last = line
				hasLast = true
				deviceIndex = index
				break
			}
		}
		if strings.Contains(line, search) {
			devices = append(devices, Device{Line: last, HasDevice: hasLast, DeviceIndex: deviceIndex, Index: index})
		}
	}
	return devices
}

// DefaultDeviceTypes is the default of get_devices(types=...).
var DefaultDeviceTypes = []string{"Device (", "Scope ("}

// GetScope mirrors get_scope.
func (d *DSDT) GetScope(startingIndex int, addHex, stripComments bool, table *Table) []string {
	table = d.tableOrDefault(table)
	if table == nil {
		return nil
	}
	brackets := 0
	started := false
	var scope []string
	for _, line := range table.Lines[pyClampStart(len(table.Lines), startingIndex):] {
		if d.IsHex(line) {
			if addHex {
				scope = append(scope, line)
			}
			continue
		}
		if stripComments {
			line = GetLine(line)
		}
		scope = append(scope, line)
		if !started {
			if c := strings.Count(line, "{"); c != 0 {
				brackets = c
				started = true
			}
			continue
		}
		brackets = brackets + strings.Count(line, "{") - strings.Count(line, "}")
		if brackets <= 0 {
			return scope
		}
	}
	return scope
}

// GetScopes mirrors get_scopes.
func (d *DSDT) GetScopes(table *Table) []Scope {
	table = d.tableOrDefault(table)
	if table == nil {
		return nil
	}
	var scopes []Scope
	for index, line := range table.Lines {
		if d.IsHex(line) {
			continue
		}
		for _, x := range []string{"Processor (", "Scope (", "Device (", "Method (", "Name ("} {
			if strings.Contains(line, x) {
				scopes = append(scopes, Scope{line, index})
				break
			}
		}
	}
	return scopes
}

type pathEntry struct {
	name     string
	brackets int
}

// GetPaths mirrors get_paths.
func (d *DSDT) GetPaths(table *Table) []Path {
	table = d.tableOrDefault(table)
	if table == nil {
		return nil
	}
	var pathList []Path
	var cur []pathEntry
	brackets := 0
	for i, line := range table.Lines {
		if d.IsHex(line) {
			continue
		}
		line = GetLine(line)
		brackets += strings.Count(line, "{") - strings.Count(line, "}")
		for len(cur) > 0 {
			if cur[len(cur)-1].brackets >= brackets {
				cur = cur[:len(cur)-1]
			} else {
				break
			}
		}
		m := d.typeMatch.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		typ, name := m[1], m[2]
		cur = append(cur, pathEntry{name, brackets})
		if typ == "Scope" {
			continue
		}
		var path []string
		for j := len(cur) - 1; j >= 0; j-- {
			p := cur[j].name
			path = append(path, p)
			check := strings.TrimRight(strings.Split(p, ".")[0], "_")
			if strings.HasPrefix(check, `\`) || check == "_SB" || check == "_PR" {
				break
			}
		}
		for a, b := 0, len(path)-1; a < b; a, b = a+1, b-1 {
			path[a], path[b] = path[b], path[a]
		}
		parts := strings.Split(strings.Join(path, "."), ".")
		if len(parts) > 0 && parts[0] == `\` {
			parts = parts[1:]
		}
		hasCaret := false
		for _, x := range parts {
			if strings.Contains(x, "^") {
				hasCaret = true
				break
			}
		}
		if hasCaret {
			var np []string
			for _, x := range parts {
				if c := strings.Count(x, "^"); c > 0 {
					np = np[:max(0, len(np)-c)]
				}
				np = append(np, strings.ReplaceAll(x, "^", ""))
			}
			parts = np
		}
		if len(parts) == 0 {
			continue
		}
		padded := make([]string, len(parts))
		for j, x := range parts {
			prefix := ""
			if j == 0 {
				prefix = `\`
			}
			padded[j] = prefix + strings.TrimRight(strings.TrimLeft(x, `\`), "_")
		}
		pathList = append(pathList, Path{strings.Join(padded, "."), i, typ})
	}
	sortPaths(pathList)
	return pathList
}

func sortPaths(p []Path) {
	sort.SliceStable(p, func(a, b int) bool {
		if p[a].Path != p[b].Path {
			return p[a].Path < p[b].Path
		}
		if p[a].Index != p[b].Index {
			return p[a].Index < p[b].Index
		}
		return p[a].Type < p[b].Type
	})
}

func normalizePath(p string) string {
	parts := strings.Split(p, ".")
	for i, x := range parts {
		parts[i] = strings.ToUpper(strings.TrimRight(x, "_"))
	}
	return strings.Join(parts, ".")
}

// GetPathOfType mirrors get_path_of_type. An empty objType means None.
func (d *DSDT) GetPathOfType(objType, obj string, table *Table) []Path {
	table = d.tableOrDefault(table)
	if table == nil {
		return nil
	}
	var paths []Path
	obj = normalizePath(obj)
	objType = strings.ToLower(objType)
	for _, p := range table.Paths {
		check := normalizePath(p.Path)
		if (objType != "" && objType != strings.ToLower(p.Type)) || !strings.HasSuffix(check, obj) {
			continue
		}
		paths = append(paths, p)
	}
	sortPaths(paths)
	return paths
}

// GetDevicePaths mirrors get_device_paths.
func (d *DSDT) GetDevicePaths(obj string, table *Table) []Path {
	return d.GetPathOfType("Device", obj, table)
}

// GetMethodPaths mirrors get_method_paths.
func (d *DSDT) GetMethodPaths(obj string, table *Table) []Path {
	return d.GetPathOfType("Method", obj, table)
}

// GetNamePaths mirrors get_name_paths.
func (d *DSDT) GetNamePaths(obj string, table *Table) []Path {
	return d.GetPathOfType("Name", obj, table)
}

// GetProcessorPaths mirrors get_processor_paths(obj_type, obj="").
func (d *DSDT) GetProcessorPaths(objType string, table *Table) []Path {
	return d.GetPathOfType(objType, "", table)
}

// GetDevicePathsWithHID mirrors get_device_paths_with_hid.
func (d *DSDT) GetDevicePathsWithHID(hid string, table *Table) []Path {
	table = d.tableOrDefault(table)
	if table == nil {
		return nil
	}
	var devs []string
	upper := strings.ToUpper(hid)
	for _, p := range table.Paths {
		if !strings.HasSuffix(p.Path, "._HID") {
			continue
		}
		// An index outside the lines is the original's bare `except: continue`.
		if p.Index < 0 || p.Index >= len(table.Lines) {
			continue
		}
		if strings.Contains(table.Lines[p.Index], upper) {
			devs = append(devs, strings.TrimSuffix(p.Path, "._HID"))
		}
	}
	var devices []Path
	for _, p := range table.Paths {
		if p.Type != "Device" {
			continue
		}
		for _, dv := range devs {
			if dv == p.Path {
				devices = append(devices, p)
				break
			}
		}
	}
	return devices
}
