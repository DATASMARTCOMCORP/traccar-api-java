package javamath

import (
	"encoding/binary"
	"math"
	"math/bits"
)

// machine is the x86_64 subset HotSpot's sin and cos stubs use: general registers, the
// low lanes of the XMM registers, the arithmetic flags, a small stack and the stub tables
// (image). The generated code in libm_gen.go runs the stubs on it, one statement per
// instruction. Floating-point lane operations are IEEE double operations rounded to
// nearest, as SSE2 does them under the MXCSR the JVM uses.
type machine struct {
	r              [16]uint64
	x              [16][2]uint64
	stack          [64]byte
	zf, sf, cf, of bool
}

const (
	imageBase = 1 << 20
	stackBase = 1 << 40
)

// imageBytes is image in memory order.
var imageBytes = func() []byte {
	b := make([]byte, 4*len(image))
	for i, w := range image {
		binary.LittleEndian.PutUint32(b[4*i:], w)
	}
	return b
}()

func newMachine(x float64) *machine {
	m := &machine{}
	m.r[4] = stackBase + uint64(len(m.stack))
	m.x[0][0] = math.Float64bits(x)
	return m
}

func (m *machine) result() float64 { return math.Float64frombits(m.x[0][0]) }

// mem returns the n bytes at addr; an access outside the tables and the stack is a bug
// in the translation, so it panics.
func (m *machine) mem(addr uint64, n int) []byte {
	switch {
	case addr >= stackBase && addr+uint64(n) <= stackBase+uint64(len(m.stack)):
		return m.stack[addr-stackBase:][:n]
	case addr >= imageBase && addr+uint64(n) <= imageBase+uint64(len(imageBytes)):
		return imageBytes[addr-imageBase:][:n]
	}
	panic("javamath: memory access out of range")
}

func (m *machine) load32(addr uint64) uint32 { return binary.LittleEndian.Uint32(m.mem(addr, 4)) }
func (m *machine) load64(addr uint64) uint64 { return binary.LittleEndian.Uint64(m.mem(addr, 8)) }
func (m *machine) load128(addr uint64) [2]uint64 {
	return [2]uint64{m.load64(addr), m.load64(addr + 8)}
}

func (m *machine) store32(addr uint64, v uint32) {
	if addr < stackBase {
		panic("javamath: store outside the stack")
	}
	binary.LittleEndian.PutUint32(m.mem(addr, 4), v)
}

func (m *machine) store64(addr uint64, v uint64) {
	if addr < stackBase {
		panic("javamath: store outside the stack")
	}
	binary.LittleEndian.PutUint64(m.mem(addr, 8), v)
}

func (m *machine) store128(addr uint64, v [2]uint64) {
	m.store64(addr, v[0])
	m.store64(addr+8, v[1])
}

func (m *machine) push(v uint64) {
	m.r[4] -= 8
	m.store64(m.r[4], v)
}

func (m *machine) pop() uint64 {
	v := m.load64(m.r[4])
	m.r[4] += 8
	return v
}

// Lanes. The float64 conversions keep each operation separately rounded (no fusion).

func fl(lane uint64) float64 { return math.Float64frombits(lane) }
func sd(v float64) uint64    { return math.Float64bits(float64(v)) }

func pd(a, b [2]uint64, op func(a, b float64) float64) [2]uint64 {
	return [2]uint64{sd(op(fl(a[0]), fl(b[0]))), sd(op(fl(a[1]), fl(b[1])))}
}

func pshufd(s [2]uint64, imm int) [2]uint64 {
	d := [4]uint32{uint32(s[0]), uint32(s[0] >> 32), uint32(s[1]), uint32(s[1] >> 32)}
	pick := func(i int) uint64 { return uint64(d[imm>>(2*i)&3]) }
	return [2]uint64{pick(0) | pick(1)<<32, pick(2) | pick(3)<<32}
}

func pextrw(s [2]uint64, i int) uint64 { return s[i/4] >> (16 * (i % 4)) & 0xffff }

func pinsrw(d [2]uint64, v uint64, i int) [2]uint64 {
	shift := 16 * (i % 4)
	d[i/4] = d[i/4]&^(0xffff<<shift) | (v&0xffff)<<shift
	return d
}

// cvttsd2si32 and cvttsd2si64 truncate; NaN and out-of-range values give the "integer
// indefinite" value.
func cvttsd2si32(v float64) uint32 {
	if t := math.Trunc(v); t >= math.MinInt32 && t <= math.MaxInt32 {
		return uint32(int32(t))
	}
	return 1 << 31
}

func cvttsd2si64(v float64) uint64 {
	if t := math.Trunc(v); t >= math.MinInt64 && t < -math.MinInt64 {
		return uint64(int64(t))
	}
	return 1 << 63
}

// Integer operations, setting the flags as x86 does.

func (m *machine) zs32(v uint32) uint32 { m.zf, m.sf = v == 0, int32(v) < 0; return v }
func (m *machine) zs64(v uint64) uint64 { m.zf, m.sf = v == 0, int64(v) < 0; return v }

func (m *machine) add32(a, b uint32) uint32 {
	r, c := bits.Add32(a, b, 0)
	m.cf, m.of = c == 1, (^(a^b)&(a^r))>>31 == 1
	return m.zs32(r)
}

func (m *machine) sub32(a, b uint32) uint32 {
	r, c := bits.Sub32(a, b, 0)
	m.cf, m.of = c == 1, ((a^b)&(a^r))>>31 == 1
	return m.zs32(r)
}

func (m *machine) and32(a, b uint32) uint32 { m.cf, m.of = false, false; return m.zs32(a & b) }
func (m *machine) or32(a, b uint32) uint32  { m.cf, m.of = false, false; return m.zs32(a | b) }
func (m *machine) xor32(a, b uint32) uint32 { m.cf, m.of = false, false; return m.zs32(a ^ b) }

func (m *machine) add64(a, b uint64) uint64 {
	r, c := bits.Add64(a, b, 0)
	m.cf, m.of = c == 1, (^(a^b)&(a^r))>>63 == 1
	return m.zs64(r)
}

func (m *machine) sub64(a, b uint64) uint64 {
	r, c := bits.Sub64(a, b, 0)
	m.cf, m.of = c == 1, ((a^b)&(a^r))>>63 == 1
	return m.zs64(r)
}

func (m *machine) sbb64(a, b uint64) uint64 {
	var in uint64
	if m.cf {
		in = 1
	}
	r, c := bits.Sub64(a, b, in)
	m.cf, m.of = c == 1, ((a^b)&(a^r))>>63 == 1
	return m.zs64(r)
}

func (m *machine) and64(a, b uint64) uint64 { m.cf, m.of = false, false; return m.zs64(a & b) }
func (m *machine) or64(a, b uint64) uint64  { m.cf, m.of = false, false; return m.zs64(a | b) }
func (m *machine) xor64(a, b uint64) uint64 { m.cf, m.of = false, false; return m.zs64(a ^ b) }

// Shifts mask the count; a zero count leaves the flags alone.

func (m *machine) shl32(a uint32, c uint) uint32 {
	if c &= 31; c == 0 {
		return a
	}
	r := a << c
	m.cf = a>>(32-c)&1 == 1
	m.of = (r>>31 == 1) != m.cf
	return m.zs32(r)
}

func (m *machine) shr32(a uint32, c uint) uint32 {
	if c &= 31; c == 0 {
		return a
	}
	m.cf, m.of = a>>(c-1)&1 == 1, a>>31 == 1
	return m.zs32(a >> c)
}

func (m *machine) sar32(a uint32, c uint) uint32 {
	if c &= 31; c == 0 {
		return a
	}
	m.cf, m.of = int32(a)>>(c-1)&1 == 1, false
	return m.zs32(uint32(int32(a) >> c))
}

func (m *machine) shl64(a uint64, c uint) uint64 {
	if c &= 63; c == 0 {
		return a
	}
	r := a << c
	m.cf = a>>(64-c)&1 == 1
	m.of = (r>>63 == 1) != m.cf
	return m.zs64(r)
}

func (m *machine) shr64(a uint64, c uint) uint64 {
	if c &= 63; c == 0 {
		return a
	}
	m.cf, m.of = a>>(c-1)&1 == 1, a>>63 == 1
	return m.zs64(a >> c)
}

func (m *machine) sar64(a uint64, c uint) uint64 {
	if c &= 63; c == 0 {
		return a
	}
	m.cf, m.of = int64(a)>>(c-1)&1 == 1, false
	return m.zs64(uint64(int64(a) >> c))
}

// imul64 is the two-operand signed multiply: the low 64 bits, with CF and OF telling
// whether the product didn't fit.
func (m *machine) imul64(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	if int64(a) < 0 {
		hi -= b
	}
	if int64(b) < 0 {
		hi -= a
	}
	m.cf = hi != uint64(int64(lo)>>63)
	m.of = m.cf
	return lo
}

// bsr sets register d to the index of the highest set bit of v; a zero v sets ZF and
// leaves d as it was.
func (m *machine) bsr(d int, v uint64) {
	m.zf = v == 0
	if v != 0 {
		m.r[d] = uint64(63 - bits.LeadingZeros64(v))
	}
}
