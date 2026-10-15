package ingest

import (
	"context"
	"sync"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// statistics is the device-traffic half of Java's StatisticsManager: messages received
// and stored, and active devices per protocol, written to tc_statistics when the day of
// the month changes. (The API counts requests and users in its own process; posting to
// server.statistics is Step 9.)
type statistics struct {
	p *Processor

	mu              sync.Mutex
	day             int
	received        int
	stored          int
	deviceProtocols map[int64]string
	deviceOrder     []int64 // insertion order, for the protocols map
	deviceMessages  map[int64]int
}

// checkSplit is StatisticsManager.checkSplit; the caller holds mu.
func (s *statistics) checkSplit(ctx context.Context) {
	day := time.Now().Day()
	if day == s.day {
		return
	}
	s.day = day
	row := model.Statistics{
		CaptureTime:      model.NewTime(time.Now()),
		ActiveDevices:    len(s.deviceProtocols),
		MessagesReceived: s.received,
		MessagesStored:   s.stored,
	}
	if len(s.deviceProtocols) > 0 {
		// HashMap<String, Integer> filled in the HashMap<Long, String> values' order;
		// the JSON follows the String keys' hash order either way.
		counts := map[string]int{}
		names := model.NewHashSet()
		for _, id := range model.NewLongSetOf(s.deviceOrder).Items() {
			name := s.deviceProtocols[id]
			counts[name]++
			names.Add(name)
		}
		var protocols model.Attributes
		for _, name := range names.Items() {
			protocols.Set(name, counts[name])
		}
		row.Protocols = &protocols
	}
	s.received, s.stored = 0, 0
	s.deviceProtocols, s.deviceOrder, s.deviceMessages = map[int64]string{}, nil, map[int64]int{}
	go func() {
		if err := store.Insert(ctx, s.p.st, &row); err != nil {
			s.p.log.Warn("Error saving statistics", "error", err)
		}
	}()
}

// messageReceived is registerMessageReceived.
func (s *statistics) messageReceived(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkSplit(ctx)
	s.received++
}

// messageStored is registerMessageStored.
func (s *statistics) messageStored(ctx context.Context, deviceID int64, protocol string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checkSplit(ctx)
	s.stored++
	if deviceID != 0 {
		if _, ok := s.deviceProtocols[deviceID]; !ok {
			s.deviceOrder = append(s.deviceOrder, deviceID)
		}
		s.deviceProtocols[deviceID] = protocol
		s.deviceMessages[deviceID]++
	}
}

// messageStoredCount is messageStoredCount(deviceId), for filter.dailyLimit.
func (s *statistics) messageStoredCount(deviceID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceMessages[deviceID]
}
