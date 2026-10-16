package model

import (
	"errors"
	"time"
)

// Errors of Position's range-checking setters (IllegalArgumentException in Java). They are
// panics, like the Java exceptions: a decoder that sets an out-of-range coordinate fails.
var (
	ErrLatitudeRange  = errors.New("Latitude out of range")
	ErrLongitudeRange = errors.New("Longitude out of range")
)

// SetLatitude is Position.setLatitude: a value outside [-90, 90] panics with
// ErrLatitudeRange.
func (p *Position) SetLatitude(v float64) {
	if v < -90 || v > 90 {
		panic(ErrLatitudeRange)
	}
	p.Latitude = v
}

// SetLongitude is Position.setLongitude: a value outside [-180, 180] panics with
// ErrLongitudeRange.
func (p *Position) SetLongitude(v float64) {
	if v < -180 || v > 180 {
		panic(ErrLongitudeRange)
	}
	p.Longitude = v
}

// SetTime is Position.setTime: the device and fix times.
func (p *Position) SetTime(t time.Time) {
	p.DeviceTime = NewTime(t)
	p.FixTime = NewTime(t)
}

// Set is ExtendedModel.set: nil (and a nil pointer, Java's boxed null) and "" are not
// stored; pointers are stored as their values.
func (p *Position) Set(key string, value any) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		if v == "" {
			return
		}
	case *string:
		if v == nil || *v == "" {
			return
		}
		value = *v
	case *bool:
		if v == nil {
			return
		}
		value = *v
	case *int:
		if v == nil {
			return
		}
		value = *v
	case *int64:
		if v == nil {
			return
		}
		value = *v
	case *float64:
		if v == nil {
			return
		}
		value = *v
	}
	p.Attributes.Set(key, value)
}

// AddAlarm is Position.addAlarm: alarms accumulate comma-separated; "" (Java's null) adds
// nothing.
func (p *Position) AddAlarm(alarm string) {
	if alarm == "" {
		return
	}
	if v, ok := p.Attributes.Get(KeyAlarm); ok {
		p.Attributes.Set(KeyAlarm, JavaToString(v)+","+alarm)
		return
	}
	p.Attributes.Set(KeyAlarm, alarm)
}
