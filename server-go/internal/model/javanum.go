package model

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Number parsing as java.lang does it. Decoders and handlers parse device text and
// attribute values with Double.parseDouble, Integer.parseInt and Long.parseLong; a value
// Java rejects makes the Java code throw, so Go must reject exactly the same inputs.

var errNumberFormat = errors.New("number format")

// javaNaN is Double.NaN's bit pattern (Go's math.NaN() differs in the low bit).
var javaNaN = math.Float64frombits(0x7ff8000000000000)

// ParseDouble is Double.parseDouble: surrounding whitespace (chars <= ' ') is ignored;
// accepted are an optional sign, then "NaN", "Infinity", a decimal number with an
// optional exponent, or a hexadecimal floating-point literal ("0x1.8p3"), each optionally
// followed by one of the suffixes f, F, d, D.
func ParseDouble(s string) (float64, error) {
	s = strings.TrimFunc(s, func(r rune) bool { return r <= ' ' })
	body, neg := s, false
	if body != "" && (body[0] == '+' || body[0] == '-') {
		neg = body[0] == '-'
		body = body[1:]
	}
	switch body {
	case "NaN":
		return javaNaN, nil
	case "Infinity":
		if neg {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	}
	if n := len(body); n > 0 && strings.IndexByte("fFdD", body[n-1]) >= 0 {
		body = body[:n-1]
	}
	if !javaDecimal(body) && !javaHex(body) {
		return 0, errNumberFormat
	}
	v, err := strconv.ParseFloat(body, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, errNumberFormat
	}
	if neg {
		v = -v
	}
	return v, nil
}

// javaDecimal matches digits, an optional fraction and exponent: "1", "1.", ".5", "1e3".
func javaDecimal(s string) bool {
	i, digits := 0, 0
	for i < len(s) && isDigit(s[i]) {
		i, digits = i+1, digits+1
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(s[i]) {
			i, digits = i+1, digits+1
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(s)
}

// javaHex matches 0x<hex>[.<hex>]p<exp>, with at least one hex digit and the exponent.
func javaHex(s string) bool {
	if len(s) < 2 || s[0] != '0' || s[1] != 'x' && s[1] != 'X' {
		return false
	}
	i, digits := 2, 0
	for i < len(s) && isHex(s[i]) {
		i, digits = i+1, digits+1
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isHex(s[i]) {
			i, digits = i+1, digits+1
		}
	}
	if digits == 0 || i >= len(s) || s[i] != 'p' && s[i] != 'P' {
		return false
	}
	i++
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	start := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i > start && i == len(s)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isHex(c byte) bool   { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// ParseInt is Integer.parseInt: an optional sign and decimal digits, nothing else (no
// whitespace), within int32.
func ParseInt(s string) (int32, error) {
	v, err := parseJavaInteger(s, 10, 32)
	return int32(v), err
}

// ParseLong is Long.parseLong.
func ParseLong(s string) (int64, error) { return parseJavaInteger(s, 10, 64) }

// ParseIntRadix is Integer.parseInt(s, radix).
func ParseIntRadix(s string, radix int) (int32, error) {
	v, err := parseJavaInteger(s, radix, 32)
	return int32(v), err
}

// ParseLongRadix is Long.parseLong(s, radix).
func ParseLongRadix(s string, radix int) (int64, error) { return parseJavaInteger(s, radix, 64) }

// parseJavaInteger accepts any digit Character.digit accepts: Unicode decimal digits
// ("١٢" is 12) and, above radix 10, ASCII and fullwidth Latin letters. It rejects
// overflow.
func parseJavaInteger(s string, radix, bits int) (int64, error) {
	digits, negative := s, false
	if digits != "" && (digits[0] == '+' || digits[0] == '-') {
		negative = digits[0] == '-'
		digits = digits[1:]
	}
	if digits == "" {
		return 0, errNumberFormat
	}
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	for _, r := range digits {
		d, ok := javaDigit(r, radix)
		if !ok {
			return 0, errNumberFormat
		}
		b.WriteByte("0123456789abcdefghijklmnopqrstuvwxyz"[d])
	}
	v, err := strconv.ParseInt(b.String(), radix, bits)
	if err != nil {
		return 0, errNumberFormat
	}
	return v, nil
}

// javaDigit is Character.digit(char, radix).
func javaDigit(r rune, radix int) (int, bool) {
	d := -1
	switch {
	case 'a' <= r && r <= 'z':
		d = int(r-'a') + 10
	case 'A' <= r && r <= 'Z':
		d = int(r-'A') + 10
	case 0xFF41 <= r && r <= 0xFF5A:
		d = int(r-0xFF41) + 10
	case 0xFF21 <= r && r <= 0xFF3A:
		d = int(r-0xFF21) + 10
	default:
		if v, ok := decimalDigit(r); ok {
			d = v
		}
	}
	return d, d >= 0 && d < radix
}

// decimalDigit is Character.digit(char, 10) for decimal digits: the value of any Unicode
// Nd character in the BMP (their ranges are runs of whole 0-9 sets). Java parses UTF-16
// chars, so a digit outside the BMP is two surrogates, neither a digit.
func decimalDigit(r rune) (int, bool) {
	if '0' <= r && r <= '9' {
		return int(r - '0'), true
	}
	if r > 0xFFFF || !unicode.Is(unicode.Nd, r) {
		return 0, false
	}
	for _, rg := range unicode.Nd.R16 {
		if lo, hi := rune(rg.Lo), rune(rg.Hi); lo <= r && r <= hi {
			return int(r-lo) % 10, true
		}
	}
	for _, rg := range unicode.Nd.R32 {
		if lo, hi := rune(rg.Lo), rune(rg.Hi); lo <= r && r <= hi {
			return int(r-lo) % 10, true
		}
	}
	return 0, false
}

// ParseBoolean is Boolean.parseBoolean: "true" in any case is true, anything else false.
func ParseBoolean(s string) bool { return strings.EqualFold(s, "true") }
