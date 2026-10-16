package kit

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Jakarta JSON (Parsson), as decoders read JSON: objects keep their keys in document order
// (a repeated key keeps its first position and its last value), numbers keep their decimal
// text, and the getters throw where JsonObject's do.

// JSONObject is a JsonObject.
type JSONObject struct {
	keys   []string
	values map[string]any // string, bool, nil, JSONNumber, *JSONObject, []any
}

// JSONNumber is a JsonNumber: its decimal text (a BigDecimal).
type JSONNumber string

// ParseJSON is Json.createReader(...).read(): one value; malformed input throws.
func ParseJSON(text []byte) any {
	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()
	v := readJSONValue(dec)
	if _, err := dec.Token(); err == nil {
		Throw("JsonParsingException: unexpected content after the value")
	}
	return v
}

// ParseJSONObject is readObject(): the value must be an object.
func ParseJSONObject(text []byte) *JSONObject {
	o, ok := ParseJSON(text).(*JSONObject)
	if !ok {
		Throw("JsonParsingException: not an object")
	}
	return o
}

func jsonToken(dec *json.Decoder) json.Token {
	tok, err := dec.Token()
	if err != nil {
		Throw("JsonParsingException: %v", err)
	}
	return tok
}

func readJSONValue(dec *json.Decoder) any {
	switch t := jsonToken(dec).(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &JSONObject{values: map[string]any{}}
			for dec.More() {
				name := jsonToken(dec).(string)
				v := readJSONValue(dec)
				if _, ok := o.values[name]; !ok {
					o.keys = append(o.keys, name)
				}
				o.values[name] = v
			}
			jsonToken(dec)
			return o
		case '[':
			list := []any{}
			for dec.More() {
				list = append(list, readJSONValue(dec))
			}
			jsonToken(dec)
			return list
		}
		Throw("JsonParsingException: unexpected %v", t)
		return nil
	case json.Number:
		return JSONNumber(t)
	default:
		return t // string, bool or nil
	}
}

// Keys are the object's keys in order (keySet()).
func (o *JSONObject) Keys() []string { return o.keys }

// Has is containsKey.
func (o *JSONObject) Has(key string) bool { _, ok := o.values[key]; return ok }

// Get is get(key): nil for a missing key or a JSON null (IsNull tells them apart).
func (o *JSONObject) Get(key string) any { return o.values[key] }

// IsNull is isNull(key): a missing key throws (NullPointerException).
func (o *JSONObject) IsNull(key string) bool {
	v, ok := o.values[key]
	if !ok {
		Throw("NullPointerException: no %q", key)
	}
	return v == nil
}

func (o *JSONObject) present(key string) any {
	v, ok := o.values[key]
	if !ok {
		Throw("NullPointerException: no %q", key)
	}
	return v
}

// String is getString(key).
func (o *JSONObject) String(key string) string { return JSONString(o.present(key)) }

// StringOr is getString(key, default): the default unless the value is a string.
func (o *JSONObject) StringOr(key, def string) string {
	if s, ok := o.values[key].(string); ok {
		return s
	}
	return def
}

// Int is getInt(key): JsonNumber.intValue.
func (o *JSONObject) Int(key string) int { return JSONNumberOf(o.present(key)).Int() }

// IntOr is getInt(key, default).
func (o *JSONObject) IntOr(key string, def int) int {
	if n, ok := o.values[key].(JSONNumber); ok {
		return n.Int()
	}
	return def
}

// Number is getJsonNumber(key): nil when missing.
func (o *JSONObject) Number(key string) *JSONNumber {
	v, ok := o.values[key]
	if !ok || v == nil {
		return nil
	}
	n := JSONNumberOf(v)
	return &n
}

// Bool is getBoolean(key).
func (o *JSONObject) Bool(key string) bool {
	b, ok := o.present(key).(bool)
	if !ok {
		Throw("ClassCastException: %q is not a boolean", key)
	}
	return b
}

// BoolOr is getBoolean(key, default).
func (o *JSONObject) BoolOr(key string, def bool) bool {
	if b, ok := o.values[key].(bool); ok {
		return b
	}
	return def
}

// Object is getJsonObject(key): nil when missing or null; another type throws.
func (o *JSONObject) Object(key string) *JSONObject {
	switch v := o.values[key].(type) {
	case nil:
		return nil
	case *JSONObject:
		return v
	}
	Throw("ClassCastException: %q is not an object", key)
	return nil
}

// Array is getJsonArray(key): nil when missing or null; another type throws.
func (o *JSONObject) Array(key string) []any {
	switch v := o.values[key].(type) {
	case nil:
		return nil
	case []any:
		return v
	}
	Throw("ClassCastException: %q is not an array", key)
	return nil
}

// JSONString is the (JsonString) cast's getString.
func JSONString(v any) string {
	s, ok := v.(string)
	if !ok {
		Throw("ClassCastException: %T is not a JsonString", v)
	}
	return s
}

// JSONNumberOf is the (JsonNumber) cast.
func JSONNumberOf(v any) JSONNumber {
	n, ok := v.(JSONNumber)
	if !ok {
		Throw("ClassCastException: %T is not a JsonNumber", v)
	}
	return n
}

// JSONObjectOf is the (JsonObject) cast.
func JSONObjectOf(v any) *JSONObject {
	o, ok := v.(*JSONObject)
	if !ok {
		Throw("ClassCastException: %T is not a JsonObject", v)
	}
	return o
}

// Double is BigDecimal.doubleValue.
func (n JSONNumber) Double() float64 {
	v, _ := strconv.ParseFloat(string(n), 64)
	return v
}

// Integral is isIntegral: a BigDecimal scale of 0.
func (n JSONNumber) Integral() bool { return !strings.ContainsAny(string(n), ".eE") }

// Long is BigDecimal.longValue: the low 64 bits of the value truncated toward zero.
func (n JSONNumber) Long() int64 {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return 0
	}
	t := new(big.Int).Quo(r.Num(), r.Denom())
	return int64(new(big.Int).And(t, new(big.Int).SetUint64(math.MaxUint64)).Uint64())
}

// Int is BigDecimal.intValue.
func (n JSONNumber) Int() int { return int(int32(n.Long())) }
