package kit

import "unicode/utf8"

// UTF8 is new String(bytes, UTF_8): java.lang.String.decodeUTF8_UTF16 with
// replacement, including where it resumes after a malformed sequence.
func UTF8(src []byte) string {
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
