// Package dualcam is the Teltonika DualCam protocol (DualcamProtocol): binary messages
// that transfer photos and videos over TCP.
package dualcam

import (
	"strconv"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "dualcam",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{Framer: Framer(), Decoder: &Decoder{}}
		}}},
	}
}

// Framer is DualcamFrameDecoder: init messages are 16 bytes, others carry their length.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 4 {
			return nil
		}
		length := 16
		if buf.GetUnsignedShort(buf.ReaderIndex()) != 0 {
			length = 4 + buf.GetUnsignedShort(buf.ReaderIndex()+2)
		}
		if buf.ReadableBytes() >= length {
			return buf.ReadSlice(length)
		}
		return nil
	}}
}

const (
	msgInit         = 0
	msgStart        = 1
	msgResume       = 2
	msgSync         = 3
	msgData         = 4
	msgComplete     = 5
	msgFileRequest  = 8
	msgInitRequest  = 9
	msgPathRequest  = 0x000C
	msgPathResponse = 0x000D
)

// Decoder is DualcamProtocolDecoder: the state of one file transfer.
type Decoder struct {
	uniqueID    string
	dataSize    int
	dataCurrent int
	video       bool
	media       *kit.Buf // the decoder's media buffer; nil when there is none
}

func (d *Decoder) isPacketData() bool { return d.dataSize < 8192 }

func (d *Decoder) completeMedia(m *ingest.Message) *model.Position {
	ds := m.Session()
	if ds == nil {
		kit.Throw("NullPointerException: device session")
	}
	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID
	m.LastLocation(pos, nil)

	data := d.media.Bytes()
	d.media = nil // writeMediaFile releases the buffer
	if d.video {
		pos.Set(model.KeyVideo, m.WriteMedia(d.uniqueID, "h265", data))
	} else {
		pos.Set(model.KeyImage, m.WriteMedia(d.uniqueID, "jpg", data))
	}

	m.Send(kit.Empty().WriteShort(msgInitRequest))
	return pos
}

func fileRequest(file string) *kit.Buf {
	response := kit.Empty()
	response.WriteShort(msgFileRequest)
	response.WriteShort(kit.Len(file))
	response.WriteASCII(file)
	return response
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)
	switch buf.ReadUnsignedShort() {
	case msgInit:
		buf.ReadUnsignedShort() // protocol id
		d.uniqueID = strconv.FormatInt(buf.ReadLong(), 10)
		ds := m.Session(d.uniqueID)
		settings := buf.ReadUnsignedInt()
		if ds != nil {
			var response *kit.Buf
			switch {
			case kit.Check(settings, 25):
				response = kit.Empty().WriteShort(msgPathRequest).WriteShort(2).WriteShort(0)
			case kit.Between(settings, 26, 30) > 0:
				var file string
				switch {
				case kit.Check(settings, 26):
					d.video, file = false, "%photof"
				case kit.Check(settings, 27):
					d.video, file = false, "%photor"
				case kit.Check(settings, 28):
					d.video, file = true, "%videof"
				default:
					d.video, file = true, "%videor"
				}
				response = fileRequest(file)
			default:
				response = kit.Empty().WriteShort(msgInit)
			}
			m.Send(response)
		}
	case msgStart:
		buf.ReadUnsignedShort() // length
		d.dataSize = int(buf.ReadInt())
		d.dataCurrent = 0
		if d.isPacketData() {
			d.dataCurrent = 1
		}
		d.media = m.NewMediaBuffer()
		m.Send(kit.Empty().WriteShort(msgResume).WriteShort(4).WriteInt(int64(d.dataCurrent)))
	case msgData:
		length := buf.ReadUnsignedShort() - 2
		data := buf.ReadBytes(length)
		d.media.WriteBytes(data) // a nil buffer throws, as Java's NullPointerException
		var finished bool
		if d.isPacketData() {
			finished = d.dataCurrent == d.dataSize
			d.dataCurrent++
		} else {
			finished = d.dataCurrent+length == d.dataSize
			d.dataCurrent += length
		}
		if finished {
			return d.completeMedia(m)
		}
	case msgComplete:
		if d.media != nil {
			return d.completeMedia(m)
		}
	case msgPathResponse:
		file := buf.ReadString(buf.ReadUnsignedShort())
		m.Send(fileRequest(file))
	}
	return nil
}
