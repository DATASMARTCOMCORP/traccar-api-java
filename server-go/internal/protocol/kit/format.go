package kit

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Format is String.format (java.util.Formatter) for the conversions protocols use: s, S,
// d, x, X, o, f, e, E, c, b, n and %, with the flags '-', '0', '+', ' ', ',' and '#'.
//
// Arguments carry their Java type: int and int32 are Integer, int64 is Long, int16 is
// Short, int8 is Byte (hex and octal of negative values are two's complement of that
// width), float64 is Double, string is String and bool is Boolean. %s of any argument is
// its Java toString. %f and %e round half up from the shortest decimal digits of the
// value, as Java's Formatter does.
func Format(format string, args ...any) string {
	var b strings.Builder
	next := 0
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		j := i + 1
		// explicit index n$
		index := -1
		if k := j; k < len(format) {
			for k < len(format) && format[k] >= '0' && format[k] <= '9' {
				k++
			}
			if k < len(format) && k > j && format[k] == '$' {
				index, _ = strconv.Atoi(format[j:k])
				index--
				j = k + 1
			}
		}
		flags := ""
		for j < len(format) && strings.IndexByte("-#+ 0,(", format[j]) >= 0 {
			flags += string(format[j])
			j++
		}
		width := -1
		k := j
		for k < len(format) && format[k] >= '0' && format[k] <= '9' {
			k++
		}
		if k > j {
			width, _ = strconv.Atoi(format[j:k])
			j = k
		}
		precision := -1
		if j < len(format) && format[j] == '.' {
			k = j + 1
			for k < len(format) && format[k] >= '0' && format[k] <= '9' {
				k++
			}
			precision, _ = strconv.Atoi(format[j+1 : k])
			j = k
		}
		if j >= len(format) {
			Throw("UnknownFormatConversionException: Conversion = '%%'")
		}
		conv := format[j]
		i = j
		switch conv {
		case '%':
			b.WriteString(pad("%", width, flags))
			continue
		case 'n':
			b.WriteByte('\n')
			continue
		}
		var arg any
		if index >= 0 {
			if index >= len(args) {
				Throw("MissingFormatArgumentException: Format specifier '%%%c'", conv)
			}
			arg = args[index]
		} else {
			if next >= len(args) {
				Throw("MissingFormatArgumentException: Format specifier '%%%c'", conv)
			}
			arg = args[next]
			next++
		}
		b.WriteString(formatOne(conv, arg, flags, width, precision))
	}
	return b.String()
}

func pad(s string, width int, flags string) string {
	n := len([]rune(s))
	if width <= n {
		return s
	}
	if strings.Contains(flags, "-") {
		return s + strings.Repeat(" ", width-n)
	}
	return strings.Repeat(" ", width-n) + s
}

func formatOne(conv byte, arg any, flags string, width, precision int) string {
	switch conv {
	case 's', 'S':
		s := javaString(arg)
		if precision >= 0 && precision < len([]rune(s)) {
			s = string([]rune(s)[:precision])
		}
		if conv == 'S' {
			s = strings.ToUpper(s)
		}
		return pad(s, width, flags)
	case 'b', 'B':
		s := "true"
		switch v := arg.(type) {
		case nil:
			s = "false"
		case bool:
			s = strconv.FormatBool(v)
		}
		if conv == 'B' {
			s = strings.ToUpper(s)
		}
		return pad(s, width, flags)
	case 'c':
		var r rune
		switch v := arg.(type) {
		case int32: // a char
			r = v
		case byte:
			r = rune(v)
		case int:
			r = rune(v)
		default:
			Throw("IllegalFormatConversionException: c != %T", arg)
		}
		return pad(string(r), width, flags)
	case 'd', 'x', 'X', 'o':
		return formatInteger(conv, arg, flags, width)
	case 'f', 'e', 'E':
		v, ok := arg.(float64)
		if !ok {
			if f, ok32 := arg.(float32); ok32 {
				v, ok = float64(f), true
			}
		}
		if !ok {
			Throw("IllegalFormatConversionException: %c != %T", conv, arg)
		}
		if precision < 0 {
			precision = 6
		}
		return formatFloat(conv, v, flags, width, precision)
	}
	Throw("UnknownFormatConversionException: Conversion = '%c'", conv)
	return ""
}

func javaString(arg any) string {
	switch v := arg.(type) {
	case int:
		return strconv.Itoa(v)
	case int8, int16, int32, int64, uint8, uint16, uint32:
		return model.JavaToString(normalizeInt(v))
	case float32:
		return model.JavaToString(float64(v)) // a Float prints differently; rare
	}
	return model.JavaToString(arg)
}

func normalizeInt(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	}
	Throw("IllegalFormatConversionException: d != %T", v)
	return 0
}

func formatInteger(conv byte, arg any, flags string, width int) string {
	var bits int
	switch arg.(type) {
	case int8:
		bits = 8
	case int16:
		bits = 16
	case int, int32, uint8, uint16:
		bits = 32
	case int64, uint32:
		bits = 64
	case *big.Int:
		bits = 0
	default:
		Throw("IllegalFormatConversionException: %c != %T", conv, arg)
	}
	var s string
	negative := false
	if bi, ok := arg.(*big.Int); ok {
		switch conv {
		case 'd':
			s = new(big.Int).Abs(bi).String()
		case 'x', 'X':
			s = new(big.Int).Abs(bi).Text(16)
		default:
			s = new(big.Int).Abs(bi).Text(8)
		}
		negative = bi.Sign() < 0
	} else {
		v := normalizeInt(arg)
		if conv == 'd' {
			negative = v < 0
			s = strconv.FormatUint(uint64(v), 10)
			if negative {
				s = strconv.FormatUint(uint64(-v), 10)
				if v == math.MinInt64 {
					s = "9223372036854775808"
				}
			}
		} else {
			u := uint64(v)
			if bits < 64 {
				u &= 1<<bits - 1
			}
			if conv == 'o' {
				s = strconv.FormatUint(u, 8)
			} else {
				s = strconv.FormatUint(u, 16)
			}
		}
	}
	if conv == 'd' && strings.Contains(flags, ",") {
		s = group(s)
	}
	if conv == 'X' {
		s = strings.ToUpper(s)
	}
	prefix := ""
	if strings.Contains(flags, "#") {
		switch conv {
		case 'x':
			prefix = "0x"
		case 'X':
			prefix = "0X"
		case 'o':
			prefix = "0"
		}
	}
	switch {
	case negative && strings.Contains(flags, "("):
		prefix, s = "(", s+")"
	case negative:
		prefix = "-" + prefix
	case strings.Contains(flags, "+"):
		prefix = "+" + prefix
	case strings.Contains(flags, " "):
		prefix = " " + prefix
	}
	if strings.Contains(flags, "0") && width > len(prefix)+len(s) {
		s = strings.Repeat("0", width-len(prefix)-len(s)) + s
	}
	return pad(prefix+s, width, flags)
}

func group(digits string) string {
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func formatFloat(conv byte, v float64, flags string, width, precision int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		s := "NaN"
		if math.IsInf(v, 1) {
			s = "Infinity"
			if strings.Contains(flags, "+") {
				s = "+Infinity"
			}
		} else if math.IsInf(v, -1) {
			s = "-Infinity"
		}
		return pad(s, width, flags)
	}
	negative := math.Signbit(v)
	// The shortest decimal digits, rounded half up at the precision.
	digits := strconv.FormatFloat(math.Abs(v), 'e', -1, 64) // d.ddddde±x
	mant, expText, _ := strings.Cut(digits, "e")
	exp, _ := strconv.Atoi(expText)
	mant = strings.Replace(mant, ".", "", 1)
	if math.Abs(v) == math.SmallestNonzeroFloat64 {
		mant = "49" // Java's FloatingDecimal digits of Double.MIN_VALUE
	}
	var s string
	if conv == 'f' {
		// value = 0.mant × 10^(exp+1)
		point := exp + 1
		s = roundHalfUp(mant, point, precision)
	} else {
		if v == 0 {
			mant, exp = "0", 0
		}
		r := roundHalfUp(mant, 1, precision)
		// rounding up may carry into a new leading digit: 9.99 -> 10.0
		if intPart, _, _ := strings.Cut(r, "."); len(intPart) > 1 {
			exp++
			r = roundHalfUp(strings.Replace(r, ".", "", 1), 1, precision)
		}
		e := "e"
		if conv == 'E' {
			e = "E"
		}
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		s = r + e + sign + strings.Repeat("0", max(0, 2-len(strconv.Itoa(exp)))) + strconv.Itoa(exp)
	}
	if conv == 'f' && strings.Contains(flags, ",") {
		intPart, frac, hasFrac := strings.Cut(s, ".")
		s = group(intPart)
		if hasFrac {
			s += "." + frac
		}
	}
	prefix := ""
	switch {
	case negative && strings.Contains(flags, "("):
		prefix, s = "(", s+")"
	case negative:
		prefix = "-"
	case strings.Contains(flags, "+"):
		prefix = "+"
	case strings.Contains(flags, " "):
		prefix = " "
	}
	if strings.Contains(flags, "0") && width > len(prefix)+len(s) {
		s = strings.Repeat("0", width-len(prefix)-len(s)) + s
	}
	return pad(prefix+s, width, flags)
}

// roundHalfUp renders the digits (with the decimal point after point digits; point may be
// negative or beyond the digits) with precision fraction digits, rounding half up.
func roundHalfUp(digits string, point, precision int) string {
	// Normalize to integer and fraction digit strings.
	var intPart, frac string
	switch {
	case point <= 0:
		intPart, frac = "0", strings.Repeat("0", -point)+digits
	case point >= len(digits):
		intPart, frac = digits+strings.Repeat("0", point-len(digits)), ""
	default:
		intPart, frac = digits[:point], digits[point:]
	}
	if len(frac) <= precision {
		frac += strings.Repeat("0", precision-len(frac))
	} else {
		roundUp := frac[precision] >= '5'
		frac = frac[:precision]
		if roundUp {
			n := []byte(intPart + frac)
			i := len(n) - 1
			for ; i >= 0; i-- {
				if n[i] == '9' {
					n[i] = '0'
					continue
				}
				n[i]++
				break
			}
			s := string(n)
			if i < 0 {
				s = "1" + s
			}
			intPart, frac = s[:len(s)-precision], s[len(s)-precision:]
		}
	}
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	if precision == 0 {
		return intPart
	}
	return intPart + "." + frac
}
