// Package validator ports Scripts/report_validator.py: it checks a hardware
// report (Report.json) against the schema and returns the cleaned data, the
// errors and the warnings, with the original messages.
package validator

import (
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/fsutil"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyjson"
)

// Kind is the Python type a rule expects.
type Kind int

const (
	KindDict Kind = iota
	KindStr
	KindList
)

func (k Kind) name() string {
	switch k {
	case KindDict:
		return "dict"
	case KindStr:
		return "str"
	}
	return "list"
}

// Field is one key of a rule's "schema", in order.
type Field struct {
	Key  string
	Rule *Rule
}

// Rule mirrors one schema node. HasRequired false means the default (required).
type Rule struct {
	Type        Kind
	HasRequired bool
	Required    bool
	HasPattern  bool
	Pattern     string
	Schema      []Field
	ValuesRule  *Rule
	ItemRule    *Rule
}

// Validator mirrors ReportValidator: Errors and Warnings hold the result of the
// last ValidateReport call.
type Validator struct {
	Errors   []string
	Warnings []string
}

// New returns a Validator.
func New() *Validator { return &Validator{} }

var (
	patternMu    sync.Mutex
	patternCache = map[string]*regexp.Regexp{}
)

// compilePattern turns a Python pattern into an anchored-at-start Go regexp, as
// re.match uses it: "\d" is any Unicode decimal digit and "$" also matches
// before one trailing newline.
func compilePattern(p string) *regexp.Regexp {
	patternMu.Lock()
	defer patternMu.Unlock()
	if re, ok := patternCache[p]; ok {
		return re
	}
	var b strings.Builder
	b.WriteString(`\A(?:`)
	runes := []rune(p)
	inClass := false
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && i+1 < len(runes):
			if runes[i+1] == 'd' {
				b.WriteString(`\p{Nd}`)
			} else {
				b.WriteRune(r)
				b.WriteRune(runes[i+1])
			}
			i++
		case r == '[':
			inClass = true
			b.WriteRune(r)
		case r == ']':
			inClass = false
			b.WriteRune(r)
		case r == '$' && !inClass:
			b.WriteString(`(?:\n?\z)`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(`)`)
	re := regexp.MustCompile(b.String())
	patternCache[p] = re
	return re
}

func reMatch(pattern, s string) bool { return compilePattern(pattern).MatchString(s) }

// typeName is type(data).__name__.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case int, int64, *big.Int:
		return "int"
	case float64:
		return "float"
	case string:
		return "str"
	case []any:
		return "list"
	case *plist.Dict:
		return "dict"
	case []byte:
		return "bytes"
	}
	return fmt.Sprintf("%T", v)
}

func isKind(v any, k Kind) bool {
	switch k {
	case KindDict:
		_, ok := v.(*plist.Dict)
		return ok
	case KindStr:
		_, ok := v.(string)
		return ok
	}
	_, ok := v.([]any)
	return ok
}

// ValidateReport mirrors validate_report. The cleaned data is nil where the
// original returns None.
func (v *Validator) ValidateReport(reportPath string) (isValid bool, errs, warns []string, cleaned any) {
	v.Errors = []string{}
	v.Warnings = []string{}
	if _, err := os.Stat(reportPath); err != nil {
		v.Errors = append(v.Errors, "File does not exist: "+reportPath)
		return false, v.Errors, v.Warnings, nil
	}
	data, err := fsutil.ReadFile(reportPath)
	if err != nil {
		var de *pyjson.DecodeError
		if errors.As(err, &de) {
			v.Errors = append(v.Errors, "Invalid JSON format: "+err.Error())
		} else {
			v.Errors = append(v.Errors, "Error reading file: "+exceptionText(err, reportPath))
		}
		return false, v.Errors, v.Warnings, nil
	}
	cleaned, _ = v.validateNode(data, Schema, "Root")
	return len(v.Errors) == 0, v.Errors, v.Warnings, cleaned
}

// validateNode mirrors _validate_node; ok is false where it returns None.
func (v *Validator) validateNode(data any, rule *Rule, path string) (any, bool) {
	if !isKind(data, rule.Type) {
		v.Errors = append(v.Errors, fmt.Sprintf("%s: Expected type %s, got %s", path, rule.Type.name(), typeName(data)))
		return nil, false
	}
	if s, isStr := data.(string); isStr {
		if rule.HasPattern {
			if !reMatch(rule.Pattern, s) {
				v.Errors = append(v.Errors, fmt.Sprintf("%s: Value '%s' does not match pattern '%s'", path, s, rule.Pattern))
				return nil, false
			}
		} else if !reMatch(NotEmptyPattern, s) {
			v.Errors = append(v.Errors, fmt.Sprintf("%s: Value '%s' does not match pattern '%s'", path, s, NotEmptyPattern))
			return nil, false
		}
	}

	cleaned := data
	switch d := data.(type) {
	case *plist.Dict:
		out := plist.NewDict()
		fields := map[string]*Rule{}
		for _, f := range rule.Schema {
			fields[f.Key] = f.Rule
		}
		for _, key := range d.Keys() {
			value, _ := d.Get(key)
			if fr, ok := fields[key]; ok {
				if cv, ok := v.validateNode(value, fr, path+"."+key); ok {
					out.Set(key, cv)
				}
			} else if rule.ValuesRule != nil {
				if cv, ok := v.validateNode(value, rule.ValuesRule, path+"."+key); ok {
					out.Set(key, cv)
				}
			} else if len(rule.Schema) > 0 {
				v.Warnings = append(v.Warnings, fmt.Sprintf("%s: Unknown key '%s'", path, key))
			}
		}
		for _, f := range rule.Schema {
			required := true
			if f.Rule.HasRequired {
				required = f.Rule.Required
			}
			if _, found := out.Get(f.Key); required && !found {
				v.Errors = append(v.Errors, fmt.Sprintf("%s: Missing required key '%s'", path, f.Key))
			}
		}
		cleaned = out
	case []any:
		if rule.ItemRule != nil {
			items := []any{}
			for i, item := range d {
				if cv, ok := v.validateNode(item, rule.ItemRule, fmt.Sprintf("%s[%d]", path, i)); ok {
					items = append(items, cv)
				}
			}
			cleaned = items
		} else {
			cleaned = append([]any{}, d...)
		}
	}
	return cleaned, true
}

// ShowValidationReport mirrors show_validation_report (without the screen
// header that utils.head draws).
func ShowValidationReport(w io.Writer, reportPath string, isValid bool, errs, warns []string) {
	fmt.Fprintf(w, "\nValidation report for: %s\n\n", reportPath)
	if isValid {
		fmt.Fprintln(w, "Hardware report is valid!")
	} else {
		fmt.Fprintln(w, "Hardware report is not valid! Please check the errors and warnings below.")
	}
	if len(errs) > 0 {
		fmt.Fprintf(w, "\n\033[31mErrors (%d):\033[0m\n", len(errs))
		for i, e := range errs {
			fmt.Fprintf(w, "    %d. %s\n", i+1, e)
		}
	}
	if len(warns) > 0 {
		fmt.Fprintf(w, "\n\033[33mWarnings (%d):\033[0m\n", len(warns))
		for i, e := range warns {
			fmt.Fprintf(w, "    %d. %s\n", i+1, e)
		}
	}
}

// exceptionText is str(e) for the errors read_file can raise: Python's
// OSError text for file-system errors, the message itself otherwise.
func exceptionText(err error, path string) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		var errno syscall.Errno
		if errors.As(pe.Err, &errno) {
			return fmt.Sprintf("[Errno %d] %s: %s", int(errno), strerror(errno), pyRepr(path))
		}
	}
	return err.Error()
}

func strerror(e syscall.Errno) string {
	s := e.Error()
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// pyRepr is repr(str) for a path.
func pyRepr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case !unicode.IsPrint(r):
			if r < 0x100 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else if r < 0x10000 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				fmt.Fprintf(&b, `\U%08x`, r)
			}
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}
