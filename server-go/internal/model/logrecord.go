package model

import "strconv"

// LogRecord is a raw message a device sent, as the WebSocket's "logs" carry it: the
// protocol, the device's unique id and id when its connection has a session (else the
// unknown unique id, or null and 0), the data (hex, or text with logger.decodeTextData) and
// the remote host.
type LogRecord struct {
	Protocol string
	UniqueID *string
	DeviceID int64
	Data     string
	Host     string
}

// MarshalJSON writes the record as Jackson does: field-backed properties in declaration
// order, then the getter-only "host".
func (r LogRecord) MarshalJSON() ([]byte, error) {
	b := []byte(`{"protocol":`)
	b = appendString(b, r.Protocol)
	b = append(b, `,"uniqueId":`...)
	if r.UniqueID == nil {
		b = append(b, "null"...)
	} else {
		b = appendString(b, *r.UniqueID)
	}
	b = append(b, `,"deviceId":`...)
	b = strconv.AppendInt(b, r.DeviceID, 10)
	b = append(b, `,"data":`...)
	b = appendString(b, r.Data)
	b = append(b, `,"host":`...)
	b = appendString(b, r.Host)
	return append(b, '}'), nil
}
