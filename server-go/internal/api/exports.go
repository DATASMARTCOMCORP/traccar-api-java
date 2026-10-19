package api

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/geo"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// exportRoutes are PositionResource's exports: KML (or zipped as KMZ), CSV and GPX of a
// device's positions in a period, optionally only those inside a geofence.
func (a *API) exportRoutes() {
	a.mux.HandleFunc("GET /api/positions/{format}", func(w http.ResponseWriter, r *http.Request) {
		a.cors(w, r)
		var h handlerFunc
		typ, sub := "application", "json" // kml|kmz have the class's @Produces
		switch r.PathValue("format") {
		case "kml", "kmz":
			h = a.exportKML
		case "csv":
			h, typ, sub = a.exportCSV, "text", "csv"
		case "gpx":
			h, typ, sub = a.exportGPX, "application", "gpx+xml"
		default:
			// Only DELETE /api/positions/{id} matches.
			w.Header().Set("Allow", "DELETE,OPTIONS")
			a.fail(w, r, errStatus(http.StatusMethodNotAllowed, "Method Not Allowed"))
			return
		}
		if !accepts(r.Header.Get("Accept"), typ, sub) {
			a.fail(w, r, errStatus(http.StatusNotAcceptable, "Not Acceptable"))
			return
		}
		req := &request{Request: r}
		if err := a.authenticate(req, authenticated); err != nil {
			a.fail(w, r, err)
			return
		}
		if err := h(w, req); err != nil {
			a.fail(w, r, labeled(err, typ+"/"+sub))
		}
	})
}

// labeled gives an error raised in a resource method that method's media type, as
// Java's error mapper does.
func labeled(err error, contentType string) error {
	var he *httpError
	if errors.As(err, &he) {
		c := *he
		c.contentType = contentType
		return &c
	}
	return &httpError{status: http.StatusBadRequest, msg: err.Error(), contentType: contentType}
}

// exportQuery reads deviceId, geofenceId, from and to, checks the device permission,
// and loads the positions (report.maxPositions at most) inside the geofence, if any.
func (a *API) exportQuery(r *request) (deviceID int64, from, to model.Time, positions []model.Position, err error) {
	if deviceID, err = qInt64(r, "deviceId"); err != nil {
		return
	}
	geofenceID, err := qInt64(r, "geofenceId")
	if err != nil {
		return
	}
	if from, err = qDate(r, "from"); err != nil {
		return
	}
	if to, err = qDate(r, "to"); err != nil {
		return
	}
	if err = a.checkPermission(r, store.KindDevice, deviceID); err != nil {
		return
	}
	if from.IsZero() || to.IsZero() {
		err = errors.New("NullPointerException: missing date") // the positions query's
		return
	}
	var geofence *model.Geofence
	if geofenceID != 0 {
		g, gerr := store.Get[model.Geofence](r.Context(), a.st, geofenceID)
		if gerr == nil {
			geofence = &g
		} else if !errors.Is(gerr, store.ErrNotFound) {
			err = gerr
			return
		}
	}
	all, err := a.st.Positions(r.Context(), deviceID, from, to, a.opts.MaxPositions)
	if err != nil {
		return
	}
	for _, p := range all {
		if geofence != nil {
			in, cerr := geo.GeofenceContains(geofence, p.Latitude, p.Longitude, p.Altitude)
			if cerr != nil {
				err = cerr
				return
			}
			if !in {
				continue
			}
		}
		positions = append(positions, p)
	}
	return
}

// exportDevice is the device by id; Java dereferences null (a 400).
func (a *API) exportDevice(r *request, id int64) (model.Device, error) {
	d, err := store.Get[model.Device](r.Context(), a.st, id)
	if errors.Is(err, store.ErrNotFound) {
		return d, errors.New("NullPointerException: no device")
	}
	return d, err
}

// xmlText is XMLStreamWriter.writeCharacters' escaping.
func xmlText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// exportKML is KmlExportProvider: one LineString of the positions.
func (a *API) exportKML(w http.ResponseWriter, r *request) error {
	deviceID, from, to, positions, err := a.exportQuery(r)
	if err != nil {
		return err
	}
	d, err := a.exportDevice(r, deviceID)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(`<?xml version='1.0' encoding='UTF-8'?><kml xmlns="http://www.opengis.net/kml/2.2"><Document><name>`)
	b.WriteString(xmlText(d.Name))
	b.WriteString("</name><Placemark><name>")
	format := func(t model.Time) string { return t.Time.In(time.Local).Format("2006-01-02 15:04") }
	b.WriteString(xmlText(format(from) + " - " + format(to)))
	b.WriteString("</name><LineString><extrude>1</extrude><tessellate>1</tessellate><altitudeMode>absolute</altitudeMode><coordinates>")
	for i, p := range positions {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(kit.Format("%f,%f,%f", p.Longitude, p.Latitude, p.Altitude))
	}
	b.WriteString("</coordinates></LineString></Placemark></Document></kml>")

	ext := r.PathValue("format")
	data := []byte(b.String())
	contentType := "application/vnd.google-earth.kml+xml"
	if ext == "kmz" {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		f, err := zw.CreateHeader(&zip.FileHeader{Name: "doc.kml", Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		data, contentType = buf.Bytes(), "application/vnd.google-earth.kmz"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "attachment; filename=positions."+ext)
	_, err = w.Write(data)
	return err
}

// csvCell is CsvExportProvider.formatCell: strings trimmed, and blanked when they start
// like a formula; other values by toString; null empty.
func csvCell(v any) string {
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
			return ""
		}
		return s
	}
	if v == nil {
		return ""
	}
	return model.JavaToString(v)
}

// exportCSV is CsvExportProvider: the position columns, then every attribute any of the
// positions has (Java's order of those is a hash set's, different on every start; here,
// first seen first). Values are not quoted.
func (a *API) exportCSV(w http.ResponseWriter, r *request) error {
	_, _, _, positions, err := a.exportQuery(r)
	if err != nil {
		return err
	}
	zone, err := a.userZone(r)
	if err != nil {
		return err
	}
	var keys []string
	seen := map[string]bool{}
	for i := range positions {
		for k := range positions[i].Attributes.All() {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	date := func(t model.Time) string { return t.Time.In(zone).Format("2006-01-02 15:04:05") }
	var b strings.Builder
	header := append([]string{"id", "deviceId", "protocol", "serverTime", "deviceTime", "fixTime", "valid",
		"latitude", "longitude", "altitude", "speed", "course", "address", "accuracy"}, keys...)
	b.WriteString(strings.Join(header, ",") + "\n")
	for i := range positions {
		p := &positions[i]
		var address any
		if p.Address != nil {
			address = *p.Address
		}
		var protocol any
		if p.Protocol != nil {
			protocol = *p.Protocol
		}
		cells := []any{p.ID, p.DeviceID, protocol, date(p.ServerTime), date(p.DeviceTime), date(p.FixTime), p.Valid,
			p.Latitude, p.Longitude, p.Altitude, p.Speed, p.Course, address, p.Accuracy}
		for _, k := range keys {
			v, _ := p.Attributes.Get(k)
			cells = append(cells, v)
		}
		for j, c := range cells {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(csvCell(c))
		}
		b.WriteByte('\n')
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=positions.csv")
	_, err = w.Write([]byte(b.String()))
	return err
}

// exportGPX is GpxExportProvider: one track segment of the positions.
func (a *API) exportGPX(w http.ResponseWriter, r *request) error {
	deviceID, _, _, positions, err := a.exportQuery(r)
	if err != nil {
		return err
	}
	d, err := a.exportDevice(r, deviceID)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(`<?xml version='1.0' encoding='UTF-8'?><gpx version="1.0"><trk><name>`)
	b.WriteString(xmlText(d.Name))
	b.WriteString("</name><trkseg>")
	for _, p := range positions {
		fmt.Fprintf(&b, `<trkpt lat="%s" lon="%s"><ele>%s</ele><time>%s</time></trkpt>`,
			model.JavaToString(p.Latitude), model.JavaToString(p.Longitude), model.JavaToString(p.Altitude), javatime.FormatISOOffset(p.FixTime.Time.In(time.Local)))
	}
	b.WriteString("</trkseg></trk></gpx>")
	w.Header().Set("Content-Type", "application/gpx+xml")
	w.Header().Set("Content-Disposition", "attachment; filename=positions.gpx")
	_, err = w.Write([]byte(b.String()))
	return err
}
