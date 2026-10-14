package model

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The Java server writes JSON with Jackson, both in API responses and in the JSON text
// columns (attributes, network, geofenceids). The functions here reproduce Jackson's
// output byte for byte, so rows written by Go store the same text as rows written by
// Java. encoding/json differs in ways that matter here: it escapes <, >, &, U+2028 and
// U+2029, uses lowercase \u hex, and formats 87.0 as 87.

// appendString appends s as a JSON string the way Jackson escapes it: \" \\ and the
// short forms \b \t \n \f \r, other control characters as \u00XX in uppercase hex, and
// everything else (including DEL, U+2028 and non-ASCII) unescaped. Invalid UTF-8 becomes
// U+FFFD, since Java strings can't hold it either.
func appendString(b []byte, s string) []byte {
	const hex = "0123456789ABCDEF"
	b = append(b, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				b = append(b, "�"...)
			} else {
				b = append(b, s[i:i+size]...)
			}
			i += size
			continue
		}
		switch c {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\b':
			b = append(b, '\\', 'b')
		case '\t':
			b = append(b, '\\', 't')
		case '\n':
			b = append(b, '\\', 'n')
		case '\f':
			b = append(b, '\\', 'f')
		case '\r':
			b = append(b, '\\', 'r')
		default:
			if c < 0x20 {
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
			} else {
				b = append(b, c)
			}
		}
		i++
	}
	return append(b, '"')
}

// appendDouble appends f the way Jackson writes a Java double. Finite values use
// Double.toString; NaN and infinities become the strings "NaN", "Infinity" and
// "-Infinity" (Jackson's QUOTE_NON_NUMERIC_NUMBERS default).
func appendDouble(b []byte, f float64) []byte {
	switch {
	case math.IsNaN(f):
		return append(b, `"NaN"`...)
	case math.IsInf(f, 1):
		return append(b, `"Infinity"`...)
	case math.IsInf(f, -1):
		return append(b, `"-Infinity"`...)
	}
	return append(b, javaDouble(f)...)
}

// javaDouble formats a finite f like Java's Double.toString (JDK 19 and later):
//   - the shortest decimal that rounds back to f; when that has a single digit, a
//     two-digit decimal is used instead if it's closer to f (so Double.MIN_VALUE is
//     4.9E-324, not 5.0E-324)
//   - plain notation with at least one fraction digit for 1e-3 <= |f| < 1e7 (87.0, 0.001)
//   - otherwise d.dddE±n with at least one fraction digit (1.0E7, 1.23456789E7, 1.0E-5)
//
// Older JDKs (before 19) sometimes printed more digits than needed. The production JVM
// version is checked in Step 0 (inventory/README.md section 2).
func javaDouble(f float64) string {
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	digits, exp := shortestDigits(f) // f = 0.d1d2d3... × 10^exp
	neg := f < 0

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	abs := math.Abs(f)
	if abs >= 1e-3 && abs < 1e7 {
		switch {
		case exp <= 0: // 0.00ddd
			b.WriteString("0.")
			b.WriteString(strings.Repeat("0", -exp))
			b.WriteString(digits)
		case exp >= len(digits): // ddd00.0
			b.WriteString(digits)
			b.WriteString(strings.Repeat("0", exp-len(digits)))
			b.WriteString(".0")
		default: // dd.ddd
			b.WriteString(digits[:exp])
			b.WriteByte('.')
			b.WriteString(digits[exp:])
		}
		return b.String()
	}
	b.WriteByte(digits[0])
	b.WriteByte('.')
	if len(digits) > 1 {
		b.WriteString(digits[1:])
	} else {
		b.WriteByte('0')
	}
	b.WriteByte('E')
	b.WriteString(strconv.Itoa(exp - 1))
	return b.String()
}

// shortestDigits returns the significant digits and decimal exponent Java chooses for
// f (f != 0), as described in javaDouble.
func shortestDigits(f float64) (string, int) {
	digits, exp := splitE(strconv.FormatFloat(math.Abs(f), 'e', -1, 64))
	if len(digits) != 1 {
		return digits, exp
	}
	// One digit: Java also considers the two-digit decimals that round to f and picks
	// the closest. FormatFloat with precision 1 is the closest two-digit decimal.
	two, exp2 := splitE(strconv.FormatFloat(math.Abs(f), 'e', 1, 64))
	if len(two) < 2 || !roundsTo(two, exp2, math.Abs(f)) {
		return digits, exp
	}
	exact := new(big.Float).SetPrec(2000).SetFloat64(math.Abs(f))
	d1 := distance(digits, exp, exact)
	d2 := distance(two, exp2, exact)
	if d2.Cmp(d1) < 0 {
		return two, exp2
	}
	return digits, exp
}

// splitE turns "1.2345e+07" into ("12345", 8), meaning 0.12345 × 10^8.
func splitE(s string) (string, int) {
	mant, e, _ := strings.Cut(s, "e")
	n, _ := strconv.Atoi(e)
	// f != 0, so at least one significant digit remains.
	return strings.TrimRight(strings.Replace(mant, ".", "", 1), "0"), n + 1
}

func decimal(digits string, exp int) string {
	return "0." + digits + "e" + strconv.Itoa(exp)
}

func roundsTo(digits string, exp int, f float64) bool {
	v, err := strconv.ParseFloat(decimal(digits, exp), 64)
	return err == nil && v == f
}

func distance(digits string, exp int, exact *big.Float) *big.Float {
	d, _, _ := big.ParseFloat(decimal(digits, exp), 10, 2000, big.ToNearestEven)
	return new(big.Float).SetPrec(2000).Abs(new(big.Float).SetPrec(2000).Sub(d, exact))
}
