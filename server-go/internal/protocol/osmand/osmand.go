// Package osmand decodes the OsmAnd protocol (OsmAndProtocolDecoder): positions sent
// over HTTP as query parameters, a form body, or the JSON of the Transistorsoft
// background-geolocation clients.
package osmand

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Decoder is the OsmAnd decoder; osmand.minAccuracy applies to JSON accuracies.
type Decoder struct {
	minAccuracy float64
	zone        *time.Location // the JVM's default zone, for "yyyy-MM-dd HH:mm:ss" times
}

// New reads the decoder's options.
func New(m interface {
	Float(string, float64) (float64, error)
}, zone *time.Location) (*Decoder, error) {
	minAccuracy, err := m.Float("osmand.minAccuracy", 10)
	if err != nil {
		return nil, err
	}
	return &Decoder{minAccuracy: minAccuracy, zone: zone}, nil
}

var (
	ok         = &ingest.HTTPResponse{Status: http.StatusOK}
	badRequest = &ingest.HTTPResponse{Status: http.StatusBadRequest}
	notFound   = &ingest.HTTPResponse{Status: http.StatusNotFound}
)

// DecodeHTTP decodes one request. Errors are the cases where the Java decoder throws; the
// connection is then closed without a response.
func (d *Decoder) DecodeHTTP(ctx context.Context, m *ingest.Message, r *http.Request, body []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return d.decodeJSON(ctx, m, body)
	}
	params, err := queryParams(latin1(r.RequestURI), true)
	if err != nil {
		return nil, nil, err
	}
	if len(params) == 0 {
		if params, err = queryParams(usASCII(body), false); err != nil {
			return nil, nil, err
		}
	}
	return d.decodeQuery(ctx, m, params)
}

// latin1 is how Netty's HTTP decoder turns the request line into a Java string: one char
// per byte.
func latin1(s string) string {
	if isASCII(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteRune(rune(s[i]))
	}
	return b.String()
}

// usASCII is ByteBuf.toString(US_ASCII): bytes above 0x7F become U+FFFD.
func usASCII(body []byte) string {
	if isASCII(string(body)) {
		return string(body)
	}
	var b strings.Builder
	for _, c := range body {
		if c < 0x80 {
			b.WriteByte(c)
		} else {
			b.WriteRune(utf8.RuneError)
		}
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func (d *Decoder) decodeQuery(ctx context.Context, m *ingest.Message, params []param) (*ingest.HTTPResponse, []*model.Position, error) {
	pos := m.NewPosition()
	pos.Valid = true
	towers := model.NewCellTowerSet()
	wifis := model.NewWifiAccessPointSet()
	var latitude, longitude *float64

	for _, p := range params {
		for _, value := range p.values {
			switch p.name {
			case "id", "deviceid":
				ds, err := m.DeviceSession(ctx, value)
				if err != nil {
					return nil, nil, err
				}
				if ds == nil {
					return badRequest, nil, nil
				}
				pos.DeviceID = ds.DeviceID
			case "notificationToken":
				if pos.DeviceID > 0 {
					if err := m.UpdateNotificationToken(ctx, pos.DeviceID, value); err != nil {
						return nil, nil, err
					}
				}
			case "valid":
				pos.Valid = model.ParseBoolean(value) || value == "1"
			case "timestamp":
				t, err := parseTimestamp(value, d.zone)
				if err != nil {
					return nil, nil, err
				}
				pos.DeviceTime, pos.FixTime = t, t
			case "lat":
				v, err := model.ParseDouble(value)
				if err != nil {
					return nil, nil, err
				}
				latitude = &v
			case "lon":
				v, err := model.ParseDouble(value)
				if err != nil {
					return nil, nil, err
				}
				longitude = &v
			case "location":
				parts := javaSplit(value, ",")
				if len(parts) < 2 {
					return nil, nil, errors.New("location: index out of bounds")
				}
				lat, err := model.ParseDouble(parts[0])
				if err != nil {
					return nil, nil, err
				}
				lon, err := model.ParseDouble(parts[1])
				if err != nil {
					return nil, nil, err
				}
				latitude, longitude = &lat, &lon
			case "cell":
				tower, err := parseCell(value)
				if err != nil {
					return nil, nil, err
				}
				towers.Add(tower)
			case "wifi":
				ap, err := parseWifi(value)
				if err != nil {
					return nil, nil, err
				}
				wifis.Add(ap)
			case "speed":
				v, err := model.ParseDouble(value)
				if err != nil {
					return nil, nil, err
				}
				pos.Speed = m.ConvertSpeed(v, "kn")
			case "bearing", "heading":
				if err := setDouble(&pos.Course, value); err != nil {
					return nil, nil, err
				}
			case "altitude":
				if err := setDouble(&pos.Altitude, value); err != nil {
					return nil, nil, err
				}
			case "accuracy":
				if err := setDouble(&pos.Accuracy, value); err != nil {
					return nil, nil, err
				}
			case "hdop", "batt":
				v, err := model.ParseDouble(value)
				if err != nil {
					return nil, nil, err
				}
				key := model.KeyHdop
				if p.name == "batt" {
					key = model.KeyBatteryLevel
				}
				pos.Attributes.Set(key, v)
			case "driverUniqueId":
				setString(pos, model.KeyDriverUniqueID, value)
			case "charge":
				pos.Attributes.Set(model.KeyCharge, model.ParseBoolean(value))
			default:
				if v, err := model.ParseDouble(value); err == nil {
					pos.Attributes.Set(p.name, v)
				} else {
					switch value {
					case "true":
						pos.Attributes.Set(p.name, true)
					case "false":
						pos.Attributes.Set(p.name, false)
					default:
						setString(pos, p.name, value)
					}
				}
			}
		}
	}

	if pos.FixTime.IsZero() {
		now := model.NewTime(time.Now())
		pos.DeviceTime, pos.FixTime = now, now
	}
	if towers.Len() > 0 || wifis.Len() > 0 {
		network := model.NewNetwork()
		if towers.Len() > 0 {
			network.CellTowers = towers.Items()
		}
		if wifis.Len() > 0 {
			network.WifiAccessPoints = wifis.Items()
		}
		pos.Network = network
	}
	if latitude != nil && longitude != nil {
		if err := setCoordinates(pos, *latitude, *longitude); err != nil {
			return nil, nil, err
		}
	} else {
		deviceTime := pos.DeviceTime.Time
		m.LastLocation(pos, &deviceTime)
	}

	if pos.DeviceID == 0 {
		return badRequest, nil, nil
	}
	commands, err := m.QueuedCommands(ctx, pos.DeviceID, 1)
	if err != nil {
		return nil, nil, err
	}
	var response *string
	for _, c := range commands {
		response = c.Attributes.JavaString("data")
	}
	if response != nil {
		return &ingest.HTTPResponse{Status: http.StatusOK, Body: []byte(*response)}, []*model.Position{pos}, nil
	}
	return ok, []*model.Position{pos}, nil
}

func setDouble(dst *float64, value string) error {
	v, err := model.ParseDouble(value)
	if err == nil {
		*dst = v
	}
	return err
}

// setString is Position.set(key, String): empty values are not stored.
func setString(pos *model.Position, key, value string) {
	if value != "" {
		pos.Attributes.Set(key, value)
	}
}

// setCoordinates is setLatitude and setLatitude, which throw outside the valid ranges.
func setCoordinates(pos *model.Position, latitude, longitude float64) error {
	if latitude < -90 || latitude > 90 {
		return errors.New("Latitude out of range")
	}
	pos.Latitude = latitude
	if longitude < -180 || longitude > 180 {
		return errors.New("Longitude out of range")
	}
	pos.Longitude = longitude
	return nil
}

// parseTimestamp reads seconds or milliseconds since the epoch, an ISO date with an
// offset, or "yyyy-MM-dd HH:mm:ss" in the server's zone.
func parseTimestamp(value string, zone *time.Location) (model.Time, error) {
	if n, err := model.ParseLong(value); err == nil {
		if n < math.MaxInt32 {
			n *= 1000
		}
		return model.NewTime(time.UnixMilli(n)), nil
	}
	var t time.Time
	var err error
	if strings.Contains(value, "T") {
		t, err = javatime.ParseISOZoned(value)
	} else {
		t, err = javatime.ParseLocal(value, zone)
	}
	if err != nil {
		return model.Time{}, fmt.Errorf("timestamp %q: %w", value, err)
	}
	return model.NewTime(t), nil
}

// javaSplit is String.split for a literal one-character separator.
func javaSplit(s, sep string) []string {
	if s == "" {
		return []string{""}
	}
	parts := strings.Split(s, sep)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// parseCell reads "mcc,mnc,lac,cid[,rssi]" with Integer.parseInt (the cell id too).
func parseCell(value string) (model.CellTower, error) {
	parts := javaSplit(value, ",")
	if len(parts) < 4 {
		return model.CellTower{}, errors.New("cell: index out of bounds")
	}
	n := 4
	if len(parts) > 4 {
		n = 5
	}
	values := make([]int64, n)
	for i := range values {
		v, err := model.ParseInt(parts[i])
		if err != nil {
			return model.CellTower{}, err
		}
		values[i] = int64(v)
	}
	tower := model.CellTower{MobileCountryCode: &values[0], MobileNetworkCode: &values[1],
		LocationAreaCode: &values[2], CellID: &values[3]}
	if n == 5 {
		tower.SignalStrength = &values[4]
	}
	return tower, nil
}

// parseWifi reads "mac,rssi"; dashes in the MAC address become colons.
func parseWifi(value string) (model.WifiAccessPoint, error) {
	parts := javaSplit(value, ",")
	if len(parts) < 2 {
		return model.WifiAccessPoint{}, errors.New("wifi: index out of bounds")
	}
	rssi, err := model.ParseInt(parts[1])
	if err != nil {
		return model.WifiAccessPoint{}, err
	}
	mac := strings.ReplaceAll(parts[0], "-", ":")
	signal := int64(rssi)
	return model.WifiAccessPoint{MacAddress: &mac, SignalStrength: &signal}, nil
}

// decodeJSON decodes the Transistorsoft JSON format.
func (d *Decoder) decodeJSON(ctx context.Context, m *ingest.Message, body []byte) (*ingest.HTTPResponse, []*model.Position, error) {
	root, err := parseJSONObject(string(body))
	if err != nil {
		return nil, nil, err
	}
	deviceID, err := root.getString("device_id")
	if err != nil {
		return nil, nil, err
	}
	ds, err := m.DeviceSession(ctx, deviceID)
	if err != nil {
		return nil, nil, err
	}
	if ds == nil {
		return notFound, nil, nil
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	location, err := root.getObject("location")
	if err != nil {
		return nil, nil, err
	}
	timestamp, err := location.getString("timestamp")
	if err != nil {
		return nil, nil, err
	}
	t, err := javatime.ParseISOZoned(timestamp)
	if err != nil {
		return nil, nil, err
	}
	pos.DeviceTime, pos.FixTime = model.NewTime(t), model.NewTime(t)

	if location.has("coords") {
		coords, err := location.getObject("coords")
		if err != nil {
			return nil, nil, err
		}
		pos.Valid = true
		number := func(name string) (float64, error) {
			n, err := coords.getNumber(name)
			if err != nil {
				return 0, err
			}
			return n.double(), nil
		}
		lat, err := number("latitude")
		if err != nil {
			return nil, nil, err
		}
		if lat < -90 || lat > 90 {
			return nil, nil, errors.New("Latitude out of range")
		}
		pos.Latitude = lat
		lon, err := number("longitude")
		if err != nil {
			return nil, nil, err
		}
		if lon < -180 || lon > 180 {
			return nil, nil, errors.New("Longitude out of range")
		}
		pos.Longitude = lon
		speed, err := number("speed")
		if err != nil {
			return nil, nil, err
		}
		if speed >= 0 {
			pos.Speed = units.KnotsFromMps(speed)
		}
		heading, err := number("heading")
		if err != nil {
			return nil, nil, err
		}
		if heading >= 0 {
			pos.Course = heading
		}
		accuracy, err := number("accuracy")
		if err != nil {
			return nil, nil, err
		}
		if accuracy >= d.minAccuracy {
			pos.Accuracy = accuracy
		}
		altitude, err := number("altitude")
		if err != nil {
			return nil, nil, err
		}
		pos.Altitude = altitude
	} else {
		m.LastLocation(pos, nil)
	}

	if location.has("event") {
		v, err := location.getString("event")
		if err != nil {
			return nil, nil, err
		}
		setString(pos, model.KeyEvent, v)
	}
	if location.has("is_moving") {
		v, err := location.getBoolean("is_moving")
		if err != nil {
			return nil, nil, err
		}
		pos.Attributes.Set(model.KeyMotion, v)
	}
	if location.has("odometer") {
		n, err := location.getNumber("odometer")
		if err != nil {
			return nil, nil, err
		}
		pos.Attributes.Set(model.KeyOdometer, int64(n.intValue()))
	}
	if location.has("mock") {
		v, err := location.getBoolean("mock")
		if err != nil {
			return nil, nil, err
		}
		pos.Attributes.Set("mock", v)
	}
	if location.has("activity") {
		activity, err := location.getObject("activity")
		if err != nil {
			return nil, nil, err
		}
		v, err := activity.getString("type")
		if err != nil {
			return nil, nil, err
		}
		setString(pos, "activity", v)
	}
	if location.has("battery") {
		battery, err := location.getObject("battery")
		if err != nil {
			return nil, nil, err
		}
		level, err := battery.getNumber("level")
		if err != nil {
			return nil, nil, err
		}
		if l := level.double(); l >= 0 {
			pos.Attributes.Set(model.KeyBatteryLevel, int64(javaIntCast(l*100)))
		}
		charging, err := battery.getBoolean("is_charging")
		if err != nil {
			return nil, nil, err
		}
		if charging {
			pos.Attributes.Set(model.KeyCharge, true)
		}
	}
	if location.has("alarm") {
		v, err := location.getString("alarm")
		if err != nil {
			return nil, nil, err
		}
		setString(pos, model.KeyAlarm, v)
	}
	if location.has("extras") {
		extras, err := location.getObject("extras")
		if err != nil {
			return nil, nil, err
		}
		for _, name := range extras.keys {
			if name == "alarm" && location.has("alarm") {
				continue
			}
			switch v := extras.values[name].(type) {
			case jsonNumber:
				if v.integral() {
					pos.Attributes.Set(name, v.longValue())
				} else {
					pos.Attributes.Set(name, v.double())
				}
			case bool:
				pos.Attributes.Set(name, v)
			case string:
				setString(pos, name, v)
			}
		}
	}
	return ok, []*model.Position{pos}, nil
}

// javaIntCast is Java's (int) cast of a double: truncation, saturation, NaN to 0.
func javaIntCast(f float64) int32 {
	switch {
	case math.IsNaN(f):
		return 0
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	return int32(f)
}

// jsonNumber is a JSON number as Parsson keeps it: the decimal text (a BigDecimal).
type jsonNumber string

// double is BigDecimal.doubleValue: the nearest double (±Inf beyond the range).
func (n jsonNumber) double() float64 {
	v, _ := strconv.ParseFloat(string(n), 64)
	return v
}

// integral is JsonNumber.isIntegral: a BigDecimal scale of 0, i.e. no fraction and no
// exponent in the text.
func (n jsonNumber) integral() bool { return !strings.ContainsAny(string(n), ".eE") }

// truncated is the BigDecimal truncated toward zero.
func (n jsonNumber) truncated() *big.Int {
	r, ok := new(big.Rat).SetString(string(n))
	if !ok {
		return new(big.Int)
	}
	return new(big.Int).Quo(r.Num(), r.Denom())
}

// longValue and intValue are BigDecimal.longValue and intValue: the low bits of the
// truncated value.
func (n jsonNumber) longValue() int64 {
	return int64(new(big.Int).And(n.truncated(), new(big.Int).SetUint64(math.MaxUint64)).Uint64())
}

func (n jsonNumber) intValue() int32 { return int32(n.longValue()) }
