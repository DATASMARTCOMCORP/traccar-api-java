// Package pui is the PUI protocol (PuiProtocol): JSON reports published over MQTT.
package pui

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: MQTT over TCP.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "pui",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			codec := ingest.NewMQTT()
			return ingest.Pipeline{Framer: codec, Decoder: ingest.MQTTDecoder(codec, decode)}
		}}},
	}
}

// jakarta.json accessors: a missing member or one of another type throws.
func member[T any](o map[string]any, key string) T {
	v, ok := o[key].(T)
	if !ok {
		kit.Throw("JSON member %q: %T", key, o[key])
	}
	return v
}

func number(o map[string]any, key string) json.Number { return member[json.Number](o, key) }

// intValue is JsonNumber.intValue (BigDecimal.intValue: the fraction dropped, the low 32
// bits kept).
func intValue(n json.Number) int {
	if v, err := n.Int64(); err == nil {
		return int(int32(v))
	}
	f := kit.Double(n.String())
	return int(int32(int64(f)))
}

func decode(m *ingest.Message, ds *ingest.DeviceSession, publish *ingest.MQTTMessage) any {
	dec := json.NewDecoder(bytes.NewReader(publish.Payload.Bytes()))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		panic(fmt.Errorf("JsonParsingException: %w", err))
	}
	o, ok := root.(map[string]any)
	if !ok {
		kit.Throw("JsonException: not an object")
	}
	switch member[string](o, "rpt") {
	case "hf", "loc":
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		pos.Valid = true
		// The pattern ends in a quoted 'Z': the time is read in the server's zone.
		pos.SetTime(kit.NewDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", m.Zone()).Parse(member[string](o, "ts")))
		location := member[map[string]any](o, "location")
		pos.SetLatitude(kit.Double(number(location, "lat").String()))
		pos.SetLongitude(kit.Double(number(location, "lon").String()))
		pos.Course = float64(intValue(number(o, "bear")))
		pos.Speed = units.KnotsFromCps(float64(intValue(number(o, "spd"))))
		pos.Set(model.KeyIgnition, member[string](o, "ign") == "on")
		return pos
	}
	return nil
}
