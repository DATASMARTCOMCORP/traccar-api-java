package geo

import (
	"strings"
	"sync"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// GeofenceContains is Geofence.containsPosition: the floor and ceiling attributes bound
// the altitude, then the area decides. An error is where Java throws: an area it can't
// parse, or a floor, ceiling or polylineDistance that isn't a number.
func GeofenceContains(g *model.Geofence, latitude, longitude, altitude float64) (bool, error) {
	floor, err := g.Attributes.JavaDouble("floor", 0)
	if err != nil {
		return false, err
	}
	if floor != 0 && altitude < floor {
		return false, nil
	}
	ceiling, err := g.Attributes.JavaDouble("ceiling", 0)
	if err != nil {
		return false, err
	}
	if ceiling != 0 && altitude > ceiling {
		return false, nil
	}
	shape, err := geometryOf(g)
	if err != nil {
		return false, err
	}
	return shape.Contains(latitude, longitude), nil
}

// geometries caches parsed geofence areas: some production polygons have hundreds of
// thousands of characters, and every position of a linked device checks them.
var geometries sync.Map // geofence id → cachedGeometry

type cachedGeometry struct {
	area     string
	distance float64
	shape    Geometry
}

func geometryOf(g *model.Geofence) (Geometry, error) {
	distance := 25.0
	if strings.HasPrefix(g.Area, "LINESTRING") {
		d, err := g.Attributes.JavaDouble("polylineDistance", 25)
		if err != nil {
			return nil, err
		}
		distance = d
	}
	if g.ID != 0 {
		if v, ok := geometries.Load(g.ID); ok {
			if c := v.(cachedGeometry); c.area == g.Area && c.distance == distance {
				return c.shape, nil
			}
		}
	}
	shape, err := ParseGeometry(g.Area, distance)
	if err != nil {
		return nil, err
	}
	if g.ID != 0 {
		geometries.Store(g.ID, cachedGeometry{g.Area, distance, shape})
	}
	return shape, nil
}
