package geo

import "math"

const plusCodeAlphabet = "23456789CFGHJMPQRVWX"

// PlusCode is the PlusCodesGeocoder address: new OpenLocationCode(lat, lon).getCode() of
// openlocationcode 1.0.4, with its default length of 10 and its arithmetic (a rounded
// integer of 1e-6 grid units, then truncating divisions).
func PlusCode(lat, lon float64) string {
	lat = math.Min(math.Max(lat, -90), 90)
	for lon < -180 {
		lon += 360
	}
	for lon >= 180 {
		lon -= 360
	}
	if lat == 90 {
		lat -= 0.9 * math.Pow(20, -3) // computeLatitudePrecision(10)
	}
	latVal := int64(float64(javaRound((lat+90)*2.5e7*1e6)) / 1e6)
	lngVal := int64(float64(javaRound((lon+180)*8192000*1e6)) / 1e6)
	latVal = int64(float64(latVal) / math.Pow(5, 5))
	lngVal = int64(float64(lngVal) / math.Pow(4, 5))
	rev := make([]byte, 0, 11)
	for i := 0; i < 5; i++ {
		rev = append(rev, plusCodeAlphabet[lngVal%20], plusCodeAlphabet[latVal%20])
		latVal /= 20
		lngVal /= 20
		if i == 0 {
			rev = append(rev, '+')
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return string(rev)
}

// javaRound is Math.round(double): the closest long, ties up; NaN is 0, and values out of
// range saturate.
func javaRound(x float64) int64 {
	if math.IsNaN(x) {
		return 0
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	switch {
	case r >= math.MaxInt64:
		return math.MaxInt64
	case r <= math.MinInt64:
		return math.MinInt64
	}
	return int64(r)
}
