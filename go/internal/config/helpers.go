// Package config ports Scripts/config_prodigy.py: it turns the OpenCore sample
// config and the hardware report into the config.plist of the EFI.
//
// Points that depend on something outside this package are injected through
// Prodigy: the interactive codec layout prompt, the random choice of the
// default layout, the remote kernel patch lists, and the SMBIOS serial
// generator (macserial). Terminal output is not reproduced.
//
// OpCore Simplify is distributed under the BSD 3-Clause License
// (Copyright (c) 2024, lzhoang2601). See go/LICENSE.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/macos"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
	"github.com/Lufel3846/OpCore-Simplify/go/internal/pyrt"
)

// ErrSerialNotPorted is returned when the SMBIOS serial generator is not
// configured. The original runs macserial, which is not ported yet.
var ErrSerialNotPorted = errors.New("SMBIOS serial generation (macserial) is not ported yet")

// index mirrors obj[k1][k2]...: KeyError where Python raises it.
func index(obj any, keys ...string) (any, error) {
	cur := obj
	for _, k := range keys {
		next, err := pyrt.Index(cur, k)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// get mirrors obj.get(k1).get(k2)... on report data (nil when a key is missing).
func get(obj any, keys ...string) (any, error) {
	cur := obj
	for _, k := range keys {
		next, err := pyrt.Get(cur, k)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// assign mirrors root[k1]...[kn] = value. Intermediate keys must exist.
func assign(root any, value any, keys ...string) error {
	container, err := index(root, keys[:len(keys)-1]...)
	if err != nil {
		return err
	}
	d, ok := container.(*plist.Dict)
	if !ok {
		return fmt.Errorf("TypeError: %T does not support item assignment", container)
	}
	d.Set(keys[len(keys)-1], value)
	return nil
}

// deleteKey mirrors del root[k1]...[kn].
func deleteKey(root any, keys ...string) error {
	container, err := index(root, keys[:len(keys)-1]...)
	if err != nil {
		return err
	}
	d, ok := container.(*plist.Dict)
	if !ok {
		return fmt.Errorf("TypeError: %T does not support item deletion", container)
	}
	last := keys[len(keys)-1]
	if _, found := d.Get(last); !found {
		return fmt.Errorf("KeyError: %q", last)
	}
	d.Delete(last)
	return nil
}

// listAt returns the list stored at root[k1]...[kn], or an error when it is not one.
func listAt(root any, keys ...string) ([]any, error) {
	v, err := index(root, keys...)
	if err != nil {
		return nil, err
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("AttributeError: %T has no list methods", v)
	}
	return list, nil
}

// setList stores a new list at root[k1]...[kn].
func setList(root any, list []any, keys ...string) error {
	return assign(root, list, keys...)
}

// appendList mirrors root[k1]...[kn].append(value).
func appendList(root any, value any, keys ...string) error {
	list, err := listAt(root, keys...)
	if err != nil {
		return err
	}
	return assign(root, append(list, value), keys...)
}

// stringsList converts a Go string slice to a Python-like list.
func stringsList(items ...string) []any {
	out := make([]any, 0, len(items))
	for _, s := range items {
		out = append(out, s)
	}
	return out
}

// hexToBytes mirrors utils.hex_to_bytes. Non-hex characters other than
// whitespace, or an odd number of hex digits, make it return the input string
// unchanged. Otherwise it returns the decoded bytes.
func hexToBytes(s string) any {
	var digits strings.Builder
	nonSpace := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			nonSpace++
		}
		if isHexRune(r) {
			digits.WriteRune(r)
		}
	}
	hexDigits := digits.String()
	if nonSpace != len([]rune(hexDigits)) {
		return s
	}
	b, err := hex.DecodeString(hexDigits)
	if err != nil {
		return s
	}
	if b == nil {
		b = []byte{}
	}
	return b
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// toLittleEndianHex mirrors utils.to_little_endian_hex. Note that Python's
// lstrip("0x") removes any leading '0' or 'x' characters, not the prefix.
func toLittleEndianHex(s string) string {
	s = strings.TrimLeft(strings.ToLower(s), "0x")
	var chunks []string
	for i := 0; i < len(s); i += 2 {
		end := min(i+2, len(s))
		chunks = append(chunks, s[i:end])
	}
	var b strings.Builder
	for i := len(chunks) - 1; i >= 0; i-- {
		b.WriteString(chunks[i])
	}
	return strings.ToUpper(b.String())
}

// intToHex mirrors utils.int_to_hex: format(number, '02X').
func intToHex(v any) (string, error) {
	n, err := pyrt.IntValue(v)
	if err != nil {
		return "", err
	}
	if n < 0 {
		return fmt.Sprintf("-%02X", -n), nil
	}
	return fmt.Sprintf("%02X", n), nil
}

// zfill mirrors str.zfill(width).
func zfill(s string, width int) string {
	if len(s) >= width {
		return s
	}
	pad := strings.Repeat("0", width-len(s))
	if s != "" && (s[0] == '+' || s[0] == '-') {
		return s[:1] + pad + s[1:]
	}
	return pad + s
}

// cmpDarwin compares two darwin versions, parsing a then b.
func cmpDarwin(a, b string) (int, error) {
	va, err := macos.ParseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := macos.ParseVersion(b)
	if err != nil {
		return 0, err
	}
	return va.Compare(vb), nil
}

// darwinAtLeast reports parse(current) >= parse(limit).
func darwinAtLeast(current, limit string) (bool, error) {
	c, err := cmpDarwin(current, limit)
	return c >= 0, err
}

// darwinBelow reports parse(current) < parse(limit).
func darwinBelow(current, limit string) (bool, error) {
	c, err := cmpDarwin(current, limit)
	return c < 0, err
}

// darwinBetween reports lo <= parse(current) < hi, evaluating the parses in the
// order of a chained comparison: the upper bound only when the lower one holds.
func darwinBetween(lo, current, hi string) (bool, error) {
	ok, err := darwinAtLeast(current, lo)
	if err != nil || !ok {
		return false, err
	}
	return darwinBelow(current, hi)
}

// inList mirrors "value in (a, b, ...)": exact membership of a string.
func inList(value any, list ...string) bool {
	s, ok := value.(string)
	if !ok {
		return false
	}
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// inSlice is inList for a Go slice of IDs.
func inSlice(value any, list []string) bool {
	return pyrt.InStrings(value, list)
}

// containsAnyAuthor mirrors utils.contains_any(authors, comment), which returns
// the first author whose lowercase form is in the comment, or "".
func containsAnyAuthor(authors []string, comment string) string {
	lower := strings.ToLower(comment)
	for _, a := range authors {
		if strings.Contains(lower, strings.ToLower(a)) {
			return a
		}
	}
	return ""
}

// tupleLE mirrors Python's tuple comparison a <= b for int tuples.
func tupleLE(a, b []int64) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) <= len(b)
}

// parseResolution mirrors tuple(map(int, value.split("x"))).
func parseResolution(value string) ([]int64, error) {
	var out []int64
	for _, part := range strings.Split(value, "x") {
		v, err := pyrt.Int(part)
		if err != nil {
			return nil, err
		}
		n, err := pyrt.IntValue(v)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// resolutionAtLeast reports tuple(reference) <= tuple(monitor["Resolution"]).
func resolutionAtLeast(monitorInfo any, reference string) (bool, error) {
	raw, err := pyrt.Get(monitorInfo, "Resolution")
	if err != nil {
		return false, err
	}
	text, err := pyrt.Str(raw)
	if err != nil {
		return false, err
	}
	ref, err := parseResolution(reference)
	if err != nil {
		return false, err
	}
	res, err := parseResolution(text)
	if err != nil {
		return false, err
	}
	return tupleLE(ref, res), nil
}

// anyMonitor mirrors any(predicate(name, info) for name, info in monitors),
// stopping at the first true value.
func anyMonitor(monitors any, predicate func(name string, info any) (bool, error)) (bool, error) {
	items, err := pyrt.Items(monitors)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		ok, err := predicate(item.Key, item.Value)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// bigFromAny is used when an integer from the report must be formatted.
func bigFromAny(v any) (*big.Int, error) {
	n, err := pyrt.Int(v)
	if err != nil {
		return nil, err
	}
	switch x := n.(type) {
	case int64:
		return big.NewInt(x), nil
	case *big.Int:
		return x, nil
	}
	return nil, errors.New("TypeError: not an integer")
}

// itoa formats an integer for a string key, as "{}".format(value) does.
func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// startsWithAny mirrors str.startswith(tuple).
func startsWithAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// sliceRunes mirrors s[start:] and s[start:end] with code points and Python clamping.
func sliceRunes(s string, start, end int) string {
	r := []rune(s)
	a, b := clampSlice(start, end, len(r))
	return string(r[a:b])
}

func clampSlice(start, end, n int) (int, int) {
	norm := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	a, b := norm(start), norm(end)
	if b < a {
		b = a
	}
	return a, b
}
