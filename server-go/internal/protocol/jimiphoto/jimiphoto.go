// Package jimiphoto is the Jimi photo upload protocol (JimiPhotoProtocol): Jimi cameras
// POST event attachments over HTTP as multipart forms with a JSON callbackBody.
package jimiphoto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ingest"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
)

// Protocol is the protocol's definition: one HTTP server.
func Protocol() ingest.Protocol {
	return ingest.Protocol{Name: "jimiphoto", Servers: []ingest.Server{{HTTP: Decoder{}}}}
}

// Decoder is JimiPhotoProtocolDecoder.
type Decoder struct{}

// part is a multipart body item: an attribute, or a file upload when it has a filename.
type part struct {
	file     bool
	filename string
	content  []byte
	charset  string
}

// formParts reads the multipart body (HttpPostRequestDecoder); the first item of each
// name wins, as getBodyHttpData does. A body that isn't multipart has no file uploads.
func formParts(r *http.Request, body []byte) (map[string]*part, error) {
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || params["boundary"] == "" {
		return map[string]*part{}, nil
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	parts := map[string]*part{}
	for {
		p, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("ErrorDataDecoderException: %w", err)
		}
		_, disposition, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := disposition["name"]
		content, err := io.ReadAll(p)
		if err != nil {
			return nil, fmt.Errorf("ErrorDataDecoderException: %w", err)
		}
		filename, file := disposition["filename"]
		charset := "UTF-8"
		if _, ct, err := mime.ParseMediaType(p.Header.Get("Content-Type")); err == nil && ct["charset"] != "" {
			charset = ct["charset"]
		}
		if _, seen := parts[name]; !seen {
			parts[name] = &part{file: file, filename: filename, content: content, charset: charset}
		}
	}
	return parts, nil
}

// jsonString is javax.json's string serialization.
func jsonString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// jsonResponse is sendJsonResponse: {"code":..,"message":..[,"data":..]}.
func jsonResponse(status int, message string, data *string) *ingest.HTTPResponse {
	var b strings.Builder
	b.WriteString(`{"code":`)
	b.WriteString(strconv.Itoa(status))
	b.WriteString(`,"message":`)
	jsonString(&b, message)
	if data != nil {
		b.WriteString(`,"data":`)
		jsonString(&b, *data)
	}
	b.WriteString("}")
	return &ingest.HTTPResponse{Status: status, Body: []byte(b.String())}
}

// getString is JsonObject.getString(key, default): the value when it is a JSON string.
func getString(body map[string]json.RawMessage, key string) *string {
	raw, ok := body[key]
	if !ok {
		return nil
	}
	var s string
	if raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

// longValue is JsonNumber.longValue (BigDecimal.longValue: truncation).
func longValue(raw json.RawMessage) (int64, error) {
	text := string(raw)
	if v, err := strconv.ParseInt(text, 10, 64); err == nil {
		return v, nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, fmt.Errorf("ClassCastException: %s is not a number", text)
	}
	return int64(math.Trunc(f)), nil
}

// DecodeHTTP decodes one upload.
func (Decoder) DecodeHTTP(_ context.Context, m *ingest.Message, r *http.Request, body []byte) (resp *ingest.HTTPResponse, positions []*model.Position, err error) {
	defer kit.Recover(&err)

	parts, err := formParts(r, body)
	if err != nil {
		return nil, nil, err
	}
	callbackBody, file := parts["callbackBody"], parts["file"]
	if callbackBody != nil && callbackBody.file {
		return nil, nil, errors.New("ClassCastException: callbackBody is a file upload")
	}
	if file != nil && !file.file {
		return nil, nil, errors.New("ClassCastException: file is an attribute")
	}
	if callbackBody == nil || file == nil {
		return jsonResponse(http.StatusBadRequest, "missing field", nil), nil, nil
	}

	value := string(callbackBody.content)
	if !strings.EqualFold(callbackBody.charset, "UTF-8") {
		value = kit.Latin1(callbackBody.content)
	}
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(strings.NewReader(value))
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return nil, nil, fmt.Errorf("JsonParsingException: %v", err)
	}
	imei := getString(fields, "imei")
	if imei == nil {
		return nil, nil, errors.New("NullPointerException: imei")
	}

	ds := m.Session(*imei)
	if ds == nil {
		return jsonResponse(http.StatusNotFound, "unknown device", nil), nil, nil
	}

	filename := file.filename
	extension := filename[strings.LastIndex(filename, ".")+1:]
	savedName := m.WriteMedia(ds.UniqueID, extension, file.content)

	pos := m.NewPosition()
	pos.DeviceID = ds.DeviceID

	if raw, ok := fields["alarmTime"]; ok {
		seconds, err := longValue(raw)
		if err != nil {
			return nil, nil, err
		}
		pos.SetTime(time.UnixMilli(seconds * 1000))
	}

	latitude, longitude := getString(fields, "lat"), getString(fields, "lng")
	if latitude != nil && longitude != nil && *latitude != "" && *longitude != "" {
		pos.Valid = true
		pos.SetLatitude(kit.Double(*latitude))
		pos.SetLongitude(kit.Double(*longitude))
	} else if pos.DeviceTime.IsZero() {
		m.LastLocation(pos, nil)
	} else {
		t := pos.DeviceTime.Time
		m.LastLocation(pos, &t)
	}

	mimeType := getString(fields, "mimeType")
	if mimeType != nil && strings.HasPrefix(*mimeType, "video/") {
		pos.Set(model.KeyVideo, savedName)
	} else {
		pos.Set(model.KeyImage, savedName)
	}

	for _, key := range []string{"businessType", "eventType", "instructionId"} {
		if v := getString(fields, key); v != nil {
			pos.Set(key, *v)
		}
	}

	var data *string
	if savedName != "" {
		data = &savedName
	}
	return jsonResponse(http.StatusOK, "the high success", data), []*model.Position{pos}, nil
}
