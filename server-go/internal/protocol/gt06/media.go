package gt06

import (
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// mediaBuffer is the decoder's media buffer (BaseProtocolDecoder.newMediaBuffer(size)):
// Netty's Unpooled.buffer(size, media.bufferSize), whose capacity decides when a photo is
// complete (writableBytes is capacity - writerIndex). buf.readBytes(dst, length) never
// grows dst: it throws when length exceeds dst.writableBytes().
type mediaBuffer struct {
	data     []byte
	capacity int
}

// newMediaBuffer is newMediaBuffer(size).
func newMediaBuffer(m *ingest.Message, size int) *mediaBuffer {
	max, err := m.Config().Int("media.bufferSize", 32*1024*1024)
	if err != nil {
		panic(err)
	}
	if size < 0 || size > max {
		kit.Throw("IllegalArgumentException: initialCapacity: %d (expected: 0-%d)", size, max)
	}
	return &mediaBuffer{capacity: size}
}

func (b *mediaBuffer) writerIndex() int   { return len(b.data) }
func (b *mediaBuffer) writableBytes() int { return b.capacity - len(b.data) }

// readFrom is buf.readBytes(media, n).
func (b *mediaBuffer) readFrom(buf *kit.Buf, n int) {
	if n > b.writableBytes() {
		kit.Throw("IndexOutOfBoundsException: length(%d) exceeds dst.writableBytes(%d)", n, b.writableBytes())
	}
	b.data = append(b.data, buf.ReadBytes(n)...)
}
