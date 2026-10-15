package osmand

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// jsonObject is a JSON object as Jakarta JSON (Parsson) reads it: keys in document order,
// a repeated key keeping its first position and its last value, and numbers kept as
// decimal text.
type jsonObject struct {
	keys   []string
	values map[string]any // string, bool, nil, jsonNumber, *jsonObject, []any
}

// parseJSONObject is Json.createReader(...).readObject().
func parseJSONObject(text string) (*jsonObject, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("json: not an object")
	}
	return readObject(dec)
}

func readObject(dec *json.Decoder) (*jsonObject, error) {
	o := &jsonObject{values: map[string]any{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name := tok.(string)
		v, err := readValue(dec)
		if err != nil {
			return nil, err
		}
		if _, ok := o.values[name]; !ok {
			o.keys = append(o.keys, name)
		}
		o.values[name] = v
	}
	if _, err := dec.Token(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return o, nil
}

func readValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return readObject(dec)
		case '[':
			var list []any
			for dec.More() {
				v, err := readValue(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return list, nil
		}
		return nil, errors.New("json: unexpected delimiter")
	case json.Number:
		return jsonNumber(t), nil
	}
	return tok, nil // string, bool or nil
}

func (o *jsonObject) has(name string) bool {
	_, ok := o.values[name]
	return ok
}

// The getters fail where Parsson throws: a missing key (NullPointerException) or a value
// of another type (ClassCastException).

func (o *jsonObject) get(name string) (any, error) {
	v, ok := o.values[name]
	if !ok {
		return nil, fmt.Errorf("json: %q is missing", name)
	}
	return v, nil
}

func (o *jsonObject) getString(name string) (string, error) {
	v, err := o.get(name)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("json: %q is not a string", name)
	}
	return s, nil
}

func (o *jsonObject) getBoolean(name string) (bool, error) {
	v, err := o.get(name)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("json: %q is not a boolean", name)
	}
	return b, nil
}

func (o *jsonObject) getNumber(name string) (jsonNumber, error) {
	v, err := o.get(name)
	if err != nil {
		return "", err
	}
	n, ok := v.(jsonNumber)
	if !ok {
		return "", fmt.Errorf("json: %q is not a number", name)
	}
	return n, nil
}

func (o *jsonObject) getObject(name string) (*jsonObject, error) {
	v, err := o.get(name)
	if err != nil {
		return nil, err
	}
	obj, ok := v.(*jsonObject)
	if !ok {
		return nil, fmt.Errorf("json: %q is not an object", name)
	}
	return obj, nil
}
