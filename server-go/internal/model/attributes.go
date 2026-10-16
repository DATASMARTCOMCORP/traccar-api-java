package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
)

// Attributes is a JSON object the way Java's AttributeMap and Jackson keep it:
//   - keys keep insertion order; setting an existing key keeps its position and
//     replaces the value, and a repeated key in JSON input behaves the same way
//   - numbers remember whether they were written as integers or doubles, so 87 and
//     87.0 round-trip unchanged
//
// Values are nil, bool, string, int64, *big.Int (integers beyond 64 bits, Java
// BigInteger), float64, []any, or *Attributes for a nested object.
//
// The zero value is an empty object, ready to use.
type Attributes struct {
	keys   []string
	values map[string]any
}

// Len returns the number of entries.
func (a *Attributes) Len() int {
	if a == nil {
		return 0
	}
	return len(a.keys)
}

// Get returns the value for key.
func (a *Attributes) Get(key string) (any, bool) {
	if a == nil {
		return nil, false
	}
	v, ok := a.values[key]
	return v, ok
}

// Set stores value under key, keeping the key's position if it exists. Go integer types
// become int64 and float32 becomes float64 (widened exactly, like Java's
// Float.doubleValue), matching the types Java stores.
func (a *Attributes) Set(key string, value any) {
	value = normalize(value)
	if a.values == nil {
		a.values = map[string]any{}
	}
	if _, ok := a.values[key]; !ok {
		a.keys = append(a.keys, key)
	}
	a.values[key] = value
}

// Delete removes key; later keys move up, as in AttributeMap.remove.
func (a *Attributes) Delete(key string) {
	if a == nil {
		return
	}
	if _, ok := a.values[key]; !ok {
		return
	}
	delete(a.values, key)
	a.keys = slices.DeleteFunc(a.keys, func(k string) bool { return k == key })
}

// All iterates over the entries in order.
func (a *Attributes) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		if a == nil {
			return
		}
		for _, k := range a.keys {
			if !yield(k, a.values[k]) {
				return
			}
		}
	}
}

func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case float32:
		return float64(x)
	case Attributes:
		return &x
	case map[string]any:
		// Plain maps have no order; sort keys so output is at least stable.
		nested := &Attributes{}
		for _, k := range slices.Sorted(maps.Keys(x)) {
			nested.Set(k, x[k])
		}
		return nested
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	}
	return v
}

// MarshalJSON writes the object as Jackson writes the Java map.
func (a Attributes) MarshalJSON() ([]byte, error) {
	return a.appendJSON(nil)
}

// String returns the JSON text, as stored in the database.
func (a *Attributes) String() string {
	if a == nil {
		return "{}"
	}
	b, err := a.appendJSON(nil)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (a *Attributes) appendJSON(b []byte) ([]byte, error) {
	b = append(b, '{')
	for i, k := range a.keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendString(b, k)
		b = append(b, ':')
		var err error
		if b, err = appendValue(b, a.values[k]); err != nil {
			return nil, fmt.Errorf("attribute %q: %w", k, err)
		}
	}
	return append(b, '}'), nil
}

func appendValue(b []byte, v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return append(b, "null"...), nil
	case bool:
		return strconv.AppendBool(b, x), nil
	case string:
		return appendString(b, x), nil
	case int64:
		return strconv.AppendInt(b, x, 10), nil
	case *big.Int:
		return x.Append(b, 10), nil
	case float64:
		return appendDouble(b, x), nil
	case Float:
		return appendFloat(b, x), nil
	case *Attributes:
		return x.appendJSON(b)
	case []any:
		b = append(b, '[')
		for i, e := range x {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			if b, err = appendValue(b, e); err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	}
	// Anything else came from Set without normalizing; refuse rather than guess.
	return nil, fmt.Errorf("unsupported attribute type %T", v)
}

// UnmarshalJSON reads a JSON object. null reads as an empty object, as Java replaces a
// null attribute map with an empty one.
func (a *Attributes) UnmarshalJSON(data []byte) error {
	*a = Attributes{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("attributes: expected a JSON object")
	}
	if err := a.decodeObject(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("attributes: trailing data")
	}
	return nil
}

// ParseAttributes reads attributes from a database column. NULL, "" and "null" are
// empty, as in Java.
func ParseAttributes(text string) (Attributes, error) {
	var a Attributes
	if text == "" {
		return a, nil
	}
	err := a.UnmarshalJSON([]byte(text))
	return a, err
}

// decodeObject reads entries after the opening '{' up to and including the closing '}'.
func (a *Attributes) decodeObject(dec *json.Decoder) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if d, ok := tok.(json.Delim); ok && d == '}' {
			return nil
		}
		key, ok := tok.(string)
		if !ok {
			return errors.New("attributes: expected a key")
		}
		v, err := decodeValue(dec)
		if err != nil {
			return err
		}
		a.Set(key, v)
	}
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			nested := &Attributes{}
			if err := nested.decodeObject(dec); err != nil {
				return nil, err
			}
			return nested, nil
		case '[':
			list := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			if _, err := dec.Token(); err != nil { // ']'
				return nil, err
			}
			return list, nil
		}
		return nil, fmt.Errorf("attributes: unexpected %v", t)
	case json.Number:
		return parseNumber(string(t))
	default: // nil, bool, string
		return t, nil
	}
}

// parseNumber classifies a JSON number literal as Jackson does: integers become Integer,
// Long or BigInteger (int64 or *big.Int here), anything with a fraction or exponent
// becomes Double. A double too large for float64 becomes ±Inf, which is then written as
// the string "Infinity", exactly as Java ends up storing it.
func parseNumber(lit string) (any, error) {
	if !strings.ContainsAny(lit, ".eE") {
		if n, err := strconv.ParseInt(lit, 10, 64); err == nil {
			return n, nil
		}
		n, ok := new(big.Int).SetString(lit, 10)
		if !ok {
			return nil, fmt.Errorf("attributes: bad number %q", lit)
		}
		return n, nil
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return nil, fmt.Errorf("attributes: bad number %q", lit)
	}
	return f, nil // ErrRange: ±Inf on overflow, ±0 on underflow, as Double.parseDouble
}

// Float is a java.lang.Float kept as such: decoders that put a Float into the attribute
// map directly (not through set, which widens it to a Double) store this, and Jackson
// writes it with Float.toString.
type Float float32

func appendFloat(b []byte, f Float) []byte {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return appendDouble(b, v)
	}
	return append(b, JavaFloat(float32(f))...)
}
