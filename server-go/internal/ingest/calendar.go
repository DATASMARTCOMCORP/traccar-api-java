package ingest

import (
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/ical"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// calendars caches parsed calendars by id, re-parsed when the data changes.
var calendars sync.Map // id → parsedCalendar

type parsedCalendar struct {
	data string
	cal  *ical.Calendar
}

// calendarContains is Calendar.checkMoment. The JVM defaults ical4j depends on are the
// server's zone and the locale's first day of the week (Sunday: production's JVM runs
// with LANG=C.UTF-8, locale "en").
func (p *Processor) calendarContains(c model.Calendar, at time.Time) (bool, error) {
	var cal *ical.Calendar
	if v, ok := calendars.Load(c.ID); ok && v.(parsedCalendar).data == string(c.Data) {
		cal = v.(parsedCalendar).cal
	} else {
		parsed, err := ical.Parse(c.Data)
		if err != nil {
			return false, err
		}
		cal = parsed
		calendars.Store(c.ID, parsedCalendar{data: string(c.Data), cal: cal})
	}
	return cal.Contains(at, ical.Options{DefaultZone: p.st.Location(), FirstDayOfWeek: time.Sunday})
}
