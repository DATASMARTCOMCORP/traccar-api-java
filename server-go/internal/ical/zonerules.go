package ical

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ical4j doesn't use the JDK's zone rules for TZID values: it builds its own from the
// VTIMEZONE files it bundles, and the result differs from the JDK's where the zone's
// rules changed (America/Sao_Paulo keeps a yearly switch to -02:00 every January).
// ical4j-zones.txt.gz holds those rules as Java builds them, for every zone id Java
// knows, dumped from the production JVM's ical4j 4.2.5 (TzDump in the migration notes):
//
//	Z <id>
//	W <first wall offset>
//	T <epoch second> <offset before> <offset after>     (each transition)
//	R <month> <dom> <dow> <second of day> <end of day> <U|W|S> <std> <before> <after>
//
// Offsets are in seconds. zoneRules answers as java.time.zone.ZoneRules does.

//go:embed ical4j-zones.txt.gz
var ical4jZones []byte

var (
	ical4jOnce  sync.Once
	ical4jRules map[string]*zoneRules
	ical4jErr   error
)

// ical4jZone returns ical4j's rules for a zone id.
func ical4jZone(id string) (*zoneRules, error) {
	ical4jOnce.Do(func() { ical4jRules, ical4jErr = parseZoneRules(ical4jZones) })
	if ical4jErr != nil {
		return nil, ical4jErr
	}
	r, ok := ical4jRules[id]
	if !ok {
		return nil, fmt.Errorf("unknown timezone identifier [%s]", id)
	}
	return r, nil
}

type transitionRule struct {
	month         time.Month
	dom           int
	dow           int // ISO; 0: none
	secondOfDay   int
	endOfDay      bool
	definition    byte // 'U'TC, 'W'ALL, 'S'TANDARD
	standard      int
	before, after int
}

// transition is a ZoneOffsetTransition.
type transition struct {
	epoch         int64
	before, after int
}

func (t transition) isGap() bool { return t.after > t.before }

// localBefore and localAfter are getDateTimeBefore and getDateTimeAfter.
func (t transition) localBefore() time.Time {
	return time.Unix(t.epoch+int64(t.before), 0).UTC()
}

func (t transition) localAfter() time.Time {
	return time.Unix(t.epoch+int64(t.after), 0).UTC()
}

type zoneRules struct {
	instants    []int64     // savingsInstantTransitions
	wallOffsets []int       // one more than instants
	locals      []time.Time // savingsLocalTransitions
	lastRules   []transitionRule

	mu    sync.Mutex
	years map[int][]transition
}

func parseZoneRules(data []byte) (map[string]*zoneRules, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	out := map[string]*zoneRules{}
	var cur *zoneRules
	var transitions []transition
	finish := func() {
		if cur == nil {
			return
		}
		for _, t := range transitions {
			cur.instants = append(cur.instants, t.epoch)
			cur.wallOffsets = append(cur.wallOffsets, t.after)
			if t.isGap() {
				cur.locals = append(cur.locals, t.localBefore(), t.localAfter())
			} else {
				cur.locals = append(cur.locals, t.localAfter(), t.localBefore())
			}
		}
	}
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		nums := make([]int64, len(f))
		for i := 1; i < len(f); i++ {
			if f[0] == "R" && i == 6 || f[0] == "Z" {
				continue
			}
			n, err := strconv.ParseInt(f[i], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("zone data %q: %w", sc.Text(), err)
			}
			nums[i] = n
		}
		switch f[0] {
		case "Z":
			finish()
			cur, transitions = &zoneRules{years: map[int][]transition{}}, nil
			out[f[1]] = cur
		case "W":
			cur.wallOffsets = []int{int(nums[1])}
		case "T":
			transitions = append(transitions, transition{epoch: nums[1], before: int(nums[2]), after: int(nums[3])})
		case "R":
			cur.lastRules = append(cur.lastRules, transitionRule{
				month: time.Month(nums[1]), dom: int(nums[2]), dow: int(nums[3]), secondOfDay: int(nums[4]),
				endOfDay: nums[5] == 1, definition: f[6][0], standard: int(nums[7]), before: int(nums[8]), after: int(nums[9]),
			})
		}
	}
	finish()
	return out, sc.Err()
}

// createTransition is ZoneOffsetTransitionRule.createTransition.
func (r transitionRule) createTransition(year int) transition {
	var date time.Time
	if r.dom < 0 {
		date = newLocal(year, r.month, daysIn(year, r.month)+1+r.dom, 0, 0, 0, 0)
		if r.dow != 0 {
			back := int(floorMod(int64(isoWeekday(date.Weekday())-r.dow), 7))
			date = date.AddDate(0, 0, -back)
		}
	} else {
		date = newLocal(year, r.month, r.dom, 0, 0, 0, 0)
		if r.dow != 0 {
			ahead := int(floorMod(int64(r.dow-isoWeekday(date.Weekday())), 7))
			date = date.AddDate(0, 0, ahead)
		}
	}
	if r.endOfDay {
		date = date.AddDate(0, 0, 1)
	}
	local := date.Add(time.Duration(r.secondOfDay) * time.Second)
	switch r.definition {
	case 'U':
		local = local.Add(time.Duration(r.before) * time.Second)
	case 'S':
		local = local.Add(time.Duration(r.before-r.standard) * time.Second)
	}
	return transition{epoch: local.Unix() - int64(r.before), before: r.before, after: r.after}
}

func (z *zoneRules) transitionsOf(year int) []transition {
	z.mu.Lock()
	defer z.mu.Unlock()
	if t, ok := z.years[year]; ok {
		return t
	}
	out := make([]transition, len(z.lastRules))
	for i, r := range z.lastRules {
		out[i] = r.createTransition(year)
	}
	if year < 2100 {
		z.years[year] = out
	}
	return out
}

// offsetAt is ZoneRules.getOffset(Instant).
func (z *zoneRules) offsetAt(t time.Time) int {
	if len(z.instants) == 0 {
		return z.wallOffsets[0]
	}
	sec := t.Unix()
	if len(z.lastRules) > 0 && sec > z.instants[len(z.instants)-1] {
		last := z.wallOffsets[len(z.wallOffsets)-1]
		year := time.Unix(sec+int64(last), 0).UTC().Year()
		var tr transition
		for _, tr = range z.transitionsOf(year) {
			if sec < tr.epoch {
				return tr.before
			}
		}
		return tr.after
	}
	i := sort.Search(len(z.instants), func(i int) bool { return z.instants[i] > sec })
	// Arrays.binarySearch: an exact match counts as at or after the transition.
	return z.wallOffsets[i]
}

// offsetInfo is ZoneRules.getOffsetInfo: the offset of a local date-time, or the
// transition (gap or overlap) it falls into.
func (z *zoneRules) offsetInfo(local time.Time) (int, *transition) {
	if len(z.locals) == 0 {
		return z.wallOffsets[0], nil
	}
	if len(z.lastRules) > 0 && local.After(z.locals[len(z.locals)-1]) {
		var off int
		var tr *transition
		for _, t := range z.transitionsOf(local.Year()) {
			t := t
			off, tr = findOffsetInfo(local, t)
			if tr != nil || off == t.before {
				return off, tr
			}
		}
		return off, tr
	}
	index, found := sort.Find(len(z.locals), func(i int) int { return local.Compare(z.locals[i]) })
	if !found {
		if index == 0 {
			return z.wallOffsets[0], nil
		}
		index--
	} else if index < len(z.locals)-1 && z.locals[index].Equal(z.locals[index+1]) {
		index++
	}
	if index&1 == 0 {
		before, after := z.wallOffsets[index/2], z.wallOffsets[index/2+1]
		dt := z.locals[index]
		if after <= before {
			dt = z.locals[index+1]
		}
		// The transition from its local date-time before.
		return 0, &transition{epoch: dt.Unix() - int64(before), before: before, after: after}
	}
	return z.wallOffsets[index/2+1], nil
}

func findOffsetInfo(local time.Time, t transition) (int, *transition) {
	before := t.localBefore()
	if t.isGap() {
		if local.Before(before) {
			return t.before, nil
		}
		if local.Before(t.localAfter()) {
			return 0, &t
		}
		return t.after, nil
	}
	if !local.Before(before) {
		return t.after, nil
	}
	if local.Before(t.localAfter()) {
		return t.before, nil
	}
	return 0, &t
}
