package ingest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// positionCache is the position half of Java's CacheManager: while a device is referenced
// (by a connection's session, a position being processed, or a short task), it keeps the
// device's latest position and, with report.trip.newLogic, the positions of the last
// report.trip.minDuration seconds. Processing compares new fixes with these. Java loads
// them from the database on the first reference and drops them with the last one, so the
// same traffic gives the same comparisons only if Go keeps the same lifetimes; this type
// does exactly that. (Device rows, groups and links are read fresh instead; see
// deviceContext.)
type positionCache struct {
	p       *Processor
	mu      sync.Mutex
	devices map[int64]*cachedDevice
}

type cachedDevice struct {
	refs      map[any]struct{}
	positions []*model.Position // oldest first; the last one is the latest position
	// status is the cached Device's status: the column as read when the device was
	// loaded (blank-padded), then what ConnectionManager.updateDevice last set.
	status string
}

// addDevice is CacheManager.addDevice: the first reference loads the device's positions.
func (c *positionCache) addDevice(ctx context.Context, deviceID int64, ref any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.devices[deviceID]
	if d == nil || len(d.refs) == 0 {
		positions, status, err := c.load(ctx, deviceID)
		if err != nil {
			return err
		}
		d = &cachedDevice{refs: map[any]struct{}{}, positions: positions, status: status}
		c.devices[deviceID] = d
	}
	d.refs[ref] = struct{}{}
	return nil
}

// removeDevice is CacheManager.removeDevice.
func (c *positionCache) removeDevice(deviceID int64, ref any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.devices[deviceID]
	if d == nil {
		return
	}
	delete(d.refs, ref)
	if len(d.refs) == 0 {
		delete(c.devices, deviceID)
	}
}

// load reads what CacheManager.addDevice reads: the device's latest position and, with
// the new trip logic, the positions from minDuration before it (plus the one before
// those), in fix time order.
func (c *positionCache) load(ctx context.Context, deviceID int64) ([]*model.Position, string, error) {
	dc, err := c.p.loadContext(ctx, deviceID)
	if err != nil {
		return nil, "", err
	}
	status := model.StatusOffline
	if dc.device.Status != nil {
		status = *dc.device.Status
	}
	positions, err := c.loadPositions(ctx, dc)
	return positions, status, err
}

func (c *positionCache) loadPositions(ctx context.Context, dc *deviceContext) ([]*model.Position, error) {
	deviceID := dc.device.ID
	if dc.device.PositionID <= 0 {
		return nil, nil
	}
	latest, err := c.p.st.PositionOf(ctx, deviceID, dc.device.PositionID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !c.p.newTripLogic {
		return []*model.Position{&latest}, nil
	}
	minDuration, err := dc.lookupLong(ctx, keyTripMinDuration, 64)
	if err != nil {
		return nil, err
	}
	d := int64(180)
	if minDuration != nil {
		d = *minDuration
	}
	to := latest.FixTime
	from := model.NewTime(to.Time.Add(-time.Duration(d*1000) * time.Millisecond))
	loaded, err := c.p.st.PositionsWithExtra(ctx, deviceID, from, to)
	if err != nil {
		return nil, err
	}
	var positions []*model.Position
	for i := range loaded {
		positions = appendPosition(positions, &loaded[i])
	}
	return positions, nil
}

// appendPosition is CacheManager.appendPosition: a fix older than the latest is dropped,
// and one at the same time replaces it unless it was received earlier.
func appendPosition(positions []*model.Position, p *model.Position) []*model.Position {
	if n := len(positions); n > 0 {
		previous := positions[n-1]
		if p.FixTime.Time.Before(previous.FixTime.Time) {
			return positions
		}
		if p.FixTime.Time.Equal(previous.FixTime.Time) {
			if p.ServerTime.Time.Before(previous.ServerTime.Time) {
				return positions
			}
			positions = positions[:n-1]
		}
	}
	return append(positions, p)
}

// status returns the cached device's status, if the device is cached.
func (c *positionCache) status(deviceID int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.devices[deviceID]; d != nil {
		return d.status, true
	}
	return "", false
}

// setStatus records a status set on the cached device.
func (c *positionCache) setStatus(deviceID int64, status string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.devices[deviceID]; d != nil {
		d.status = status
	}
}

// last is CacheManager.getPosition: the latest position, or nil.
func (c *positionCache) last(deviceID int64) *model.Position {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.devices[deviceID]; d != nil && len(d.positions) > 0 {
		return d.positions[len(d.positions)-1]
	}
	return nil
}

// window is CacheManager.getPositions: the cached positions, oldest first.
func (c *positionCache) window(deviceID int64) []*model.Position {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d := c.devices[deviceID]; d != nil {
		return append([]*model.Position(nil), d.positions...)
	}
	return nil
}

// update is CacheManager.updatePosition (after a position is stored as the latest): it
// appends the position and prunes the window to minDuration, keeping the newest.
func (c *positionCache) update(ctx context.Context, dc *deviceContext, p *model.Position) error {
	var minDuration int64
	if c.p.newTripLogic {
		v, err := dc.lookupLong(ctx, keyTripMinDuration, 64)
		if err != nil {
			return err
		}
		minDuration = 180
		if v != nil {
			minDuration = *v
		}
		minDuration *= 1000
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.devices[p.DeviceID]
	if d == nil {
		return nil
	}
	before := len(d.positions)
	d.positions = appendPosition(d.positions, p)
	if len(d.positions) == before && (before == 0 || d.positions[before-1] != p) {
		return nil // not appended
	}
	if !c.p.newTripLogic {
		if n := len(d.positions); n > 1 {
			d.positions = d.positions[n-1:]
		}
		return nil
	}
	lastTime := p.FixTime.Time.UnixMilli()
	prune := 0
	for _, q := range d.positions[1:] {
		if lastTime-q.FixTime.Time.UnixMilli() < minDuration {
			break
		}
		prune++
	}
	d.positions = d.positions[prune:]
	return nil
}
