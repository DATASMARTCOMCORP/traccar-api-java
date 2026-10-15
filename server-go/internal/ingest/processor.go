// Package ingest receives device traffic and processes positions the way the Java
// server does (MIGRATION.md Step 6): device sessions and status, queued commands, the
// position handlers in Java's order, event detection, and the database writes.
//
// Protocol decoders live in their own packages and call into a Conn, which gives them
// what BaseProtocolDecoder gives Java decoders.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Processor is the shared state of ingestion: sessions, the position cache, buffering,
// per-device processing queues, and the settings Java reads once at startup.
type Processor struct {
	cfg *config.Config
	st  *store.Store
	log *slog.Logger

	cache    *positionCache
	sessions *sessions
	buffer   *buffering
	stats    *statistics

	ctx    context.Context // processing outlives the request that delivered a position
	cancel context.CancelFunc
	work   sync.WaitGroup

	qmu    sync.Mutex
	queues map[int64][]job // per device: the position being processed, then those waiting

	settings
}

// settings are the config values Java's handlers read in their constructors.
type settings struct {
	newTripLogic  bool
	statusEvents  bool
	statusTimeout time.Duration

	registerUnknown         bool
	registerUnknownRegex    *regexp.Regexp
	registerUnknownCategory *string
	registerUnknownGroupID  int64
	throttleUnknown         bool

	bufferingThreshold time.Duration
	delayAck           bool
	saveOriginal       bool
	saveEmpty          bool
	remoteAddress      bool
	ignoreOffline      *model.HashSet // status.ignoreOffline protocols

	filterEnabled     bool
	coordinatesFilter bool
	minError          int
	maxError          int
	timeProtocols     *model.HashSet // nil: time.override applies to all protocols
	latitudeFactor    int
	longitudeFactor   int
	copyAttributes    bool
	useLinkedDriver   bool

	ignoreDuplicateAlerts bool
	overspeedMinDuration  int64 // ms
	overspeedPreferLowest bool
	overspeedMultiplier   float64
	accelerationThreshold float64
	brakingThreshold      float64
}

// NewProcessor reads the settings and returns a processor ready for connections.
func NewProcessor(cfg *config.Config, st *store.Store, log *slog.Logger) (*Processor, error) {
	p := &Processor{cfg: cfg, st: st, log: log, queues: map[int64][]job{}}
	if err := p.readSettings(); err != nil {
		return nil, err
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.cache = &positionCache{p: p, devices: map[int64]*cachedDevice{}}
	p.sessions = newSessions(p)
	p.buffer = newBuffering(p)
	p.stats = &statistics{p: p, day: time.Now().Day(), deviceProtocols: map[int64]string{}, deviceMessages: map[int64]int{}}
	return p, nil
}

func (p *Processor) readSettings() error {
	c := p.cfg
	var err error
	s := &p.settings
	s.newTripLogic = c.Bool("report.trip.newLogic", true)
	s.statusEvents = c.Bool("event.status.enable", false)
	timeout, err := c.Int64("status.timeout", 600)
	if err != nil {
		return err
	}
	s.statusTimeout = time.Duration(timeout) * time.Second

	s.registerUnknown = c.Bool("database.registerUnknown", false)
	if s.registerUnknownRegex, err = registerUnknownPattern(c.String("database.registerUnknown.regex", `\w{3,15}`)); err != nil {
		return fmt.Errorf("config database.registerUnknown.regex: %w", err)
	}
	if v, ok := c.Lookup("database.registerUnknown.defaultCategory"); ok {
		s.registerUnknownCategory = &v
	}
	if s.registerUnknownGroupID, err = c.Int64("database.registerUnknown.defaultGroupId", 0); err != nil {
		return err
	}
	s.throttleUnknown = c.Bool("database.throttleUnknown", false)

	threshold, err := c.Int64("server.buffering.threshold", 3000)
	if err != nil {
		return err
	}
	s.bufferingThreshold = time.Duration(threshold) * time.Millisecond
	s.delayAck = c.Bool("server.delayAcknowledgement", false)
	s.saveOriginal = c.Bool("database.saveOriginal", false)
	s.saveEmpty = c.Bool("database.saveEmpty", false)
	s.remoteAddress = c.Bool("processing.remoteAddress.enable", false)
	if v, ok := c.Lookup("status.ignoreOffline"); ok {
		s.ignoreOffline = model.NewHashSetOf(javaListSplit(v))
	}

	s.filterEnabled = c.Bool("filter.enable", true)
	s.coordinatesFilter = c.Bool("coordinates.filter", false)
	if s.minError, err = c.Int("coordinates.minError", 0); err != nil {
		return err
	}
	if s.maxError, err = c.Int("coordinates.maxError", 0); err != nil {
		return err
	}
	if v, ok := c.Lookup("time.protocols"); ok {
		s.timeProtocols = model.NewHashSetOf(javaListSplit(v))
	}
	s.latitudeFactor = hemisphere(c.String("location.latitudeHemisphere", ""), "N", "S")
	s.longitudeFactor = hemisphere(c.String("location.longitudeHemisphere", ""), "E", "W")
	s.copyAttributes = c.Bool("processing.copyAttributes.enable", false)
	s.useLinkedDriver = c.Bool("processing.useLinkedDriver", false)

	s.ignoreDuplicateAlerts = c.Bool("event.ignoreDuplicateAlerts", true)
	minDuration, err := c.Int64("event.overspeed.minimalDuration", 0)
	if err != nil {
		return err
	}
	s.overspeedMinDuration = minDuration * 1000
	s.overspeedPreferLowest = c.Bool("event.overspeed.preferLowest", false)
	if s.overspeedMultiplier, err = c.Float("event.overspeed.thresholdMultiplier", 1); err != nil {
		return err
	}
	if s.accelerationThreshold, err = c.Float("event.behavior.accelerationThreshold", 0); err != nil {
		return err
	}
	if s.brakingThreshold, err = c.Float("event.behavior.brakingThreshold", 0); err != nil {
		return err
	}
	p.warnUnported()
	return nil
}

// warnUnported logs the processing features configured here that the Go server doesn't
// have yet, so they are noticed before traffic moves to it.
func (p *Processor) warnUnported() {
	c := p.cfg
	for _, k := range []string{"geolocation.enable", "mapMatcher.enable", "speedLimit.enable"} {
		if c.Bool(k, false) {
			p.log.Warn("not ported yet, ignored", "key", k)
		}
	}
	if c.Bool("geocoder.enable", true) && !c.Bool("geocoder.ignorePositions", true) {
		p.log.Warn("geocoding positions is not ported yet; addresses stay empty", "key", "geocoder.ignorePositions")
	}
	if c.Has("forward.url") {
		p.log.Warn("position forwarding is not ported yet", "key", "forward.url")
	}
	if c.Has("event.forward.url") {
		p.log.Warn("event forwarding is not ported yet", "key", "event.forward.url")
	}
}

// javaListSplit is split("[, ]") on a config list.
func javaListSplit(s string) []string {
	parts := listSeparator.Split(s, -1)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

var listSeparator = regexp.MustCompile(`[, ]`)

func hemisphere(v, positive, negative string) int {
	switch {
	case v == "":
		return 0
	case equalsIgnoreCase(v, positive):
		return 1
	case equalsIgnoreCase(v, negative):
		return -1
	}
	return 0
}

// equalsIgnoreCase is String.equalsIgnoreCase for ASCII text.
func equalsIgnoreCase(a, b string) bool { return len(a) == len(b) && foldEqual(a, b) }

func foldEqual(a, b string) bool {
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'a' <= x && x <= 'z' {
			x -= 'a' - 'A'
		}
		if 'a' <= y && y <= 'z' {
			y -= 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// Run runs the periodic tasks (TaskSessionTimeout) until ctx ends.
func (p *Processor) Run(ctx context.Context) {
	period := max(p.statusTimeout/10, time.Second)
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.sessions.sweep(p.ctx)
		}
	}
}

// Shutdown waits for positions in processing (and those still buffered, which are
// released at once) until ctx ends.
func (p *Processor) Shutdown(ctx context.Context) error {
	p.buffer.flush()
	done := make(chan struct{})
	go func() {
		p.work.Wait()
		close(done)
	}()
	select {
	case <-done:
		p.cancel()
		return nil
	case <-ctx.Done():
		p.cancel()
		return ctx.Err()
	}
}
