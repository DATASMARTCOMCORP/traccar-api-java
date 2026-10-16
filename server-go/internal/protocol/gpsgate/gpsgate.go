// Package gpsgate is the GpsGate protocol (GpsGateProtocol): $FRLIN/$FRVER/$GPRMC/$FRCMD
// text sentences over TCP.
package gpsgate

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "gpsgate",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewDelimiterFramer(ingest.MaxFrameLength, "\x00", "\n", "\r\n"),
				Text:    true,
				TextOut: true,
				Decoder: &Decoder{},
			}
		}}},
	}
}

var patternGPRMC = kit.NewPattern().
	Text("$GPRMC,").
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([AV]),").       // validity
	Number("(dd)(dd.d+),").      // latitude
	Expression("([NS]),").
	Number("(ddd)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.d+)?,").    // speed
	Number("(d+.d+)?,").    // course
	Number("(dd)(dd)(dd)"). // date (ddmmyy)
	Any().
	Compile()

var patternFRCMD = kit.NewPattern().
	Text("$FRCMD,").
	Number("(d+),").      // imei
	Expression("[^,]*,"). // command
	Expression("[^,]*,").
	Number("(d+)(dd.d+),"). // latitude
	Expression("([NS]),").
	Number("(d+)(dd.d+),"). // longitude
	Expression("([EW]),").
	Number("(d+.?d*),").         // altitude
	Number("(d+.?d*),").         // speed
	Number("(d+.?d*)?,").        // course
	Number("(dd)(dd)(dd),").     // date (ddmmyy)
	Number("(dd)(dd)(dd).?d*,"). // time (hhmmss)
	Expression("([01])").        // validity
	Any().
	Compile()

// Decoder is GpsGateProtocolDecoder. Its session id stands for Netty's
// channel.id().asShortText(), sent in the $FRSES reply: one per connection.
type Decoder struct {
	sessionID string
}

func send(m *ingest.Message, message string) {
	m.Send(message + kit.NMEA(kit.SubstringFrom(message, 1)) + "\r\n")
}

func (d *Decoder) shortChannelID() string {
	if d.sessionID == "" {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		d.sessionID = hex.EncodeToString(b)
	}
	return d.sessionID
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	sentence := msg.(string)

	switch {
	case strings.HasPrefix(sentence, "$FRLIN,"):
		beginIndex := kit.IndexOfFrom(sentence, ",", 7)
		if beginIndex == -1 {
			send(m, "$FRERR,AuthError,Parse error")
			return nil
		}
		beginIndex++
		endIndex := kit.IndexOfFrom(sentence, ",", beginIndex)
		if endIndex == -1 {
			send(m, "$FRERR,AuthError,Parse error")
			return nil
		}
		imei := kit.Substring(sentence, beginIndex, endIndex)
		if m.Session(imei) != nil {
			if m.HasChannel() {
				send(m, "$FRSES,"+d.shortChannelID())
			}
		} else {
			send(m, "$FRERR,AuthError,Unknown device")
		}

	case strings.HasPrefix(sentence, "$FRVER,"):
		send(m, "$FRVER,1,0,GpsGate Server 1.0")

	case strings.HasPrefix(sentence, "$GPRMC,"):
		ds := m.Session()
		if ds == nil {
			return nil
		}

		parser := kit.NewParser(patternGPRMC, sentence)
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		date := kit.NewDateBuilder(nil).
			SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

		pos.Valid = parser.Next() == "A"
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
		pos.Speed = parser.NextDoubleDefault(0)
		pos.Course = parser.NextDoubleDefault(0)

		date.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
		pos.SetTime(date.Date())

		return pos

	case strings.HasPrefix(sentence, "$FRCMD,"):
		parser := kit.NewParser(patternFRCMD, sentence)
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()

		ds := m.Session(parser.Next())
		if ds == nil {
			return nil
		}
		pos.DeviceID = ds.DeviceID

		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
		pos.Altitude = parser.NextDoubleDefault(0)
		pos.Speed = parser.NextDoubleDefault(0)
		pos.Course = parser.NextDoubleDefault(0)

		pos.SetTime(parser.NextDateTime(kit.DMY_HMS, nil, m.Now))

		pos.Valid = parser.Next() == "1"

		return pos
	}

	return nil
}
