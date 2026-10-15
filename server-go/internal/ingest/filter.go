package ingest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/units"
)

// filterHandler is FilterHandler (filter.enable, on by default): it drops positions that
// match any enabled filter. A lookup Java can't convert fails the whole handler, and the
// position is then kept.
func (p *Processor) filterHandler(ctx context.Context, dc *deviceContext, pos *model.Position) (bool, error) {
	f := filterRun{p: p, dc: dc, pos: pos, now: time.Now()}
	types, err := f.run(ctx)
	if err != nil {
		return false, err
	}
	if len(types) > 0 {
		p.log.Info("Position filtered by " + strings.Join(types, " ") + " filters from device: " + dc.device.UniqueID)
		return true, nil
	}
	return false, nil
}

type filterRun struct {
	p   *Processor
	dc  *deviceContext
	pos *model.Position
	now time.Time
}

func (f *filterRun) flag(ctx context.Context, k key) (bool, error) {
	v, err := f.dc.lookupBool(ctx, k)
	return v != nil && *v, err
}

func (f *filterRun) run(ctx context.Context) ([]string, error) {
	pos := f.pos
	var types []string
	add := func(ok bool, err error, name string) error {
		if err == nil && ok {
			types = append(types, name)
		}
		return err
	}

	invalid, err := f.flag(ctx, keyFilterInvalid)
	invalid = invalid && (!pos.Valid || pos.Latitude > 90 || pos.Longitude > 180 || pos.Latitude < -90 || pos.Longitude < -180)
	if err := add(invalid, err, "Invalid"); err != nil {
		return nil, err
	}
	zero, err := f.flag(ctx, keyFilterZero)
	if err := add(zero && pos.Latitude == 0 && pos.Longitude == 0, err, "Zero"); err != nil {
		return nil, err
	}
	outdated, err := f.flag(ctx, keyFilterOutdated)
	if err := add(outdated && pos.Outdated, err, "Outdated"); err != nil {
		return nil, err
	}
	future, err := f.dc.lookupLong(ctx, keyFilterFuture, 64)
	if err != nil {
		return nil, err
	}
	if future == nil {
		v := int64(86400)
		future = &v
	}
	if pos.FixTime.Time.UnixMilli() > f.now.UnixMilli()+*future*1000 {
		types = append(types, "Future")
	}
	past, err := f.dc.lookupLong(ctx, keyFilterPast, 64)
	if err != nil {
		return nil, err
	}
	if past != nil && pos.FixTime.Time.UnixMilli() < f.now.UnixMilli()-*past*1000 {
		types = append(types, "Past")
	}
	accuracy, err := f.dc.lookupLong(ctx, keyFilterAccuracy, 32)
	if err != nil {
		return nil, err
	}
	if accuracy != nil && pos.Accuracy > float64(*accuracy) {
		types = append(types, "Accuracy")
	}
	approximate, err := f.flag(ctx, keyFilterApproximate)
	if err := add(approximate && pos.Attributes.JavaBool(model.KeyApproximate), err, "Approximate"); err != nil {
		return nil, err
	}

	last := f.p.cache.last(pos.DeviceID)
	duplicate, err := f.duplicate(ctx, last)
	if err != nil {
		return nil, err
	}
	if duplicate {
		if skip, err := f.skip(ctx, last); err != nil {
			return nil, err
		} else if !skip {
			types = append(types, "Duplicate")
		}
	}
	static, err := f.flag(ctx, keyFilterStatic)
	if err != nil {
		return nil, err
	}
	if static && pos.Speed == 0 {
		if skip, err := f.skip(ctx, last); err != nil {
			return nil, err
		} else if !skip {
			types = append(types, "Static")
		}
	}
	minDistance, err := f.dc.lookupLong(ctx, keyFilterDistance, 32)
	if err != nil {
		return nil, err
	}
	if minDistance != nil && last != nil {
		d, err := pos.Attributes.JavaDouble(model.KeyDistance, 0)
		if err != nil {
			return nil, err
		}
		if d < float64(*minDistance) {
			if skip, err := f.skip(ctx, last); err != nil {
				return nil, err
			} else if !skip {
				types = append(types, "Distance")
			}
		}
	}
	maxSpeed, err := f.dc.lookupLong(ctx, keyFilterMaxSpeed, 32)
	if err != nil {
		return nil, err
	}
	if maxSpeed != nil && last != nil {
		d, err := pos.Attributes.JavaDouble(model.KeyDistance, 0)
		if err != nil {
			return nil, err
		}
		t := float64(pos.FixTime.Time.UnixMilli() - last.FixTime.Time.UnixMilli())
		if t > 0 && units.KnotsFromMps(d/(t/1000)) > float64(*maxSpeed) {
			types = append(types, "MaxSpeed")
		}
	}
	minPeriod, err := f.dc.lookupLong(ctx, keyFilterMinPeriod, 32)
	if err != nil {
		return nil, err
	}
	if minPeriod != nil && last != nil {
		t := pos.FixTime.Time.UnixMilli() - last.FixTime.Time.UnixMilli()
		if t > 0 && t < *minPeriod*1000 {
			types = append(types, "MinPeriod")
		}
	}
	dailyLimit, err := f.dc.lookupLong(ctx, keyFilterDailyLimit, 32)
	if err != nil {
		return nil, err
	}
	if dailyLimit != nil && int64(f.p.stats.messageStoredCount(pos.DeviceID)) >= *dailyLimit {
		interval, err := f.dc.lookupLong(ctx, keyFilterDailyInterval, 32)
		if err != nil {
			return nil, err
		}
		var lastTime int64
		if last != nil {
			lastTime = last.FixTime.Time.UnixMilli()
		}
		if interval == nil || pos.FixTime.Time.UnixMilli()-lastTime < *interval*1000 {
			types = append(types, "DailyLimit")
		}
	}
	if f.dc.device.CalendarID > 0 {
		in, err := f.p.checkCalendar(ctx, f.dc.device.CalendarID, pos.FixTime.Time)
		if err != nil {
			return nil, err
		}
		if !in {
			types = append(types, "Calendar")
		}
	}
	return types, nil
}

// duplicate is filterDuplicate: same fix time as the last position, and no attribute the
// last one lacks.
func (f *filterRun) duplicate(ctx context.Context, last *model.Position) (bool, error) {
	on, err := f.flag(ctx, keyFilterDuplicate)
	if err != nil || !on || last == nil || !f.pos.FixTime.Time.Equal(last.FixTime.Time) {
		return false, err
	}
	for k := range f.pos.Attributes.All() {
		if !last.Attributes.Has(k) {
			return false, nil
		}
	}
	return true, nil
}

// skip is "skipLimit || skipAttributes": an excess-data filter doesn't apply when the
// last position is older than filter.skipLimit, or a filter.skipAttributes attribute
// changed.
func (f *filterRun) skip(ctx context.Context, last *model.Position) (bool, error) {
	limit, err := f.dc.lookupLong(ctx, keyFilterSkipLimit, 64)
	if err != nil {
		return false, err
	}
	if limit != nil && last != nil && f.pos.ServerTime.Time.UnixMilli()-last.ServerTime.Time.UnixMilli() > *limit*1000 {
		return true, nil
	}
	list, err := f.dc.lookupString(ctx, keyFilterSkipAttrs)
	if err != nil {
		return false, err
	}
	if list == nil {
		return false, nil
	}
	for _, name := range javaSplitRegex(*list, spaceOrComma) {
		v, ok := f.pos.Attributes.Get(name)
		if !ok {
			continue
		}
		var lastValue any
		if last != nil {
			lastValue, _ = last.Attributes.Get(name)
		}
		if !javaEquals(v, lastValue) {
			return true, nil
		}
	}
	return false, nil
}

// javaEquals is Objects.equals for attribute values: an Integer never equals a Double,
// as in Java.
func javaEquals(a, b any) bool { return reflect.DeepEqual(a, b) }

// checkCalendar is Calendar.checkMoment for the calendar with this id.
func (p *Processor) checkCalendar(ctx context.Context, calendarID int64, at time.Time) (bool, error) {
	c, err := store.Get[model.Calendar](ctx, p.st, calendarID)
	if errors.Is(err, store.ErrNotFound) {
		return false, errors.New("calendar not found")
	}
	if err != nil {
		return false, err
	}
	return p.calendarContains(c, at)
}
