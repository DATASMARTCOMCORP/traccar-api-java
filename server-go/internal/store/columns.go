package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Adapters between Go values and the JSON text columns, matching what Java's
// QueryBuilder does with Jackson: non-primitive fields are written as JSON (a null field
// becomes the text "null"), and on read NULL or "" leave the field at its default.

// attrs binds a model.Attributes to a JSON text column.
type attrs struct{ a *model.Attributes }

func (c attrs) Value() string { return c.a.String() }

func (c attrs) Scan(src any) error {
	text, ok, err := textOf(src)
	if err != nil || !ok {
		*c.a = model.Attributes{}
		return err
	}
	a, err := model.ParseAttributes(text)
	if err != nil {
		// Java logs the error and leaves the default (empty) map.
		slog.Warn("unreadable attributes column", "err", err)
	}
	*c.a = a
	return nil
}

// optionalAttrs binds a nullable map (tc_statistics.protocols).
type optionalAttrs struct{ a **model.Attributes }

func (c optionalAttrs) Value() string {
	if *c.a == nil {
		return "null"
	}
	return (*c.a).String()
}

func (c optionalAttrs) Scan(src any) error {
	*c.a = nil
	text, ok, err := textOf(src)
	if err != nil || !ok || text == "null" {
		return err
	}
	a, err := model.ParseAttributes(text)
	if err != nil {
		slog.Warn("unreadable map column", "err", err)
		return nil
	}
	*c.a = &a
	return nil
}

// network binds tc_positions.network.
type network struct{ n **model.Network }

func (c network) Value() string {
	if *c.n == nil {
		return "null"
	}
	b, _ := (*c.n).MarshalJSON() // never fails
	return string(b)
}

func (c network) Scan(src any) error {
	*c.n = nil
	text, ok, err := textOf(src)
	if err != nil || !ok || text == "null" {
		return err
	}
	var n model.Network
	if err := json.Unmarshal([]byte(text), &n); err != nil {
		slog.Warn("unreadable network column", "err", err)
		return nil
	}
	*c.n = &n
	return nil
}

// longList binds tc_positions.geofenceids, a Java List<Long>.
type longList struct{ l *[]int64 }

func (c longList) Value() string {
	if *c.l == nil {
		return "null"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range *c.l {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(v, 10))
	}
	b.WriteByte(']')
	return b.String()
}

func (c longList) Scan(src any) error {
	*c.l = nil
	text, ok, err := textOf(src)
	if err != nil || !ok || text == "null" {
		return err
	}
	var numbers []json.Number
	if err := json.Unmarshal([]byte(text), &numbers); err != nil {
		slog.Warn("unreadable list column", "err", err)
		return nil
	}
	list := make([]int64, len(numbers))
	for i, n := range numbers {
		// Java's setGeofenceIds keeps Number.longValue(), truncating any fraction.
		if v, err := n.Int64(); err == nil {
			list[i] = v
		} else if f, err := n.Float64(); err == nil {
			list[i] = int64(f)
		}
	}
	*c.l = list
	return nil
}

// status binds tc_devices.status, a blank-padded character column; Java trims on read.
type status struct{ s **string }

func (c status) Scan(src any) error {
	*c.s = nil
	text, ok, err := textOf(src)
	if err != nil || !ok {
		return err
	}
	t := strings.TrimSpace(text)
	*c.s = &t
	return nil
}

// textOf returns the text of a scanned column; ok is false for NULL and "".
func textOf(src any) (string, bool, error) {
	switch v := src.(type) {
	case nil:
		return "", false, nil
	case string:
		return v, v != "", nil
	case []byte:
		return string(v), len(v) > 0, nil
	}
	return "", false, fmt.Errorf("text column: unexpected %T", src)
}

// nullBool, nullInt and nullFloat read NULL as the zero value, as JDBC does.
type nullBool struct{ v *bool }

func (n nullBool) Scan(src any) error {
	switch x := src.(type) {
	case nil:
		*n.v = false
	case bool:
		*n.v = x
	default:
		return fmt.Errorf("boolean column: unexpected %T", src)
	}
	return nil
}

type nullInt struct{ v *int }

func (n nullInt) Scan(src any) error {
	switch x := src.(type) {
	case nil:
		*n.v = 0
	case int64:
		*n.v = int(x)
	case int32:
		*n.v = int(x)
	default:
		return fmt.Errorf("integer column: unexpected %T", src)
	}
	return nil
}

type nullFloat struct{ v *float64 }

func (n nullFloat) Scan(src any) error {
	switch x := src.(type) {
	case nil:
		*n.v = 0
	case float64:
		*n.v = x
	default:
		return fmt.Errorf("double column: unexpected %T", src)
	}
	return nil
}

// ErrNotFound is returned when a row doesn't exist.
var ErrNotFound = errors.New("not found")
