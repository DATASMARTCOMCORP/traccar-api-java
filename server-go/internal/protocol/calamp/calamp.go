// Package calamp is the CalAmp protocol (CalAmpProtocol): LMD binary messages over UDP.
package calamp

import (
	"encoding/hex"
	"strconv"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "calamp",
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode)}
		}}},
	}
}

const (
	msgNull            = 0
	msgAck             = 1
	msgEventReport     = 2
	msgIDReport        = 3
	msgUserData        = 4
	msgAppData         = 5
	msgConfig          = 6
	msgUnitRequest     = 7
	msgLocateReport    = 8
	msgUserDataAcc     = 9
	msgMiniEventReport = 10
	msgMiniUserData    = 11

	serviceUnacknowledged = 0
	serviceAcknowledged   = 1
	serviceResponse       = 2
)

func sendResponse(m *ingest.Message, typ, index, result int) {
	response := kit.Empty()
	response.WriteByte8(serviceResponse)
	response.WriteByte8(msgAck)
	response.WriteShort(index)
	response.WriteByte8(typ)
	response.WriteByte8(result)
	response.WriteByte8(0)
	response.WriteMedium(0)
	m.Send(response)
}

func decodePosition(m *ingest.Message, ds *ingest.DeviceSession, typ int, buf *kit.Buf) *model.Position {
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	mini := typ == msgMiniEventReport

	pos.SetTime(time.UnixMilli(buf.ReadUnsignedInt() * 1000))
	if !mini {
		buf.ReadUnsignedInt() // fix time
	}
	pos.SetLatitude(float64(buf.ReadInt()) / 10000000.0)
	pos.SetLongitude(float64(buf.ReadInt()) / 10000000.0)
	if !mini {
		pos.Altitude = float64(buf.ReadInt()) / 100.0
		pos.Speed = units.KnotsFromCps(float64(buf.ReadUnsignedInt()))
	}
	pos.Course = float64(buf.ReadShort())
	if mini {
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
	}

	if mini {
		pos.Set(model.KeySatellites, buf.GetUnsignedByte(buf.ReaderIndex())&0xf)
		pos.Valid = buf.ReadUnsignedByte()&0x20 == 0
	} else {
		pos.Set(model.KeySatellites, buf.ReadUnsignedByte())
		pos.Valid = buf.ReadUnsignedByte()&0x08 == 0
	}

	if !mini {
		pos.Set("carrier", buf.ReadUnsignedShort())
		pos.Set(model.KeyRssi, buf.ReadShort())
	}

	pos.Set("modem", buf.ReadUnsignedByte())

	if !mini {
		pos.Set(model.KeyHdop, buf.ReadUnsignedByte())
	}

	input := buf.ReadUnsignedByte()
	pos.Set(model.KeyInput, input)
	pos.Set(model.KeyIgnition, kit.Check(int64(input), 0))

	if !mini {
		pos.Set(model.KeyStatus, buf.ReadUnsignedByte())
	}

	if typ == msgEventReport || mini {
		if !mini {
			buf.ReadUnsignedByte() // event index
		}
		pos.Set(model.KeyEvent, buf.ReadUnsignedByte())
	}

	if typ == msgEventReport || typ == msgLocateReport || mini {
		accType := kit.FromInt(buf.GetUnsignedByte(buf.ReaderIndex()), 6)
		accCount := kit.ToInt(buf.ReadUnsignedByte(), 6)

		if !mini {
			pos.Set("append", buf.ReadUnsignedByte())
		}

		if accType == 1 {
			buf.ReadUnsignedInt() // threshold
			buf.ReadUnsignedInt() // mask
		}

		for i := 0; i < accCount; i++ {
			if buf.ReadableBytes() >= 4 {
				pos.Set("acc"+strconv.Itoa(i), buf.ReadUnsignedInt())
			}
		}
	} else if typ == msgUserData {
		buf.ReadUnsignedByte() // message route
		buf.ReadUnsignedByte() // message id
		pos.Set(model.KeyResult, kit.Trim(buf.ReadString(buf.ReadUnsignedShort())))
	}

	return pos
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	if kit.Check(int64(buf.GetByte(buf.ReaderIndex())), 7) {
		content := int64(buf.ReadUnsignedByte())

		if kit.Check(content, 0) {
			id := kit.StripTrailing('f', hex.EncodeToString(buf.ReadBytes(buf.ReadUnsignedByte())))
			m.Session(id)
		}
		for bit := 1; bit <= 5; bit++ {
			// identifier type, authentication, routing, forwarding, response redirection
			if kit.Check(content, bit) {
				buf.Skip(buf.ReadUnsignedByte())
			}
		}
	}

	ds := m.Session()
	if ds == nil {
		return nil
	}

	service := buf.ReadUnsignedByte()
	typ := buf.ReadUnsignedByte()
	index := buf.ReadUnsignedShort()

	if service == serviceAcknowledged {
		sendResponse(m, typ, index, 0)
	}

	if typ == msgEventReport || typ == msgLocateReport || typ == msgMiniEventReport || typ == msgUserData {
		return decodePosition(m, ds, typ, buf)
	}
	return nil
}
