// Package sanul is the Sanul protocol (SanulProtocol): little-endian binary messages with
// a login and location heartbeats (no coordinates).
package sanul

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "sanul",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			f := ingest.NewLengthFramer(ingest.MaxFrameLength, 3, 2, 0, 0)
			f.LittleEndian = true
			return ingest.Pipeline{Framer: f, Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgLogin    = 0
	msgLocation = 1
	msgResponse = 5
)

func sendResponse(m *ingest.Message, typ int) {
	response := kit.Empty()
	response.WriteByte8(0xaa)     // header
	response.WriteShortLE(0x85da) // reserved
	response.WriteShortLE(15)     // length
	response.WriteByte8(1)        // edition
	response.WriteShortLE(msgResponse)
	response.WriteShortLE(typ)
	response.WriteIntLE(0)       // command id
	response.WriteByte8(0)       // status
	response.WriteByte8(0)       // result length
	response.WriteIntLE(0x20000) // result data ?
	m.Send(response)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedByte()    // header
	buf.ReadUnsignedShortLE() // reserved
	buf.ReadUnsignedShortLE() // length
	buf.ReadUnsignedByte()    // edition

	typ := buf.ReadUnsignedShortLE()

	buf.ReadUnsignedIntLE() // command id

	sendResponse(m, typ)

	switch typ {
	case msgLogin:
		m.Session(kit.Trim(kit.Latin1(buf.Bytes())))
	case msgLocation:
		ds := m.Session()
		if ds == nil {
			return nil
		}
		pos := m.NewPosition()
		pos.DeviceID = ds.DeviceID
		m.LastLocation(pos, nil)
		return pos
	}
	return nil
}
