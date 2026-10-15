// Package geo holds distance calculations and geofence geometry, computed exactly as
// the Java server computes them (DistanceCalculator and org.traccar.geofence): the
// results end up in stored positions (distance, totalDistance) and decide geofence
// events, so they must match Java to the last bit.
package geo

import (
	"math"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javamath"
)

const (
	earthRadius = 6378137.0 // equatorial, meters
	degToRad    = math.Pi / 180
	// degreesToRadians is Math.toRadians's factor (Math.DEGREES_TO_RADIANS), which is the
	// same double as Math.PI / 180.
	degreesToRadians = 0.017453292519943295
)

// Distance is DistanceCalculator.distance: the haversine distance in meters. The
// explicit float64 conversions keep Java's evaluation order and rounding (no fused
// multiply-add).
func Distance(lat1, lon1, lat2, lon2 float64) float64 {
	dlong := float64(lon2-lon1) * degToRad
	dlat := float64(lat2-lat1) * degToRad
	sinDlat := javamath.Sin(dlat / 2)
	sinDlong := javamath.Sin(dlong / 2)
	cos := float64(javamath.Cos(float64(lat1*degToRad)) * javamath.Cos(float64(lat2*degToRad)))
	a := float64(sinDlat*sinDlat) + float64(float64(cos*sinDlong)*sinDlong)
	c := 2 * javamath.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadius * c
}

// DistanceToLine is DistanceCalculator.distanceToLine: the distance in meters from a
// point to the segment between two points.
func DistanceToLine(pointLat, pointLon, lat1, lon1, lat2, lon2 float64) float64 {
	d0 := Distance(pointLat, pointLon, lat1, lon1)
	d1 := Distance(lat1, lon1, lat2, lon2)
	d2 := Distance(lat2, lon2, pointLat, pointLon)
	d0Squared := float64(d0 * d0)
	d1Squared := float64(d1 * d1)
	d2Squared := float64(d2 * d2)
	if d0Squared > d1Squared+d2Squared {
		return d2
	}
	if d2Squared > d1Squared+d0Squared {
		return d0
	}
	halfP := float64(float64(d0+d1)+d2) * 0.5
	area := math.Sqrt(float64(float64(float64(halfP*float64(halfP-d0))*float64(halfP-d1)) * float64(halfP-d2)))
	return float64(2*area) / d1
}

// LatitudeDelta is DistanceCalculator.getLatitudeDelta: degrees of latitude per meters.
func LatitudeDelta(meters float64) float64 { return meters / 111320 }

// LongitudeDelta is DistanceCalculator.getLongitudeDelta at the given latitude.
func LongitudeDelta(meters, latitude float64) float64 {
	return meters / float64(111320*javamath.Cos(float64(latitude*degreesToRadians)))
}
