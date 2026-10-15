package model

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Typed reads of attributes, as ExtendedModel's getters do them. Java converts a stored
// value of another type through its toString and parses it, which can throw; these
// return an error in exactly those cases.

// Has is hasAttribute.
func (a *Attributes) Has(key string) bool {
	_, ok := a.Get(key)
	return ok
}

// JavaString is getString: the value's toString, or nil when absent.
func (a *Attributes) JavaString(key string) *string {
	v, ok := a.Get(key)
	if !ok || v == nil {
		return nil
	}
	s := JavaToString(v)
	return &s
}

// JavaDouble is getDouble(key, def): numbers as doubles, anything else through
// Double.parseDouble of its text.
func (a *Attributes) JavaDouble(key string, def float64) (float64, error) {
	v, ok := a.Get(key)
	if !ok || v == nil {
		return def, nil
	}
	if f, ok := doubleValue(v); ok {
		return f, nil
	}
	return ParseDouble(JavaToString(v))
}

// JavaBool is getBoolean: booleans as they are, anything else through
// Boolean.parseBoolean of its text (which never fails); false when absent.
func (a *Attributes) JavaBool(key string) bool {
	v, ok := a.Get(key)
	if !ok || v == nil {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return ParseBoolean(JavaToString(v))
}

// JavaLong is getLong: numbers through longValue, anything else through Long.parseLong
// of its text; 0 when absent.
func (a *Attributes) JavaLong(key string) (int64, error) {
	v, ok := a.Get(key)
	if !ok || v == nil {
		return 0, nil
	}
	switch x := v.(type) {
	case int64:
		return x, nil
	case float64:
		return javaDoubleToLong(x), nil
	case *big.Int:
		return int64(new(big.Int).And(x, new(big.Int).SetUint64(math.MaxUint64)).Uint64()), nil
	}
	return ParseLong(JavaToString(v))
}

func doubleValue(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, true
	}
	return 0, false
}

// javaDoubleToLong is Java's (long) cast: truncation, saturation, NaN to 0.
func javaDoubleToLong(f float64) int64 {
	switch {
	case math.IsNaN(f):
		return 0
	case f >= math.MaxInt64:
		return math.MaxInt64
	case f <= math.MinInt64:
		return math.MinInt64
	}
	return int64(f)
}

// JavaToString is Object.toString for the values attributes hold: Double.toString for
// doubles, "[a, b]" for lists and "{k=v}" for objects.
func JavaToString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return javaDouble(x)
	case *big.Int:
		return x.String()
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = JavaToString(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Attributes:
		var parts []string
		for k, e := range x.All() {
			parts = append(parts, k+"="+JavaToString(e))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return ""
}
