package leafspy

// Copied from osmand (unexported there): Netty's QueryStringDecoder.

import (
	"fmt"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"strings"
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
		b.WriteString(kit.UTF8(buf))
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
