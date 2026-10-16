// Package vlt is the VLT protocol (VltProtocol): fixed-width records in the "vltdata"
// form parameter of HTTP requests.
package vlt

import (
	"context"
	"net/http"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:    "vlt",
		Servers: []ingest.Server{{HTTP: Decoder{}}},
	}
}

// Decoder is VltProtocolDecoder.
type Decoder struct{}

var pattern = kit.NewPattern().
	Number("(dd)").              // alert id
	Expression("([HL])").        // history
	Number("([01])").            // validity
	Number("(dd)(dd)(dd)").      // date (ddmmyy)
	Number("(dd)(dd)(dd)").      // time (hhmmss)
	Number("(d{3}.d{6})([NS])"). // latitude
	Number("(d{3}.d{6})([EW])"). // longitude
	Number("(d{3})").            // mcc
	Expression("(x*[0-9]+)").    // mnc
	Number("(x{4})").            // lac
	Number("(d{9})").            // cid
	Number("(d{3}.d{2})").       // speed
	Number("(d{3}.d{2})").       // course
	Number("(d{2})").            // satellites
	Number("(d{2})").            // hdop
	Number("(d{2})").            // rssi
	Number("([01])").            // ignition
	Number("([01])").            // charging
	Expression("([HMS])").       // vehicle mode
	Compile()

func decodePosition(m *ingest.Message, ds *ingest.DeviceSession, sentence string) *model.Position {
	parser := kit.NewParser(pattern, sentence)
	if !parser.Matches() {
		return nil
	}

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	pos.Set(model.KeyEvent, parser.NextInt())
	var archive *bool
	if parser.Next() == "H" {
		archive = new(true)
	}
	pos.Set(model.KeyArchive, archive)

	pos.Valid = parser.MustInt() > 0
	pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))
	pos.SetLatitude(parser.NextCoordinate(kit.DegHem))
	pos.SetLongitude(parser.NextCoordinate(kit.DegHem))

	mcc := parser.MustInt()
	mnc := kit.Int(strings.ReplaceAll(parser.Next(), "x", ""))
	lac := parser.MustHexInt()
	cid := parser.MustInt()

	pos.Speed = parser.MustDouble()
	pos.Course = parser.MustDouble()

	pos.Set(model.KeySatellites, parser.NextInt())
	pos.Set(model.KeyHdop, parser.NextInt())

	pos.Network = model.NetworkOf(model.NewCellTowerRSSI(mcc, mnc, lac, int64(cid), parser.MustInt()))

	pos.Set(model.KeyIgnition, parser.MustInt() > 0)
	pos.Set(model.KeyCharge, parser.MustInt() > 0)
	pos.Set(model.KeyMotion, parser.Next() == "M")

	return pos
}

// DecodeHTTP decodes one request: 400 for an unknown device, 200 otherwise. Errors are
// where the Java decoder throws.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, _ *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)

	params, err := kit.QueryParams(kit.Latin1(body), false)
	if err != nil {
		return nil, nil, err
	}
	sentence := ""
	found := false
	for _, p := range params {
		if p.Name == "vltdata" {
			sentence, found = p.Values[0], true
			break
		}
	}
	if !found {
		kit.Throw("NullPointerException: vltdata")
	}

	index := 0
	typ := kit.Substring(sentence, index, index+3)
	index += 3
	imei := kit.Substring(sentence, index, index+15)
	index += 15

	ds := m.Session(imei)
	if ds == nil {
		return &ingest.HTTPResponse{Status: http.StatusBadRequest}, nil, nil
	}

	ok := &ingest.HTTPResponse{Status: http.StatusOK}

	switch typ {
	case "NRM":
		if pos := decodePosition(m, ds, kit.SubstringFrom(sentence, 3+15)); pos != nil {
			return ok, []*model.Position{pos}, nil
		}
	case "BTH":
		positions = []*model.Position{}
		count := kit.Int(kit.Substring(sentence, index, index+3))
		index += 3
		for range count {
			// A record that doesn't match is a null element in Java's list; it is
			// dropped here.
			if pos := decodePosition(m, ds, kit.Substring(sentence, index, index+78)); pos != nil {
				positions = append(positions, pos)
			}
			index += 78
		}
		return ok, positions, nil
	}
	return ok, nil, nil
}
