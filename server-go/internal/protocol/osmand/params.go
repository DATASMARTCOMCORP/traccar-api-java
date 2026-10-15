package osmand

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// param is a query parameter with all its values, in the order of first appearance
// (Netty's QueryStringDecoder.parameters(), a LinkedHashMap of lists).
type param struct {
	name   string
	values []string
}

// maxParams is QueryStringDecoder.DEFAULT_MAX_PARAMS.
const maxParams = 1024

// queryParams is new QueryStringDecoder(s, hasPath).parameters(): '&' and ';' separate
// parameters, '#' ends them, '+' is a space, %XX escapes are UTF-8 (an invalid escape
// throws), and a name without '=' has an empty value.
func queryParams(s string, hasPath bool) ([]param, error) {
	from := 0
	if hasPath {
		from = strings.IndexAny(s, "?#")
		if from < 0 {
			return nil, nil
		}
	}
	if from >= len(s) {
		return nil, nil
	}
	if s[from] == '?' {
		from++
	}
	var params []param
	index := map[string]int{}
	limit := maxParams
	add := func(nameStart, valueStart, valueEnd int) (bool, error) {
		if nameStart >= valueEnd {
			return false, nil
		}
		if valueStart <= nameStart {
			valueStart = valueEnd + 1
		}
		name, err := decodeComponent(s, nameStart, valueStart-1)
		if err != nil {
			return false, err
		}
		value, err := decodeComponent(s, valueStart, valueEnd)
		if err != nil {
			return false, err
		}
		if i, ok := index[name]; ok {
			params[i].values = append(params[i].values, value)
		} else {
			index[name] = len(params)
			params = append(params, param{name: name, values: []string{value}})
		}
		return true, nil
	}
	nameStart, valueStart := from, -1
	i := from
loop:
	for ; i < len(s); i++ {
		switch s[i] {
		case '=':
			if nameStart == i {
				nameStart = i + 1
			} else if valueStart < nameStart {
				valueStart = i + 1
			}
		case ';', '&':
			added, err := add(nameStart, valueStart, i)
			if err != nil {
				return nil, err
			}
			if added {
				if limit--; limit == 0 {
					return params, nil
				}
			}
			nameStart = i + 1
		case '#':
			break loop
		}
	}
	if _, err := add(nameStart, valueStart, i); err != nil {
		return nil, err
	}
	return params, nil
}

// decodeComponent is QueryStringDecoder.decodeComponent with '+' as a space.
func decodeComponent(s string, from, to int) (string, error) {
	if to-from <= 0 {
		return "", nil
	}
	part := s[from:to]
	if !strings.ContainsAny(part, "%+") {
		return part, nil
	}
	var b strings.Builder
	for i := 0; i < len(part); i++ {
		c := part[i]
		if c == '+' {
			b.WriteByte(' ')
			continue
		}
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		var buf []byte
		for i < len(part) && part[i] == '%' {
			if i+3 > len(part) {
				return "", fmt.Errorf("unterminated escape sequence at index %d of: %s", i, s)
			}
			hi, lo := unhex(part[i+1]), unhex(part[i+2])
			if hi < 0 || lo < 0 {
				return "", fmt.Errorf("invalid hex byte '%s' at index %d of '%s'", part[i+1:i+3], i, s)
			}
			buf = append(buf, byte(hi<<4|lo))
			i += 3
		}
		i--
		b.WriteString(decodeUTF8(buf))
	}
	return b.String(), nil
}

func unhex(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// decodeUTF8 is new String(bytes, UTF_8): java.lang.String.decodeUTF8_UTF16 with
// replacement, including where it resumes after a malformed sequence.
func decodeUTF8(src []byte) string {
	const repl = utf8.RuneError
	var out []rune
	cont := func(b byte) bool { return b&0xC0 == 0x80 }
	malformed3 := func(b1, b2 byte) bool { return b1 == 0xE0 && b2&0xE0 == 0x80 || !cont(b2) }
	sl := len(src)
	for sp := 0; sp < sl; {
		b1 := src[sp]
		sp++
		switch {
		case b1 < 0x80:
			out = append(out, rune(b1))
		case b1>>5 == 0x06 && b1&0x1E != 0: // 2 bytes, not overlong
			if sp < sl {
				b2 := src[sp]
				sp++
				if !cont(b2) {
					out = append(out, repl)
					sp--
				} else {
					out = append(out, rune(b1&0x1F)<<6|rune(b2&0x3F))
				}
				continue
			}
			out = append(out, repl)
			return string(out)
		case b1>>4 == 0x0E: // 3 bytes
			if sp+1 < sl {
				b2, b3 := src[sp], src[sp+1]
				sp += 2
				if malformed3(b1, b2) || !cont(b3) {
					out = append(out, repl)
					sp -= 3
					// malformed3: skip one byte, or two when the second continues.
					if malformed3(src[sp], src[sp+1]) {
						sp++
					} else {
						sp += 2
					}
				} else {
					c := rune(b1&0x0F)<<12 | rune(b2&0x3F)<<6 | rune(b3&0x3F)
					if c >= 0xD800 && c <= 0xDFFF {
						out = append(out, repl)
					} else {
						out = append(out, c)
					}
				}
				continue
			}
			if sp < sl && malformed3(b1, src[sp]) {
				out = append(out, repl)
				continue
			}
			out = append(out, repl)
			return string(out)
		case b1>>3 == 0x1E: // 4 bytes
			if sp+2 < sl {
				b2, b3, b4 := src[sp], src[sp+1], src[sp+2]
				sp += 3
				uc := rune(b1&0x07)<<18 | rune(b2&0x3F)<<12 | rune(b3&0x3F)<<6 | rune(b4&0x3F)
				if !cont(b2) || !cont(b3) || !cont(b4) || uc < 0x10000 || uc > 0x10FFFF {
					out = append(out, repl)
					sp -= 4
					sp += malformed4(src[sp], src[sp+1], src[sp+2])
				} else {
					out = append(out, uc)
				}
				continue
			}
			if b1 > 0xF4 || sp < sl && malformed4x2(b1, src[sp]) {
				out = append(out, repl)
				continue
			}
			sp++
			out = append(out, repl)
			if sp < sl && !cont(src[sp]) {
				continue
			}
			return string(out)
		default:
			out = append(out, repl)
		}
	}
	return string(out)
}

func malformed4x2(b1, b2 byte) bool {
	return b1 == 0xF0 && (b2 < 0x90 || b2 > 0xBF) || b1 == 0xF4 && b2&0xF0 != 0x80 || b2&0xC0 != 0x80
}

// malformed4 is how many bytes a malformed 4-byte sequence covers.
func malformed4(b1, b2, b3 byte) int {
	if b1 > 0xF4 || malformed4x2(b1, b2) {
		return 1
	}
	if b3&0xC0 != 0x80 {
		return 2
	}
	return 3
}
