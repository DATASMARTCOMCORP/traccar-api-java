package kit

// MediaBuffer is a media buffer made with an initial capacity (newMediaBuffer(size),
// Unpooled.buffer(size, max)): it grows as Netty's buffers do when more is written, and
// protocols that fetch a file in chunks treat it as complete when nothing is writable.
type MediaBuffer struct {
	Data        []byte
	Capacity    int
	MaxCapacity int
}

// NewMediaBuffer is Unpooled.buffer(capacity, maxCapacity).
func NewMediaBuffer(capacity, maxCapacity int) *MediaBuffer {
	if capacity < 0 || capacity > maxCapacity {
		Throw("IllegalArgumentException: initialCapacity: %d (expected: 0-%d)", capacity, maxCapacity)
	}
	return &MediaBuffer{Capacity: capacity, MaxCapacity: maxCapacity}
}

// Write is writeBytes.
func (p *MediaBuffer) Write(b []byte) {
	need := len(p.Data) + len(b)
	if need > p.MaxCapacity {
		Throw("IndexOutOfBoundsException: writerIndex(%d) + minWritableBytes(%d) exceeds maxCapacity(%d)", len(p.Data), len(b), p.MaxCapacity)
	}
	if need > p.Capacity {
		p.Capacity = nettyCapacity(need, p.MaxCapacity)
	}
	p.Data = append(p.Data, b...)
}

// Writable is writableBytes.
func (p *MediaBuffer) Writable() int { return p.Capacity - len(p.Data) }

// nettyCapacity is AbstractByteBufAllocator.calculateNewCapacity.
func nettyCapacity(minimum, maximum int) int {
	const threshold = 4 * 1024 * 1024
	if minimum == threshold {
		return threshold
	}
	if minimum > threshold {
		c := minimum / threshold * threshold
		if c > maximum-threshold {
			return maximum
		}
		return c + threshold
	}
	c := 64
	for c < minimum {
		c <<= 1
	}
	return min(c, maximum)
}
