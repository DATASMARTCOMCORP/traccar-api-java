// Package flexapi is the FlexApi protocol (FlexApiProtocol): InHand FlexAPI lines, a
// JSON object of an MQTT topic and its payload between a leading char and two trailing
// ones.
package flexapi

import (
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server of lines (US_ASCII strings).
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "flexapi",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLineFramer(ingest.MaxFrameLengthLarge),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

// text is the message as StringDecoder(US_ASCII) makes it: Netty decodes one char per
// byte.
func text(msg any) string {
	if b, ok := msg.(*kit.Buf); ok {
		return kit.Latin1(b.Bytes())
	}
	return msg.(string)
}

func object(v any) *jsonObject {
	o, ok := v.(*jsonObject)
	if !ok {
		kit.Throw("ClassCastException: %T is not a JsonObject", v)
	}
	return o
}

func (o *jsonObject) has(key string) bool {
	_, ok := o.values[key]
	return ok
}

func (o *jsonObject) getInt(key string) int       { return number(o.values[key]).intValue() }
func (o *jsonObject) getDouble(key string) float64 { return number(o.values[key]).double() }
func (o *jsonObject) getString(key string) string  { return str(o.values[key]) }

func seconds(v int) *time.Time {
	t := time.UnixMilli(int64(v) * 1000)
	return &t
}

func decode(m *ingest.Message, msg any) any {
	message := text(msg)
	root := object(parseJSON(kit.Substring(message, 1, kit.Len(message)-2)))

	topic := root.getString("topic")
	clientID := kit.Substring(topic, 3, kit.IndexOfFrom(topic, "/", 3))
	ds := m.Session(clientID)
	if ds == nil {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	payloadValue, ok := root.values["payload"]
	if !ok {
		kit.Throw("NullPointerException: payload")
	}
	payload := object(payloadValue)

	switch {
	case strings.Contains(topic, "/gnss/"):
		pos.Valid = true
		if payload.has("time") {
			pos.SetTime(*seconds(payload.getInt("time")))
			pos.SetLatitude(payload.getDouble("lat"))
			pos.SetLongitude(payload.getDouble("log"))
		} else {
			pos.SetTime(*seconds(payload.getInt("gnss.ts")))
			pos.SetLatitude(payload.getDouble("gnss.latitude"))
			pos.SetLongitude(payload.getDouble("gnss.longitude"))
		}
		pos.Valid = payload.getInt("gnss.fix") > 0
		pos.Altitude = payload.getDouble("gnss.altitude")
		pos.Speed = payload.getDouble("gnss.speed")
		pos.Course = payload.getDouble("gnss.heading")
		pos.Set(model.KeySatellites, payload.getInt("gnss.num_sv"))
		pos.Set(model.KeyHdop, payload.getDouble("gnss.hdop"))

	case strings.Contains(topic, "/cellular1/"):
		m.LastLocation(pos, seconds(payload.getInt("modem1.ts")))
		pos.Set("imei", payload.getString("modem1.imei"))
		pos.Set("imsi", payload.getString("modem1.imsi"))
		pos.Set(model.KeyIccid, payload.getString("modem1.iccid"))
		operator := payload.getString("modem1.operator")
		if operator != "" {
			tower := model.NewCellTowerRSSI(
				kit.Int(kit.Substring(operator, 0, 3)),
				kit.Int(kit.SubstringFrom(operator, 3)),
				kit.IntRadix(payload.getString("modem1.lac"), 16),
				int64(kit.IntRadix(payload.getString("modem1.cell_id"), 16)),
				payload.getInt("modem1.rssi"))
			var radio string
			switch payload.getInt("modem1.network") {
			case 1:
				radio = "gsm"
			case 2:
				radio = "wcdma"
			case 3:
				radio = "lte"
			}
			if radio != "" {
				tower.RadioType = &radio
			}
			pos.Network = model.NetworkOf(tower)
		}

	case strings.Contains(topic, "/obd/"):
		m.LastLocation(pos, seconds(payload.getInt("obd.ts")))
		if payload.has("obd.speed") {
			pos.Set(model.KeyObdSpeed, payload.getDouble("obd.speed"))
		}
		if payload.has("obd.odo") {
			pos.Set(model.KeyObdOdometer, payload.getInt("obd.odo"))
		}
		if payload.has("obd.rpm") {
			pos.Set(model.KeyRpm, payload.getInt("obd.rpm"))
		}
		if payload.has("obd.vin") {
			pos.Set(model.KeyVin, payload.getString("obd.vin"))
		}

	case strings.Contains(topic, "/motion/"):
		m.LastLocation(pos, seconds(payload.getInt("motion.ts")))
		for _, axis := range []string{"ax", "ay", "az", "gx", "gy", "gz"} {
			pos.Set(axis, payload.getDouble("motion."+axis))
		}

	case strings.Contains(topic, "/io/"):
		m.LastLocation(pos, seconds(payload.getInt("io.ts")))
		if payload.has("io.IGT") {
			pos.Set(model.KeyIgnition, payload.getInt("io.IGT") > 0)
		}
		for _, key := range payload.keys {
			switch {
			case strings.HasPrefix(key, "io.AI"):
				pos.Set(model.PrefixAdc+kit.SubstringFrom(key, 5), payload.getDouble(key))
			case strings.HasPrefix(key, "io.DI") && !strings.HasSuffix(key, "_pullup"):
				pos.Set(model.PrefixIn+kit.SubstringFrom(key, 5), payload.getInt(key) > 0)
			case strings.HasPrefix(key, "io.DO"):
				pos.Set(model.PrefixOut+kit.SubstringFrom(key, 5), payload.getInt(key) > 0)
			}
		}

	case strings.Contains(topic, "/sysinfo/"):
		m.LastLocation(pos, seconds(payload.getInt("sysinfo.ts")))
		pos.Set("serial", payload.getString("sysinfo.serial_number"))
		pos.Set(model.KeyVersionFw, payload.getString("sysinfo.firmware_version"))

	default:
		return nil
	}

	return pos
}
