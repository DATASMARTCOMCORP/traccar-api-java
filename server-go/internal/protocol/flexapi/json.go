package flexapi

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// jsonObject is a JSON object as Jakarta JSON (Parsson) reads it: keys in document order,
// a repeated key keeping its first position and its last value.
type jsonObject struct {
	keys   []string
	values map[string]any // string, bool, nil, jsonNumber, *jsonObject, []any
}

// jsonNumber is a JSON number as Parsson keeps it: its decimal text (a BigDecimal).
type jsonNumber string

// parseJSON reads one JSON value; malformed input throws, as Parsson's reader.
func parseJSON(text string) any {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	v := readValue(dec)
	if _, err := dec.Token(); err == nil {
		kit.Throw("JsonParsingException: unexpected content after the value")
	}
	return v
}

func token(dec *json.Decoder) json.Token {
	tok, err := dec.Token()
	if err != nil {
		kit.Throw("JsonParsingException: %v", err)
	}
	return tok
}

func readValue(dec *json.Decoder) any {
	switch t := token(dec).(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &jsonObject{values: map[string]any{}}
			for dec.More() {
				name := token(dec).(string)
				v := readValue(dec)
				if _, ok := o.values[name]; !ok {
					o.keys = append(o.keys, name)
				}
				o.values[name] = v
			}
			token(dec)
			return o
		case '[':
			list := []any{}
			for dec.More() {
				list = append(list, readValue(dec))
			}
			token(dec)
			return list
		}
		kit.Throw("JsonParsingException: unexpected %v", t)
		return nil
	case json.Number:
		return jsonNumber(t)
	default:
		return t // string, bool or nil
	}
}

// number is the (JsonNumber) cast: anything else throws ClassCastException.
func number(v any) jsonNumber {
	n, ok := v.(jsonNumber)
	if !ok {
		kit.Throw("ClassCastException: %T is not a JsonNumber", v)
	}
	return n
}

// str is the (JsonString) cast.
func str(v any) string {
	s, ok := v.(string)
	if !ok {
		kit.Throw("ClassCastException: %T is not a JsonString", v)
	}
	return s
}

// double is BigDecimal.doubleValue.
func (n jsonNumber) double() float64 {
	v, _ := strconv.ParseFloat(string(n), 64)
	return v
}

// integral is JsonNumber.isIntegral: a BigDecimal scale of 0.
func (n jsonNumber) integral() bool { return !strings.ContainsAny(string(n), ".eE") }

// longValue is BigDecimal.longValue: the low 64 bits of the value truncated toward zero.
func (n jsonNumber) longValue() int64 {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return 0
	}
	t := new(big.Int).Quo(r.Num(), r.Denom())
	return int64(new(big.Int).And(t, new(big.Int).SetUint64(math.MaxUint64)).Uint64())
}

// intValue is BigDecimal.intValue.
func (n jsonNumber) intValue() int { return int(int32(n.longValue())) }
