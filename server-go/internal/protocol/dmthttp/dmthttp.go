// Package dmthttp is the Digital Matter HTTP protocol (DmtHttpProtocol): JSON records
// posted over HTTP, in the "traditional" and the "edge" (Yabby Edge) formats.
package dmthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "dmthttp",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is DmtHttpProtocolDecoder.
type Decoder struct{}

var dateFormat = kit.NewDateFormat("yyyy-MM-dd HH:mm:ss", time.UTC)

// DecodeHTTP decodes one request: 200 when it gives positions, 400 otherwise. Errors are
// where the Java decoder throws (malformed JSON, missing or mistyped fields).
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)
	root := parseObject(kit.Latin1(body)) // content().toString(US_ASCII)
	if root.has("device") {
		if pos := decodeEdge(m, root); pos != nil {
			positions = []*model.Position{pos}
		}
	} else {
		positions = decodeTraditional(m, root)
	}
	status := http.StatusBadRequest
	if positions != nil {
		status = http.StatusOK
	}
	return &ingest.HTTPResponse{Status: status}, positions, nil
}

func decodeTraditional(m *ingest.Message, root object) []*model.Position {
	ds := m.Session(root.str("IMEI"))
	if ds == nil {
		return nil
	}
	positions := []*model.Position{}
	for _, record := range root.objects("Records") {
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		pos.Set(model.KeyIndex, record.int("SeqNo"))
		pos.Set(model.KeyEvent, record.int("Reason"))
		pos.DeviceTime = model.NewTime(dateFormat.Parse(record.str("DateUTC")))

		for _, field := range record.objects("Fields") {
			switch field.int("FType") {
			case 0:
				pos.FixTime = model.NewTime(dateFormat.Parse(field.str("GpsUTC")))
				pos.SetLatitude(field.double("Lat"))
				pos.SetLongitude(field.double("Long"))
				pos.Altitude = float64(field.int("Alt"))
				pos.Speed = units.KnotsFromCps(float64(field.int("Spd")))
				pos.Course = float64(field.int("Head"))
				pos.Accuracy = float64(field.int("PosAcc"))
				pos.Valid = field.int("GpsStat") > 0
			case 2:
				input := field.int("DIn")
				output := field.int("DOut")
				pos.Set(model.KeyIgnition, kit.Check(int64(input), 0))
				pos.Set(model.KeyInput, input)
				pos.Set(model.KeyOutput, output)
				pos.Set(model.KeyStatus, field.int("DevStat"))
			case 6:
				adc := field.object("AnalogueData")
				if adc.has("1") {
					pos.Set(model.KeyBattery, float64(adc.int("1"))/1000.0)
				}
				if adc.has("2") {
					pos.Set(model.KeyPower, float64(adc.int("2"))/100.0)
				}
				if adc.has("3") {
					pos.Set(model.KeyDeviceTemp, float64(adc.int("3"))/100.0)
				}
				if adc.has("4") {
					pos.Set(model.KeyRssi, adc.int("4"))
				}
				if adc.has("5") {
					pos.Set("solarPower", float64(adc.int("5"))/1000.0)
				}
			}
		}
		positions = append(positions, pos)
	}
	return positions
}

func decodeEdge(m *ingest.Message, root object) *model.Position {
	device := root.object("device")
	ds := m.Session(device.str("imei"))
	if ds == nil {
		return nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	t, err := time.Parse(time.RFC3339Nano, root.str("date")) // OffsetDateTime.parse
	if err != nil {
		kit.Throw("DateTimeParseException: %v", err)
	}
	if root.has("lat") && root.has("lng") {
		pos.Valid = true
		pos.SetTime(t)
		pos.SetLatitude(root.double("lat"))
		pos.SetLongitude(root.double("lng"))
		pos.Accuracy = root.double("posAcc")
	} else {
		m.LastLocation(pos, &t)
	}

	pos.Set(model.KeyIndex, root.int("sqn"))
	pos.Set(model.KeyEvent, root.int("reason"))

	if root.has("analogues") {
		for _, adc := range root.objects("analogues") {
			pos.Set(model.PrefixAdc+strconv.Itoa(adc.int("id")), adc.int("val"))
		}
	}
	if root.has("inputs") {
		input := root.int("inputs")
		pos.Set(model.KeyIgnition, kit.Check(int64(input), 0))
		pos.Set(model.KeyInput, input)
	}
	if root.has("outputs") {
		pos.Set(model.KeyOutput, root.int("outputs"))
	}
	if root.has("status") {
		pos.Set(model.KeyStatus, root.int("status"))
	}
	if root.has("counters") {
		for _, counter := range root.objects("counters") {
			switch id := counter.int("id"); id {
			case 0:
				pos.Set(model.KeyBattery, float64(counter.int("val"))/1000.0)
			case 1:
				pos.Set(model.KeyBatteryLevel, float64(counter.int("val"))/100.0)
			default:
				pos.Set("counter"+strconv.Itoa(id), counter.int("val"))
			}
		}
	}
	return pos
}

// object is a JSON object read as javax.json's JsonObject: the getters throw when a key
// is missing or has another type.
type object map[string]any

func parseObject(s string) object {
	d := json.NewDecoder(bytes.NewReader([]byte(s)))
	d.UseNumber()
	var v map[string]any
	if err := d.Decode(&v); err != nil || v == nil {
		kit.Throw("JsonParsingException: %v", err)
	}
	return v
}

func (o object) has(key string) bool { _, ok := o[key]; return ok }

func (o object) get(key string) any {
	v, ok := o[key]
	if !ok {
		kit.Throw("NullPointerException: %s", key)
	}
	return v
}

func (o object) str(key string) string {
	v, ok := o.get(key).(string)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return v
}

func (o object) number(key string) json.Number {
	v, ok := o.get(key).(json.Number)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return v
}

// int is getInt: JsonNumber.intValue, BigDecimal.intValue (the fraction dropped, the low
// 32 bits kept).
func (o object) int(key string) int {
	n := o.number(key)
	if v, err := n.Int64(); err == nil {
		return int(int32(v))
	}
	r, ok := new(big.Float).SetPrec(0).SetString(n.String())
	if !ok {
		kit.Throw("NumberFormatException: %s", n)
	}
	i, _ := r.Int(nil)
	return int(int32(i.Int64()))
}

// double is getJsonNumber(key).doubleValue().
func (o object) double(key string) float64 {
	v, err := strconv.ParseFloat(o.number(key).String(), 64)
	if err != nil {
		kit.Throw("NumberFormatException: %v", err)
	}
	return v
}

func (o object) object(key string) object {
	v, ok := o.get(key).(map[string]any)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	return v
}

func (o object) objects(key string) []object {
	list, ok := o.get(key).([]any)
	if !ok {
		kit.Throw("ClassCastException: %s", key)
	}
	out := make([]object, 0, len(list))
	for _, v := range list {
		obj, ok := v.(map[string]any)
		if !ok {
			kit.Throw("ClassCastException: %s", fmt.Sprint(v))
		}
		out = append(out, obj)
	}
	return out
}
