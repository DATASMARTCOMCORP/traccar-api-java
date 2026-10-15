package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Request bodies are read the way Jackson reads them into Java beans: each known property
// goes through its setter, in document order (a repeated property is set twice, so the
// last value wins), absent properties keep the bean's defaults, and scalars are coerced as
// Jackson does by default. A property the class doesn't have fails (400) unless the class
// ignores unknown properties or the property is @JsonIgnore'd.

// props maps JSON property names to setters.
type props map[string]func(json.RawMessage) error

// bean describes how a Java class reads JSON.
type bean struct {
	props         props
	ignoreUnknown bool     // @JsonIgnoreProperties(ignoreUnknown = true)
	ignored       []string // @JsonIgnore'd properties: accepted and dropped
	// notNull are properties stored in NOT NULL columns: Java leaves them null when absent
	// or null, and the insert or update then fails (400). Go fails the same way.
	notNull []string
}

// readJSON reads the request body into a bean, enforcing @Consumes(JSON) (415).
func readJSON(r *request, b bean) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return errStatus(http.StatusUnsupportedMediaType, "Unsupported Media Type")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	err = decodeBean(body, b)
	var missing *errNotNull
	if err != nil && !errors.As(err, &missing) {
		// Jersey's Jackson exception mappers answer mapping errors in plain text.
		return &httpError{status: http.StatusBadRequest, msg: err.Error(), contentType: "text/plain"}
	}
	return err
}

// errNotNull reports a NOT NULL property left null. Java only finds out when the database
// rejects the row, after the insert has used a sequence value, so callers that insert
// must consume one too (see store.ConsumeID).
type errNotNull struct{ property string }

func (e *errNotNull) Error() string { return fmt.Sprintf("null value in column %q", e.property) }

func decodeBean(body []byte, b bean) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New("expected a JSON object")
	}
	set := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		name := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("property %q: %w", name, err)
		}
		set[name] = !isNull(raw)
		setter, ok := b.props[name]
		switch {
		case ok:
			if err := setter(raw); err != nil {
				return fmt.Errorf("property %q: %w", name, err)
			}
		case b.ignoreUnknown || contains(b.ignored, name):
		default:
			return fmt.Errorf("unrecognized field %q", name)
		}
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data after JSON object")
	}
	for _, name := range b.notNull {
		if !set[name] {
			return &errNotNull{name}
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

// Setters for each Java property type. A JSON null leaves a primitive at its current
// value (Jackson's default) and sets an object property to null.

func longProp(dst *int64) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		if isNull(raw) {
			return nil
		}
		v, err := coerceInt(raw, math.MinInt64, math.MaxInt64)
		if err == nil {
			*dst = v
		}
		return err
	}
}

func intProp(dst *int) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		if isNull(raw) {
			return nil
		}
		v, err := coerceInt(raw, math.MinInt32, math.MaxInt32)
		if err == nil {
			*dst = int(v)
		}
		return err
	}
}

// coerceInt accepts integers, floats (truncated: ACCEPT_FLOAT_AS_INT), numeric strings,
// and "" (as 0); booleans, objects and out-of-range values fail.
func coerceInt(raw json.RawMessage, lo, hi int64) (int64, error) {
	var v any
	if err := unmarshalNumber(raw, &v); err != nil {
		return 0, err
	}
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			if n < lo || n > hi {
				return 0, fmt.Errorf("%s out of range", x)
			}
			return n, nil
		}
		f, err := x.Float64()
		if err != nil || f < float64(lo) || f > float64(hi) {
			return 0, fmt.Errorf("cannot convert %s to integer", x)
		}
		return int64(f), nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < lo || n > hi {
			return 0, fmt.Errorf("cannot convert %q to integer", x)
		}
		return n, nil
	}
	return 0, fmt.Errorf("cannot convert %s to integer", raw)
}

func doubleProp(dst *float64) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		if isNull(raw) {
			return nil
		}
		var v any
		if err := unmarshalNumber(raw, &v); err != nil {
			return err
		}
		switch x := v.(type) {
		case json.Number:
			f, err := strconv.ParseFloat(string(x), 64)
			if err != nil && !errors.Is(err, strconv.ErrRange) {
				return err
			}
			*dst = f
			return nil
		case string:
			s := strings.TrimSpace(x)
			if s == "" {
				*dst = 0
				return nil
			}
			f, err := parseJavaDouble(s)
			if err != nil {
				return err
			}
			*dst = f
			return nil
		}
		return fmt.Errorf("cannot convert %s to double", raw)
	}
}

// parseJavaDouble is Double.parseDouble for the forms that matter in JSON strings.
func parseJavaDouble(s string) (float64, error) {
	switch s {
	case "NaN":
		return math.NaN(), nil
	case "Infinity", "+Infinity":
		return math.Inf(1), nil
	case "-Infinity":
		return math.Inf(-1), nil
	}
	return strconv.ParseFloat(strings.TrimRight(s, "dDfF"), 64)
}

func boolProp(dst *bool) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		if isNull(raw) {
			return nil
		}
		var v any
		if err := unmarshalNumber(raw, &v); err != nil {
			return err
		}
		switch x := v.(type) {
		case bool:
			*dst = x
			return nil
		case string:
			switch strings.TrimSpace(x) {
			case "true", "True", "TRUE":
				*dst = true
				return nil
			case "false", "False", "FALSE", "":
				*dst = false
				return nil
			}
		case json.Number:
			if n, err := x.Int64(); err == nil {
				*dst = n != 0
				return nil
			}
		}
		return fmt.Errorf("cannot convert %s to boolean", raw)
	}
}

// stringProp sets a nullable String; scalars are coerced to text, as Jackson does.
func stringProp(dst **string) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		if isNull(raw) {
			*dst = nil
			return nil
		}
		s, err := coerceString(raw)
		if err == nil {
			*dst = &s
		}
		return err
	}
}

// requiredString sets a String the Java setter dereferences: null fails (NullPointerException).
func requiredString(dst *string, transform func(string) string) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		if isNull(raw) {
			return errors.New("null value")
		}
		s, err := coerceString(raw)
		if err != nil {
			return err
		}
		if transform != nil {
			s = transform(s)
		}
		*dst = s
		return nil
	}
}

// plainString sets a String column the Java setter stores as is; null keeps "" here, as
// the NOT NULL column would refuse it on insert anyway.
func plainString(dst *string) func(json.RawMessage) error {
	return func(raw json.RawMessage) error {
		if isNull(raw) {
			*dst = ""
			return nil
		}
		s, err := coerceString(raw)
		if err == nil {
			*dst = s
		}
		return err
	}
}

func coerceString(raw json.RawMessage) (string, error) {
	var v any
	if err := unmarshalNumber(raw, &v); err != nil {
		return "", err
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return string(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	}
	return "", fmt.Errorf("cannot convert %s to string", raw)
}

func timeProp(dst *model.Time) func(json.RawMessage) error {
	return func(raw json.RawMessage) error { return dst.UnmarshalJSON(raw) }
}

func attributesProp(dst *model.Attributes) func(json.RawMessage) error {
	return func(raw json.RawMessage) error { return dst.UnmarshalJSON(raw) }
}

func unmarshalNumber(raw json.RawMessage, v *any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}
