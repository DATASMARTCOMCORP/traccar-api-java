// Package hoopo is the Hoopo protocol (HoopoProtocol): JSON event objects over TCP.
package hoopo

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "hoopo",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.JSONFramer(),
				Text:    true,
				TextOut: true,
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

func decode(m *ingest.Message, msg any) any {
	root := readObject(msg.(string))

	ds := m.Session(jsonString(root, "deviceId"))
	if ds == nil {
		return nil
	}

	if _, ok := root["eventData"]; !ok {
		return nil
	}
	eventData := jsonObject(root, "eventData")

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	pos.SetTime(parseOffsetDateTime(jsonString(root, "eventTime")))
	pos.Valid = true
	pos.SetLatitude(jsonDouble(eventData, "latitude"))
	pos.SetLongitude(jsonDouble(eventData, "longitude"))
	pos.Set(model.KeyEvent, jsonString(eventData, "eventType"))
	pos.Set(model.KeyBatteryLevel, jsonInt(eventData, "batteryLevel"))

	if _, ok := root["movement"]; ok {
		pos.Speed = float64(jsonInt(jsonObject(root, "movement"), "Speed"))
	}
	return pos
}

// readObject is Json.createReader(...).readObject(): numbers are kept as their text.
func readObject(s string) map[string]any {
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var v map[string]any
	if err := d.Decode(&v); err != nil || v == nil {
		kit.Throw("JsonParsingException: %v", err)
	}
	return v
}

// jsonObject is JsonObject.getJsonObject: a missing key is null (a NullPointerException
// when used), another type a ClassCastException.
func jsonObject(o map[string]any, key string) map[string]any {
	v, ok := o[key].(map[string]any)
	if !ok {
		kit.Throw("ClassCastException: %s is not an object", key)
	}
	return v
}

// jsonString is JsonObject.getString (throws for a missing or non-string value).
func jsonString(o map[string]any, key string) string {
	v, ok := o[key].(string)
	if !ok {
		kit.Throw("ClassCastException: %s is not a string", key)
	}
	return v
}

func jsonNumber(o map[string]any, key string) json.Number {
	v, ok := o[key].(json.Number)
	if !ok {
		kit.Throw("ClassCastException: %s is not a number", key)
	}
	return v
}

// jsonDouble is getJsonNumber(key).doubleValue() (BigDecimal.doubleValue: the nearest
// double).
func jsonDouble(o map[string]any, key string) float64 {
	f, _ := strconv.ParseFloat(string(jsonNumber(o, key)), 64) // ±Inf out of range, as Java
	return f
}

// jsonInt is JsonObject.getInt: BigDecimal.intValue, the fraction dropped and the low 32
// bits kept.
func jsonInt(o map[string]any, key string) int {
	r, ok := new(big.Rat).SetString(string(jsonNumber(o, key)))
	if !ok {
		kit.Throw("NumberFormatException: %s", key)
	}
	q := new(big.Int).Quo(r.Num(), r.Denom())
	low := new(big.Int).And(q, big.NewInt(0xFFFFFFFF))
	return int(int32(uint32(low.Uint64())))
}

// parseOffsetDateTime is OffsetDateTime.parse (ISO_OFFSET_DATE_TIME): seconds and the
// fraction are optional.
func parseOffsetDateTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.999999999Z07:00", "2006-01-02T15:04Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	kit.Throw("DateTimeParseException: Text '%s' could not be parsed", s)
	return time.Time{}
}
