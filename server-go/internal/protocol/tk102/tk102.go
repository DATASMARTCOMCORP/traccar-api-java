// Package tk102 is the TK102 protocol (Tk102Protocol): "[" type, sequence, length,
// data "]" frames; the data of reports is a text "(...)".
package tk102

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "tk102",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  ingest.NewLengthFramer(ingest.MaxFrameLength, 1+1+10, 1, 1, 0),
				Decoder: ingest.DecoderFunc(decode),
			}
		}}},
	}
}

const (
	msgLoginRequest      = 0x80
	msgLoginRequest2     = 0x21
	msgLoginResponse     = 0x00
	msgHeartbeatRequest  = 0xF0
	msgHeartbeatResponse = 0xFF

	modeGPRS = 0x30
)

var pattern = kit.NewPattern().
	Text("(").
	Expression("[A-Z]+").
	Number("(dd)(dd)(dd)").         // time (hhmmss)
	Expression("([AV])").           // validity
	Number("(dd)(dd.dddd)([NS])").  // latitude
	Number("(ddd)(dd.dddd)([EW])"). // longitude
	Number("(ddd.ddd)").            // speed
	Number("(dd)(dd)(dd)").         // date (ddmmyy)
	Any().
	Text(")").
	Compile()

func sendResponse(m *ingest.Message, typ int, dataSequence, content []byte) {
	response := kit.Empty()
	response.WriteByte8('[')
	response.WriteByte8(typ)
	response.WriteBytes(dataSequence)
	response.WriteByte8(len(content))
	response.WriteBytes(content)
	response.WriteByte8(']')
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.Skip(1) // header
	typ := buf.ReadUnsignedByte()
	dataSequence := buf.ReadBytes(10)
	length := buf.ReadUnsignedByte()

	switch typ {
	case msgLoginRequest, msgLoginRequest2:
		data := buf.ReadSlice(length)
		var id string
		if typ == msgLoginRequest {
			id = kit.Latin1(data.Bytes())
		} else {
			id = data.GetString(data.ReaderIndex()+1, 15)
		}
		if m.Session(id) != nil {
			response := append([]byte{modeGPRS}, data.Bytes()...)
			sendResponse(m, msgLoginResponse, dataSequence, response)
		}
	case msgHeartbeatRequest:
		sendResponse(m, msgHeartbeatResponse, dataSequence, buf.ReadBytes(length))
	default:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		parser := kit.NewParser(pattern, buf.ReadString(length))
		if !parser.Matches() {
			return nil
		}

		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID

		db := kit.NewDateBuilder(nil).
			SetTime(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))

		pos.Valid = parser.Next() == "A"
		pos.SetLatitude(parser.NextCoordinate(kit.DegMinHem))
		pos.SetLongitude(parser.NextCoordinate(kit.DegMinHem))
		pos.Speed = parser.NextDoubleDefault(0)

		db.SetDateReverse(parser.NextIntDefault(0), parser.NextIntDefault(0), parser.NextIntDefault(0))
		pos.SetTime(db.Date())
		return pos
	}
	return nil
}
