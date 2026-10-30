// Package pyrt reproduces the Python operations that OpCore Simplify applies to
// the hardware report and other JSON-like data: dict.get, "in", len, int(),
// truthiness and ==. Each helper returns an error where Python would raise, so
// ports keep the same failure behavior instead of silently using zero values.
//
// Values follow the types produced by pyjson and plist: *plist.Dict, []any,
// string, bool, int64, *big.Int, float64 and nil.
package pyrt

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/Lufel3846/OpCore-Simplify/go/internal/plist"
)

// ErrAttribute is Python's AttributeError on a value that has no .get / .items.
var ErrAttribute = errors.New("AttributeError")

// ErrType is Python's TypeError.
var ErrType = errors.New("TypeError")

// ErrKey is Python's KeyError.
var ErrKey = errors.New("KeyError")

// Get mirrors obj.get(key): the value, or nil when the key is missing.
func Get(obj any, key string) (any, error) {
	d, ok := obj.(*plist.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: %T has no attribute 'get'", ErrAttribute, obj)
	}
	v, _ := d.Get(key)
	return v, nil
}

// GetDefault mirrors obj.get(key, def).
func GetDefault(obj any, key string, def any) (any, error) {
	d, ok := obj.(*plist.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: %T has no attribute 'get'", ErrAttribute, obj)
	}
	if v, found := d.Get(key); found {
		return v, nil
	}
	return def, nil
}

// Index mirrors obj[key] for a dict, raising KeyError when the key is missing.
func Index(obj any, key string) (any, error) {
	d, ok := obj.(*plist.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: %T is not subscriptable by str", ErrType, obj)
	}
	v, found := d.Get(key)
	if !found {
		return nil, fmt.Errorf("%w: %q", ErrKey, key)
	}
	return v, nil
}

// Items mirrors dict.items(): the key/value pairs in insertion order.
func Items(obj any) ([]Pair, error) {
	d, ok := obj.(*plist.Dict)
	if !ok {
		return nil, fmt.Errorf("%w: %T has no attribute 'items'", ErrAttribute, obj)
	}
	var out []Pair
	for _, k := range d.Keys() {
		v, _ := d.Get(k)
		out = append(out, Pair{Key: k, Value: v})
	}
	return out, nil
}

// Values mirrors dict.values().
func Values(obj any) ([]any, error) {
	pairs, err := Items(obj)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(pairs))
	for i, p := range pairs {
		out[i] = p.Value
	}
	return out, nil
}

// Pair is one dict item.
type Pair struct {
	Key   string
	Value any
}

// In mirrors Python's "needle in haystack":
//   - str haystack: substring test, and the needle must be a str (else TypeError);
//   - list haystack: any element equal to the needle;
//   - dict haystack: the needle is a key;
//   - anything else (including None): TypeError.
func In(needle, haystack any) (bool, error) {
	switch h := haystack.(type) {
	case string:
		n, ok := needle.(string)
		if !ok {
			return false, fmt.Errorf("%w: 'in <string>' requires string as left operand, not %T", ErrType, needle)
		}
		return strings.Contains(h, n), nil
	case []any:
		for _, item := range h {
			if Equal(needle, item) {
				return true, nil
			}
		}
		return false, nil
	case *plist.Dict:
		n, ok := needle.(string)
		if !ok {
			return false, nil
		}
		_, found := h.Get(n)
		return found, nil
	}
	return false, fmt.Errorf("%w: argument of type '%T' is not iterable", ErrType, haystack)
}

// InStrings is In for a Go []string haystack, used for the generated datasets.
func InStrings(needle any, haystack []string) bool {
	s, ok := needle.(string)
	if !ok {
		return false
	}
	for _, h := range haystack {
		if h == s {
			return true
		}
	}
	return false
}

// Len mirrors len(v) for str (code points), list and dict.
func Len(v any) (int, error) {
	switch x := v.(type) {
	case string:
		return utf8.RuneCountInString(x), nil
	case []any:
		return len(x), nil
	case *plist.Dict:
		return x.Len(), nil
	}
	return 0, fmt.Errorf("%w: object of type '%T' has no len()", ErrType, v)
}

// Str returns v when it is a str, and raises TypeError otherwise.
func Str(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%w: expected str, got %T", ErrType, v)
	}
	return s, nil
}

// Int mirrors int(v) for the value types that appear in the report: int64 and
// *big.Int pass through, bool gives 0 or 1, float truncates toward zero, and str
// is parsed with Python's rules. None raises TypeError.
func Int(v any) (any, error) {
	switch x := v.(type) {
	case int64, *big.Int:
		return x, nil
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, fmt.Errorf("ValueError: cannot convert float %v to integer", x)
		}
		return int64(x), nil
	case string:
		return plist.ParseInt(x, 10)
	}
	return nil, fmt.Errorf("%w: int() argument must be a string or a number, not %T", ErrType, v)
}

// IntValue is Int for callers that need a Go int, such as core counts. It
// fails when the value does not fit.
func IntValue(v any) (int64, error) {
	n, err := Int(v)
	if err != nil {
		return 0, err
	}
	switch x := n.(type) {
	case int64:
		return x, nil
	case *big.Int:
		if x.IsInt64() {
			return x.Int64(), nil
		}
	}
	return 0, errors.New("OverflowError: int too large")
}

// Truthy mirrors bool(v).
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int64:
		return x != 0
	case *big.Int:
		return x.Sign() != 0
	case float64:
		return x != 0
	case *plist.Dict:
		return x.Len() > 0
	case []any:
		return len(x) > 0
	}
	return true
}

// Equal mirrors Python's == for the value types above. Numbers compare by
// value across int and float, so 1 == 1.0, and bool is a number (True == 1).
func Equal(a, b any) bool {
	if na, ok := number(a); ok {
		if nb, ok := number(b); ok {
			return na.Cmp(nb) == 0
		}
		return false
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case *plist.Dict:
		y, ok := b.(*plist.Dict)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for _, k := range x.Keys() {
			va, _ := x.Get(k)
			vb, found := y.Get(k)
			if !found || !Equal(va, vb) {
				return false
			}
		}
		return true
	}
	return false
}

// number converts the numeric value types to a comparable form. Floats are
// compared through big.Float, which is exact for every float64.
func number(v any) (*big.Float, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return big.NewFloat(1), true
		}
		return big.NewFloat(0), true
	case int64:
		return new(big.Float).SetInt64(x), true
	case *big.Int:
		return new(big.Float).SetInt(x), true
	case float64:
		if math.IsNaN(x) {
			return nil, false
		}
		return new(big.Float).SetFloat64(x), true
	}
	return nil, false
}
