package javamath

import "math"

// atan2 and atan are fdlibm 5.3's e_atan2.c and s_atan.c, which StrictMath (and through
// it Math) uses for atan2. The float64 conversions keep each operation separately rounded.

/*
 * ====================================================
 * Copyright (C) 1993 by Sun Microsystems, Inc. All rights reserved.
 *
 * Developed at SunSoft, a Sun Microsystems, Inc. business.
 * Permission to use, copy, modify, and distribute this
 * software is freely granted, provided that this notice
 * is preserved.
 * ====================================================
 */

var (
	atanHi = [...]float64{
		4.63647609000806093515e-01, // atan(0.5)hi
		7.85398163397448278999e-01, // atan(1.0)hi
		9.82793723247329054082e-01, // atan(1.5)hi
		1.57079632679489655800e+00, // atan(inf)hi
	}
	atanLo = [...]float64{
		2.26987774529616870924e-17,
		3.06161699786838301793e-17,
		1.39033110312309984516e-17,
		6.12323399573676603587e-17,
	}
	aT = [...]float64{
		3.33333333333329318027e-01,
		-1.99999999998764832476e-01,
		1.42857142725034663711e-01,
		-1.11111104054623557880e-01,
		9.09088713343650656196e-02,
		-7.69187620504482999495e-02,
		6.66107313738753120669e-02,
		-5.83357013379057348645e-02,
		4.97687799461593236017e-02,
		-3.65315727442169155270e-02,
		1.62858201153657823623e-02,
	}
)

// Variables, not constants: Go evaluates constant expressions exactly, while fdlibm's
// (such as 3.0*pi_o_4 and pi+tiny) are rounded double operations.
var (
	huge = 1.0e300
	tiny = 1.0e-300
	piO4 = 7.8539816339744827900e-01
	piO2 = 1.5707963267948965580e+00
	pi   = 3.1415926535897931160e+00
	piLo = 1.2246467991473531772e-16
)

func hi(x float64) int32  { return int32(math.Float64bits(x) >> 32) }
func lo(x float64) uint32 { return uint32(math.Float64bits(x)) }

func atan(x float64) float64 {
	hx := hi(x)
	ix := hx & 0x7fffffff
	var id int
	switch {
	case ix >= 0x44100000: // |x| >= 2^66
		if ix > 0x7ff00000 || ix == 0x7ff00000 && lo(x) != 0 {
			return x + x // NaN
		}
		if hx > 0 {
			return float64(atanHi[3] + atanLo[3])
		}
		return float64(-atanHi[3] - atanLo[3])
	case ix < 0x3fdc0000: // |x| < 0.4375
		if ix < 0x3e200000 && huge+x > 1 { // |x| < 2^-29
			return x
		}
		id = -1
	default:
		x = math.Abs(x)
		switch {
		case ix < 0x3fe60000: // 7/16 <= |x| < 11/16
			id, x = 0, float64(float64(2.0*x)-1)/float64(2.0+x)
		case ix < 0x3ff30000: // 11/16 <= |x| < 19/16
			id, x = 1, float64(x-1)/float64(x+1)
		case ix < 0x40038000: // |x| < 2.4375
			id, x = 2, float64(x-1.5)/float64(1+float64(1.5*x))
		default: // 2.4375 <= |x| < 2^66
			id, x = 3, -1.0/x
		}
	}
	z := float64(x * x)
	w := float64(z * z)
	// The sum from i=0 to 10 of aT[i]z^(i+1), as odd and even polynomials.
	s1 := z * float64(aT[0]+w*float64(aT[2]+w*float64(aT[4]+w*float64(aT[6]+w*float64(aT[8]+float64(w*aT[10]))))))
	s1 = float64(s1)
	s2 := float64(w * float64(aT[1]+w*float64(aT[3]+w*float64(aT[5]+w*float64(aT[7]+float64(w*aT[9]))))))
	if id < 0 {
		return x - float64(x*float64(s1+s2))
	}
	z = atanHi[id] - float64(float64(float64(x*float64(s1+s2))-atanLo[id])-x)
	if hx < 0 {
		return -z
	}
	return z
}

func atan2(y, x float64) float64 {
	hx, lx := hi(x), lo(x)
	ix := hx & 0x7fffffff
	hy, ly := hi(y), lo(y)
	iy := hy & 0x7fffffff
	if uint32(ix)|(lx|-lx)>>31 > 0x7ff00000 || uint32(iy)|(ly|-ly)>>31 > 0x7ff00000 {
		return x + y // NaN
	}
	if (hx-0x3ff00000)|int32(lx) == 0 { // x == 1.0
		return atan(y)
	}
	m := (hy>>31)&1 | (hx>>30)&2 // 2*sign(x) + sign(y)

	if iy|int32(ly) == 0 { // y == 0
		switch m {
		case 0, 1:
			return y // atan(±0, +anything) = ±0
		case 2:
			return pi + tiny // atan(+0, -anything) = pi
		default:
			return -pi - tiny // atan(-0, -anything) = -pi
		}
	}
	if ix|int32(lx) == 0 { // x == 0
		if hy < 0 {
			return -piO2 - tiny
		}
		return piO2 + tiny
	}
	if ix == 0x7ff00000 { // x is ±Inf
		if iy == 0x7ff00000 {
			switch m {
			case 0:
				return piO4 + tiny
			case 1:
				return -piO4 - tiny
			case 2:
				return float64(3.0*piO4) + tiny
			default:
				return float64(-3.0*piO4) - tiny
			}
		}
		switch m {
		case 0:
			return 0
		case 1:
			return math.Copysign(0, -1)
		case 2:
			return pi + tiny
		default:
			return -pi - tiny
		}
	}
	if iy == 0x7ff00000 { // y is ±Inf
		if hy < 0 {
			return -piO2 - tiny
		}
		return piO2 + tiny
	}

	var z float64
	k := (iy - ix) >> 20
	switch {
	case k > 60: // |y/x| > 2^60
		z = piO2 + float64(0.5*piLo)
	case hx < 0 && k < -60: // |y|/x < -2^60
		z = 0
	default:
		z = atan(math.Abs(float64(y / x)))
	}
	switch m {
	case 0:
		return z // atan(+, +)
	case 1:
		return -z // atan(-, +)
	case 2:
		return pi - float64(z-piLo) // atan(+, -)
	default:
		return float64(z-piLo) - pi // atan(-, -)
	}
}
