// Package units converts speeds and distances with the ratios of Java's UnitsConverter.
// Positions store speed in knots and distances in meters; decoders and processing
// convert with exactly these constants, so the stored values match Java's.
package units

const (
	knotsToKph = 0.539957
	knotsToMph = 0.868976
	knotsToMps = 1.94384
	knotsToCps = 0.0194384449
	feet       = 0.3048
	mile       = 1609.34
)

func KnotsFromKph(v float64) float64 { return v * knotsToKph }
func KphFromKnots(v float64) float64 { return v / knotsToKph }
func KnotsFromMph(v float64) float64 { return v * knotsToMph }
func MphFromKnots(v float64) float64 { return v / knotsToMph }
func KnotsFromMps(v float64) float64 { return v * knotsToMps }
func MpsFromKnots(v float64) float64 { return v / knotsToMps }
func KnotsFromCps(v float64) float64 { return v * knotsToCps }

func FeetFromMeters(v float64) float64  { return v / feet }
func MetersFromFeet(v float64) float64  { return v * feet }
func MilesFromMeters(v float64) float64 { return v / mile }
func MetersFromMiles(v float64) float64 { return v * mile }
