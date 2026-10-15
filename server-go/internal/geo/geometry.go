package geo

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Geometry is a geofence area parsed from its WKT text (Geofence.getGeometry).
type Geometry interface {
	// Contains is GeofenceGeometry.containsPoint.
	Contains(latitude, longitude float64) bool
}

// ParseGeometry parses a geofence area the way Java does: CIRCLE, POLYGON and LINESTRING,
// with Java's exact (and strict) tokenization. Anything else, including MULTILINESTRING
// and decimal commas, is an error, where Java throws; polylineDistance is the
// geofence's "polylineDistance" attribute (default 25).
func ParseGeometry(area string, polylineDistance float64) (Geometry, error) {
	switch {
	case strings.HasPrefix(area, "CIRCLE"):
		return parseCircle(area)
	case strings.HasPrefix(area, "POLYGON"):
		return parsePolygon(area)
	case strings.HasPrefix(area, "LINESTRING"):
		return parsePolyline(area, polylineDistance)
	}
	return nil, errors.New("unknown geometry type")
}

// bounds is GeofenceGeometry's bounding box check.
type bounds struct{ minLat, minLon, maxLat, maxLon float64 }

func (b bounds) outside(latitude, longitude float64) bool {
	if b.minLon >= 0 || b.maxLon < 0 {
		if latitude < b.minLat || latitude > b.maxLat {
			return true
		}
		if longitude < b.minLon || longitude > b.maxLon {
			return true
		}
	}
	return false
}

// boundsOf is calculateBoundary.
func boundsOf(points []coordinate, padding float64) bounds {
	b := bounds{points[0].lat, points[0].lon, points[0].lat, points[0].lon}
	for _, p := range points[1:] {
		b.minLat, b.minLon = javaMin(b.minLat, p.lat), javaMin(b.minLon, p.lon)
		b.maxLat, b.maxLon = javaMax(b.maxLat, p.lat), javaMax(b.maxLon, p.lon)
	}
	if padding > 0 {
		latPadding := LatitudeDelta(padding)
		lonPadding := javaMax(LongitudeDelta(padding, b.minLat), LongitudeDelta(padding, b.maxLat))
		return bounds{b.minLat - latPadding, b.minLon - lonPadding, b.maxLat + latPadding, b.maxLon + lonPadding}
	}
	return b
}

// javaMin and javaMax are Math.min and Math.max: NaN wins, and -0.0 < 0.0.
func javaMin(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	if a == 0 && b == 0 {
		if math.Signbit(a) {
			return a
		}
		return b
	}
	if a <= b {
		return a
	}
	return b
}

func javaMax(a, b float64) float64 {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.NaN()
	}
	if a == 0 && b == 0 {
		if math.Signbit(a) {
			return b
		}
		return a
	}
	if a >= b {
		return a
	}
	return b
}

type coordinate struct{ lat, lon float64 }

type circle struct {
	bounds
	lat, lon, radius float64
}

func (c *circle) Contains(latitude, longitude float64) bool {
	if c.outside(latitude, longitude) {
		return false
	}
	return Distance(c.lat, c.lon, latitude, longitude) <= c.radius
}

// whitespace is Java's regex \s: [ \t\n\x0B\f\r].
var whitespace = regexp.MustCompile(`[ \t\n\x0B\f\r]`)

// javaSplit is String.split(regex) for a single-character pattern: trailing empty
// strings are removed (and an empty input gives one empty string).
func javaSplit(s string, re *regexp.Regexp) []string {
	parts := re.Split(s, -1)
	for len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 1 && parts[0] == "" && s != "" {
		return nil
	}
	return parts
}

var comma = regexp.MustCompile(`,`)

// content is wkt.substring(wkt.indexOf(open) + len(open), wkt.indexOf(close)); Java
// throws StringIndexOutOfBoundsException when the indexes don't make a valid range.
func content(wkt, open, close string) (string, error) {
	start := strings.Index(wkt, open) + len(open)
	end := strings.Index(wkt, close)
	if start < 0 || end < 0 || start > end || end > len(wkt) {
		return "", fmt.Errorf("bad WKT %q", wkt)
	}
	return wkt[start:end], nil
}

func parseDouble(s string) (float64, error) {
	v, err := model.ParseDouble(s)
	if err != nil {
		return 0, fmt.Errorf("%s is not a double", s)
	}
	return v, nil
}

func parseCircle(wkt string) (*circle, error) {
	text, err := content(wkt, "(", ")")
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, errors.New("no content")
	}
	commaTokens := javaSplit(text, comma)
	if len(commaTokens) != 2 {
		return nil, errors.New("not valid content")
	}
	tokens := javaSplit(commaTokens[0], whitespace)
	if len(tokens) != 2 {
		return nil, errors.New("too much or less coordinates")
	}
	c := &circle{}
	if c.lat, err = parseDouble(tokens[0]); err != nil {
		return nil, err
	}
	if c.lon, err = parseDouble(tokens[1]); err != nil {
		return nil, err
	}
	if c.radius, err = parseDouble(commaTokens[1]); err != nil {
		return nil, err
	}
	c.bounds = bounds{c.lat - c.radius, c.lon - c.radius, c.lat + c.radius, c.lon + c.radius}
	return c, nil
}

// parsePoints reads the "lat lon, lat lon, ..." list of a polygon or polyline.
func parsePoints(text string, minPoints int) ([]coordinate, error) {
	if text == "" {
		return nil, errors.New("no content")
	}
	commaTokens := javaSplit(text, comma)
	if len(commaTokens) < minPoints {
		return nil, errors.New("not valid content")
	}
	points := make([]coordinate, 0, len(commaTokens))
	for _, token := range commaTokens {
		tokens := javaSplit(strings.TrimFunc(token, func(r rune) bool { return r <= ' ' }), whitespace)
		if len(tokens) != 2 {
			return nil, fmt.Errorf("here must be two coordinates: %s", token)
		}
		lat, err := parseDouble(tokens[0])
		if err != nil {
			return nil, err
		}
		lon, err := parseDouble(tokens[1])
		if err != nil {
			return nil, err
		}
		points = append(points, coordinate{lat, lon})
	}
	return points, nil
}

type polygon struct {
	bounds
	points          []coordinate
	constant        []float64
	multiple        []float64
	needsNormalized bool
}

func parsePolygon(wkt string) (*polygon, error) {
	text, err := content(wkt, "((", "))")
	if err != nil {
		return nil, err
	}
	points, err := parsePoints(text, 3)
	if err != nil {
		return nil, err
	}
	p := &polygon{bounds: boundsOf(points, 0), points: points}
	var hasNegative, hasPositive bool
	for _, c := range points {
		if c.lon > 90 {
			hasPositive = true
		} else if c.lon < -90 {
			hasNegative = true
		}
	}
	p.needsNormalized = hasPositive && hasNegative
	n := len(points)
	p.constant = make([]float64, n)
	p.multiple = make([]float64, n)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		pi, pj := points[i], points[j]
		loni, lonj := p.normalize(pi.lon), p.normalize(pj.lon)
		if lonj == loni {
			p.constant[i] = pi.lon
			p.multiple[i] = 0
			continue
		}
		// constant = lat_i - (lon_i*lat_j)/(lon_j-lon_i) + (lon_i*lat_i)/(lon_j-lon_i)
		d := float64(lonj - loni)
		p.constant[i] = float64(float64(pi.lat-float64(float64(loni*pj.lat)/d)) + float64(float64(loni*pi.lat)/d))
		p.multiple[i] = float64(pj.lat-pi.lat) / d
	}
	return p, nil
}

func (p *polygon) normalize(lon float64) float64 {
	if p.needsNormalized && lon < -90 {
		return lon + 360
	}
	return lon
}

func (p *polygon) Contains(latitude, longitude float64) bool {
	if p.outside(latitude, longitude) {
		return false
	}
	n := len(p.points)
	lon := p.normalize(longitude)
	odd := false
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		loni, lonj := p.normalize(p.points[i].lon), p.normalize(p.points[j].lon)
		if loni < lon && lonj >= lon || lonj < lon && loni >= lon {
			if float64(float64(lon*p.multiple[i])+p.constant[i]) < latitude {
				odd = !odd
			}
		}
	}
	return odd
}

type polyline struct {
	bounds
	points   []coordinate
	distance float64
}

func parsePolyline(wkt string, distance float64) (*polyline, error) {
	text, err := content(wkt, "(", ")")
	if err != nil {
		return nil, err
	}
	points, err := parsePoints(text, 2)
	if err != nil {
		return nil, err
	}
	return &polyline{bounds: boundsOf(points, distance), points: points, distance: distance}, nil
}

func (p *polyline) Contains(latitude, longitude float64) bool {
	if p.outside(latitude, longitude) {
		return false
	}
	for i := 1; i < len(p.points); i++ {
		a, b := p.points[i-1], p.points[i]
		if DistanceToLine(latitude, longitude, a.lat, a.lon, b.lat, b.lon) <= p.distance {
			return true
		}
	}
	return false
}
