// Package rftrack is the RfTrack protocol (RfTrackProtocol): form-encoded HTTP posts with
// JSON location, cell and Wi-Fi values.
package rftrack

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "rftrack",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is RfTrackProtocolDecoder.
type Decoder struct{}

func readArray(value string) []any {
	a, ok := kit.ParseJSON([]byte(value)).([]any)
	if !ok {
		kit.Throw("JsonParsingException: not an array")
	}
	return a
}

// DecodeHTTP decodes one request; a panic is where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	params, err := kit.QueryParams(kit.Latin1(body), false)
	if err != nil {
		return nil, nil, err
	}

	pos := m.NewPosition()
	network := model.NewNetwork()

	for _, p := range params {
		for _, value := range p.Values {
			switch p.Name {
			case "i":
				ds := m.Session(value)
				if ds == nil {
					return &ingest.HTTPResponse{Status: http.StatusBadRequest}, nil, nil
				}
				pos.DeviceID = ds.DeviceID
			case "v":
				pos.Set(model.KeyVersionFw, value)
			case "t":
				pos.DeviceTime = model.NewTime(time.UnixMilli(kit.Long(value)))
			case "bat":
				battery := kit.Int(value)
				pos.Set(model.KeyBatteryLevel, battery&0xff)
				pos.Set("plugStatus", (battery>>8)&0x0f)
				pos.Set(model.KeyCharge, ((battery>>12)&0x0f) == 1)
			case "id":
				pos.Set("braceletId", value)
			case "rc":
				braceletCode := kit.Int(value)
				pos.Set("braceletCode", braceletCode&0xffff)
				pos.Set("braceletStatus", braceletCode>>16)
			case "idt":
				braceletTime := kit.Long(value)
				pos.Set("lastHeartbeat", (braceletTime>>45)*10)
				pos.Set("lastPaired", ((braceletTime>>30)&0xffff)*10)
				pos.Set("lastUnpaired", ((braceletTime>>15)&0xffff)*10)
			case "mt":
				vibrationTime := kit.Int(value)
				pos.Set("vibrationDevice", (vibrationTime&0x7fff)*10)
				pos.Set("vibrationBracelet", (vibrationTime>>15)*10)
			case "gps":
				location := kit.ParseJSONObject([]byte(value))
				pos.Valid = true
				pos.Accuracy = location.Number("a").Double()
				pos.SetLongitude(location.Number("x").Double())
				pos.SetLatitude(location.Number("y").Double())
				pos.Altitude = location.Number("z").Double()
				pos.FixTime = model.NewTime(time.UnixMilli(location.Number("t").Long()))
			case "gsm":
				cellInfo := kit.ParseJSONObject([]byte(value))
				mcc := cellInfo.Int("c")
				mnc := cellInfo.Int("n")
				for _, v := range cellInfo.Array("b") {
					cell := kit.JSONObjectOf(v)
					network.AddCellTower(model.NewCellTowerRSSI(
						mcc, mnc, cell.Int("l"), int64(cell.Int("c")), cell.Int("b")))
				}
			case "dbm":
				pos.Set(model.KeyRssi, kit.Int(value))
			case "bar":
				pos.Set("pressure", kit.Double(value))
			case "cob":
				pos.Set("pressureChanges", value)
			case "wifi":
				for _, v := range readArray(value) {
					wifi := kit.JSONObjectOf(v)
					network.AddWifiAccessPoint(model.NewWifiAccessPoint(
						strings.ReplaceAll(wifi.String("m"), "-", ":"), wifi.Int("l")))
				}
			case "u_ids":
				pos.Set("unpairedIds", value)
			}
		}
	}

	if pos.FixTime.IsZero() {
		if pos.DeviceTime.IsZero() {
			m.LastLocation(pos, nil)
		} else {
			t := pos.DeviceTime.Time
			m.LastLocation(pos, &t)
		}
	}

	if network.CellTowers != nil || network.WifiAccessPoints != nil {
		pos.Network = network
	}

	response := "{}"
	commands, err := m.QueuedCommands(pos.DeviceID, 1)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range commands {
		data := c.Attributes.JavaString(model.CommandKeyData)
		if data == nil {
			kit.Throw("NullPointerException: command data")
		}
		response = *data
	}
	return &ingest.HTTPResponse{Status: http.StatusOK, Body: []byte(response)}, []*model.Position{pos}, nil
}
