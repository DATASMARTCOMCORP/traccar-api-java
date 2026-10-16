package kit

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Command attribute getters (ExtendedModel.getString, getInteger, ...): a value that
// doesn't parse panics, where Java throws.

// CommandString is command.getString(key): nil when absent.
func CommandString(c *model.Command, key string) *string {
	return c.Attributes.JavaString(key)
}

// CommandInt is command.getInteger(key): 0 when absent.
func CommandInt(c *model.Command, key string) int {
	v, ok := c.Attributes.Get(key)
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case int64:
		return int(int32(x))
	case float64:
		return int(javaDoubleToInt(x))
	}
	return Int(model.JavaToString(v))
}

// CommandLong is command.getLong(key): 0 when absent.
func CommandLong(c *model.Command, key string) int64 {
	v, err := c.Attributes.JavaLong(key)
	if err != nil {
		Throw("NumberFormatException: %v", err)
	}
	return v
}

// CommandDouble is command.getDouble(key): 0 when absent.
func CommandDouble(c *model.Command, key string) float64 {
	v, err := c.Attributes.JavaDouble(key, 0)
	if err != nil {
		Throw("NumberFormatException: %v", err)
	}
	return v
}

// CommandBool is command.getBoolean(key).
func CommandBool(c *model.Command, key string) bool { return c.Attributes.JavaBool(key) }

// FormatCommand is StringProtocolEncoder.formatCommand: String.format of format with the
// values of keys, uniqueId being the device's unique id and absent attributes "".
func FormatCommand(c *model.Command, uniqueID, format string, keys ...string) string {
	return FormatCommandWith(c, uniqueID, format, nil, keys...)
}

// FormatCommandWith is formatCommand with a ValueFormatter: it returns the text of a
// value, or false for the default (toString).
func FormatCommandWith(c *model.Command, uniqueID, format string, formatter func(key string, value any) (string, bool), keys ...string) string {
	values := make([]any, len(keys))
	for i, key := range keys {
		if key == model.CommandKeyUniqueID {
			values[i] = uniqueID
			continue
		}
		v, _ := c.Attributes.Get(key)
		value, ok := "", false
		if formatter != nil {
			value, ok = formatter(key, v)
		}
		if !ok && v != nil {
			value, ok = model.JavaToString(v), true
		}
		values[i] = value
	}
	return Format(format, values...)
}

// javaDoubleToInt is Java's (int) cast of a double.
func javaDoubleToInt(f float64) int32 {
	switch {
	case f != f:
		return 0
	case f >= 2147483647:
		return 2147483647
	case f <= -2147483648:
		return -2147483648
	}
	return int32(f)
}
