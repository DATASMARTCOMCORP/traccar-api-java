package xlsx

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// BeanOf turns a model object into the Bean JEXL sees, through its JSON (which is how
// Jackson sees the same getters): integers stay Long, decimals are Double, dates
// ("...Time" and lastUpdate) are Dates, attributes keep their order, nested objects are
// Beans.
func BeanOf(v json.Marshaler) (Bean, error) {
	data, err := v.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return beanOf(data)
}

func beanOf(data []byte) (Bean, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	b := Bean{}
	for k, raw := range fields {
		if k == "attributes" {
			var a model.Attributes
			if err := a.UnmarshalJSON(raw); err != nil {
				return nil, err
			}
			b[k] = Attributes{&a}
			continue
		}
		v, err := value(k, raw)
		if err != nil {
			return nil, err
		}
		b[k] = v
	}
	return b, nil
}

func value(key string, raw json.RawMessage) (any, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch raw[0] {
	case '{':
		return beanOf(raw)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		out := make([]any, len(items))
		for i, it := range items {
			var err error
			if out[i], err = value("", it); err != nil {
				return nil, err
			}
		}
		return out, nil
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		if strings.HasSuffix(key, "Time") || key == "lastUpdate" {
			if t, err := time.Parse("2006-01-02T15:04:05.000-07:00", s); err == nil {
				return t, nil
			}
		}
		return s, nil
	case 't', 'f':
		return string(raw) == "true", nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	if strings.ContainsAny(string(n), ".eE") || strings.Contains(string(n), "Infinity") {
		return n.Float64()
	}
	return n.Int64()
}
