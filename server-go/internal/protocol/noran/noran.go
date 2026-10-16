// Package noran is the Noran protocol (NoranProtocol): binary messages over UDP.
package noran

import (
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// Protocol is the protocol's definition: one UDP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name:         "noran",
		DataCommands: protocol.DataCommands("noran"),
		Servers: []ingest.Server{{Datagram: true, NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Decoder: ingest.DecoderFunc(decode), Encoder: ingest.CommandEncoderFunc(encode)}
		}}},
	}
}

const (
	msgUploadPosition    = 0x0008
	msgUploadPositionNew = 0x0032
	msgControl           = 0x0002
	msgControlResponse   = 0x8009
	msgAlarm             = 0x0003
	msgShakeHand         = 0x0000
	msgShakeHandResponse = 0x8000
)

// printable is replaceAll("[^\\p{Print}]", ""): only ASCII 0x20-0x7E is kept.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x20 && r <= 0x7E {
			return r
		}
		return -1
	}, s)
}

func decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	buf.ReadUnsignedShortLE() // length
	typ := buf.ReadUnsignedShortLE()

	if typ == msgShakeHand {
		if m.HasChannel() {
			response := kit.Empty().WriteASCII("\r\n*KW")
			response.WriteByte8(0)
			response.WriteShortLE(13) // capacity
			response.WriteShortLE(msgShakeHandResponse)
			response.WriteByte8(1) // status
			response.WriteASCII("\r\n")
			m.Send(response)
		}
		return nil
	}
	if typ != msgUploadPosition && typ != msgUploadPositionNew && typ != msgControlResponse && typ != msgAlarm {
		return nil
	}

	newFormat := typ == msgUploadPosition && buf.ReadableBytes() == 48 ||
		typ == msgAlarm && buf.ReadableBytes() == 48 ||
		typ == msgControlResponse && buf.ReadableBytes() == 57

	pos := m.NewPosition()
	if typ == msgControlResponse {
		buf.ReadUnsignedIntLE() // GIS ip
		buf.ReadUnsignedIntLE() // GIS port
	}
	pos.Valid = kit.Check(int64(buf.ReadUnsignedByte()), 0)
	switch buf.ReadUnsignedByte() {
	case 1:
		pos.AddAlarm(model.AlarmSos)
	case 2:
		pos.AddAlarm(model.AlarmOverspeed)
	case 3:
		pos.AddAlarm(model.AlarmGeofenceExit)
	case 9:
		pos.AddAlarm(model.AlarmPowerOff)
	}
	if newFormat {
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedIntLE()))
		pos.Course = float64(buf.ReadFloatLE())
	} else {
		pos.Speed = units.KnotsFromKph(float64(buf.ReadUnsignedByte()))
		pos.Course = float64(buf.ReadUnsignedShortLE())
	}
	pos.SetLongitude(float64(buf.ReadFloatLE()))
	pos.SetLatitude(float64(buf.ReadFloatLE()))

	if !newFormat {
		t := buf.ReadUnsignedIntLE()
		pos.SetTime(kit.NewDateBuilder(nil).
			SetYear(int(kit.From(t, 26))).
			SetMonth(int(kit.Between(t, 22, 26))).
			SetDay(int(kit.Between(t, 17, 22))).
			SetHour(int(kit.Between(t, 12, 17))).
			SetMinute(int(kit.Between(t, 6, 12))).
			SetSecond(int(kit.To(t, 6))).
			Date())
	}

	idLength := 11
	if newFormat {
		idLength = 12
	}
	ds := m.Session(printable(buf.ReadString(idLength)))
	if ds == nil {
		return nil
	}
	pos.DeviceID = ds.DeviceID

	if newFormat {
		pos.SetTime(kit.NewDateFormat("yy-MM-dd HH:mm:ss", m.Zone()).Parse(buf.ReadString(17)))
		buf.ReadSignedByte()
	}
	if !newFormat {
		pos.Set(model.PrefixIo+"1", buf.ReadUnsignedByte())
		pos.Set(model.KeyFuel, buf.ReadUnsignedByte())
	} else if typ == msgUploadPositionNew {
		pos.Set(model.PrefixTemp+"1", buf.ReadShortLE())
		pos.Set(model.KeyOdometer, buf.ReadFloatLE())
	}
	return pos
}

// encodeContent is a 68-byte control message with the command text (zero-padded to 50).
func encodeContent(content string) *kit.Buf {
	buf := kit.Empty().WriteASCII("\r\n*KW")
	buf.WriteByte8(0)
	buf.WriteShortLE(12 + 56) // capacity
	buf.WriteShortLE(msgControl)
	buf.WriteInt(0)     // gis ip
	buf.WriteShortLE(0) // gis port
	buf.WriteBytes(kit.ASCIIBytes(content))
	if pad := 50 - kit.Len(content); pad >= 0 {
		buf.WriteZero(pad)
	} else {
		buf.SetWriterIndex(buf.WriterIndex() + pad)
	}
	return buf.WriteASCII("\r\n")
}

func encode(_ *ingest.Message, c *model.Command) any {
	switch c.Type {
	case model.CommandPositionSingle:
		return encodeContent("*KW,000,000,000000#")
	case model.CommandPositionPeriodic:
		return encodeContent("*KW,000,002,000000," + strconv.Itoa(kit.CommandInt(c, model.CommandKeyFrequency)) + "#")
	case model.CommandPositionStop:
		return encodeContent("*KW,000,002,000000,0#")
	case model.CommandEngineStop:
		return encodeContent("*KW,000,007,000000,0#")
	case model.CommandEngineResume:
		return encodeContent("*KW,000,007,000000,1#")
	}
	return nil
}
