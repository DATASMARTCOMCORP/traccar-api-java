package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Query parameters as Jersey converts them for @QueryParam:
//   - absent or empty primitives take their default (false, 0)
//   - booleans are true only for "true" (any case)
//   - a value that doesn't convert is a 404 (Jersey's NotFoundException for query
//     parameters)
//   - dates use DateUtil.parseDate (ISO_ZONED_DATE_TIME)

var errBadParam = &httpError{status: http.StatusNotFound, msg: "Not Found", fromResource: true}

func qBool(r *request, name string) bool {
	return strings.EqualFold(r.URL.Query().Get(name), "true")
}

func qInt64(r *request, name string) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, errBadParam
	}
	return n, nil
}

func qInt(r *request, name string) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, errBadParam
	}
	return int(n), nil
}

// qInt64s is a List<Long> parameter: every value, in order.
func qInt64s(r *request, name string) ([]int64, error) {
	var out []int64
	for _, v := range r.URL.Query()[name] {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, errBadParam
		}
		out = append(out, n)
	}
	return out, nil
}

// qDate returns the parameter as a date, or a zero Time when absent.
func qDate(r *request, name string) (model.Time, error) {
	v, ok := r.URL.Query()[name]
	if !ok || len(v) == 0 {
		return model.Time{}, nil
	}
	t, err := parseZonedDate(v[0])
	if err != nil {
		return model.Time{}, errBadParam
	}
	return model.NewTime(t), nil
}

// parseZonedDate is DateTimeFormatter.ISO_ZONED_DATE_TIME: date, time (seconds and
// fraction optional), an offset (Z, +03:00, +0300 is not accepted), and an optional
// [Region/City] suffix.
func parseZonedDate(s string) (time.Time, error) {
	if i := strings.IndexByte(s, '['); i > 0 && strings.HasSuffix(s, "]") {
		s = s[:i]
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, &time.ParseError{Value: s, Message: ": not an ISO zoned date-time"}
}

func parseInt64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }
