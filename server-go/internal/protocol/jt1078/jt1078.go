// Package jt1078 is the JT/T 1078 video protocol (Jt1078Protocol): RTP-like media
// packets over TCP, reassembled into frames for a video stream manager.
//
// The Go server has no VideoStreamManager (HLS segmenting) yet: Protocol runs the framer
// and decoder with no Streams, so packets are parsed and dropped. A Streams
// implementation plugs into Decoder.Streams when one exists.
package jt1078

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/jt808"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one TCP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{
		Name: "jt1078",
		Servers: []ingest.Server{{NewPipeline: func() ingest.Pipeline {
			return ingest.Pipeline{
				Framer:  Framer(),
				Decoder: &Decoder{},
			}
		}}},
	}
}

// Framer is Jt1078FrameDecoder: the length is in the header, after a type-dependent
// number of fields.
func Framer() ingest.Framer {
	return &ingest.BaseFramer{MaxLength: ingest.MaxFrameLengthLarge, Decode: func(_ *ingest.Message, buf *kit.Buf) any {
		if buf.ReadableBytes() < 34 {
			return nil
		}
		startIndex := buf.ReaderIndex()
		idLength := 6
		if buf.GetUnsignedShort(startIndex+8) == 0 {
			idLength = 10
		}
		index := startIndex + 9 + idLength // skip header

		typ := kit.FromInt(buf.GetUnsignedByte(index), 4)
		index += 1 + 8 // data type + timestamp
		if typ <= 2 {
			index += 4 // i-frame interval + frame interval
		}
		if buf.ReadableBytes() < index-startIndex+2 {
			return nil
		}
		bodyLength := buf.GetUnsignedShort(index)
		index += 2

		length := index - startIndex + bodyLength
		if buf.ReadableBytes() < length {
			return nil
		}
		return buf.ReadSlice(length)
	}}
}

// Streams is what the decoder needs from the server: DeviceLookupService.lookup and the
// VideoStreamManager.
type Streams interface {
	// Lookup is the id of the device with this unique id, or false when unknown.
	Lookup(m *ingest.Message, uniqueID string) (int64, bool)
	HandleFrame(deviceID int64, channel int, data []byte, timestamp int64, keyFrame bool, payloadType int)
	RemoveStream(deviceID int64, channel int)
}

// Decoder is Jt1078ProtocolDecoder: it reassembles subpackaged frames per connection.
type Decoder struct {
	Streams Streams

	frame            []byte // nil: no frame in progress
	frameDataType    int
	frameTimestamp   int64
	framePayloadType int

	streamDeviceID int64
	streamChannel  int
}

func (d *Decoder) Decode(m *ingest.Message, msg any) any {
	buf := msg.(*kit.Buf)

	buf.ReadUnsignedInt()                        // header
	buf.ReadUnsignedByte()                       // V/P/X/CC
	payloadType := buf.ReadUnsignedByte() & 0x7F // M/PT
	buf.ReadUnsignedShort()                      // index

	idLength := 6
	if buf.GetUnsignedShort(buf.ReaderIndex()) == 0 {
		idLength = 10
	}
	uniqueID := jt808.DecodeID(buf.ReadSlice(idLength))
	videoChannel := buf.ReadUnsignedByte()
	rawType := buf.ReadUnsignedByte()
	dataType := kit.FromInt(rawType, 4)
	subpackageType := kit.ToInt(rawType, 4)
	timestamp := buf.ReadLong()

	if dataType <= 2 {
		buf.Skip(4) // i-frame interval + frame interval
	}

	bodyLength := buf.ReadUnsignedShort()
	if bodyLength == 0 || dataType > 2 || d.Streams == nil {
		return nil
	}

	deviceID, ok := d.Streams.Lookup(m, uniqueID)
	if !ok {
		return nil
	}
	d.streamDeviceID = deviceID
	d.streamChannel = videoChannel

	body := buf.ReadBytes(bodyLength)

	switch subpackageType {
	case 0:
		d.Streams.HandleFrame(deviceID, videoChannel, body, timestamp, dataType == 0, payloadType)
	case 1:
		d.frame = append([]byte{}, body...)
		d.frameDataType = dataType
		d.frameTimestamp = timestamp
		d.framePayloadType = payloadType
	case 3:
		if d.frame != nil {
			d.frame = append(d.frame, body...)
		}
	case 2:
		if d.frame != nil {
			d.frame = append(d.frame, body...)
			d.Streams.HandleFrame(deviceID, videoChannel, d.frame, d.frameTimestamp, d.frameDataType == 0, d.framePayloadType)
			d.frame = nil
		}
	}
	return nil
}

// Close is channelInactive: it ends the connection's stream.
func (d *Decoder) Close() {
	if d.Streams != nil && d.streamDeviceID > 0 {
		d.Streams.RemoveStream(d.streamDeviceID, d.streamChannel)
	}
	d.frame = nil
}
