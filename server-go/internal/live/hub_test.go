package live

import (
	"log/slog"
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// listen registers a listener for a user who sees devices, without the database.
func listen(h *Hub, userID int64, devices ...int64) *Listener {
	l := NewListener()
	if h.listeners[userID] == nil {
		h.listeners[userID] = map[*Listener]struct{}{}
		h.userDevices[userID] = map[int64]struct{}{}
		for _, d := range devices {
			h.userDevices[userID][d] = struct{}{}
			if h.deviceUsers[d] == nil {
				h.deviceUsers[d] = map[int64]struct{}{}
			}
			h.deviceUsers[d][userID] = struct{}{}
		}
	}
	h.listeners[userID][l] = struct{}{}
	return l
}

func drain(l *Listener) []string {
	var out []string
	for {
		select {
		case m, ok := <-l.out:
			if !ok {
				return out
			}
			out = append(out, string(m))
		default:
			return out
		}
	}
}

func TestHub(t *testing.T) {
	h := NewHub(nil, slog.Default())
	a := listen(h, 1, 10, 11)
	b := listen(h, 2, 11)

	h.UpdateDevice(model.Device{ID: 11, Name: "Shared", UniqueID: "u11"})
	if got := drain(a); len(got) != 1 || got[0][:13] != `{"devices":[{` {
		t.Fatalf("device to user 1: %q", got)
	}
	if got := drain(b); len(got) != 1 {
		t.Fatalf("device to user 2: %q", got)
	}

	// Logs only after {"logs": true}.
	id := "u10"
	h.LogDevice(model.LogRecord{Protocol: "osmand", UniqueID: &id, DeviceID: 10, Data: "x", Host: "127.0.0.1"})
	if got := drain(a); len(got) != 0 {
		t.Fatalf("log before asking: %q", got)
	}
	a.SetLogs(true)
	h.LogDevice(model.LogRecord{Protocol: "osmand", UniqueID: &id, DeviceID: 10, Data: "x", Host: "127.0.0.1"})
	want := `{"logs":[{"protocol":"osmand","uniqueId":"u10","deviceId":10,"data":"x","host":"127.0.0.1"}]}`
	if got := drain(a); len(got) != 1 || got[0] != want {
		t.Fatalf("log: %q", got)
	}

	// Unknown devices' logs go to every socket only with web.showUnknownDevices.
	b.SetLogs(true)
	h.LogUnknown(model.LogRecord{Protocol: "osmand", Host: "h"})
	if len(drain(a))+len(drain(b)) != 0 {
		t.Fatal("unknown log without showUnknownDevices")
	}
	h.ShowUnknownDevices = true
	h.LogUnknown(model.LogRecord{Protocol: "osmand", Host: "h"})
	if len(drain(a)) != 1 || len(drain(b)) != 1 {
		t.Fatal("unknown log with showUnknownDevices")
	}

	// A device user 2 creates becomes user 2's only: Java replaces its user set.
	h.LinkDevice(2, 11)
	h.UpdateDevice(model.Device{ID: 11})
	if len(drain(a)) != 0 || len(drain(b)) != 1 {
		t.Fatal("linked device still reaches user 1")
	}
	h.LinkDevice(3, 12) // no sockets: nothing changes
	if _, ok := h.deviceUsers[12]; ok {
		t.Fatal("link without sockets")
	}

	h.Keepalive()
	if got := drain(a); len(got) != 1 || got[0] != "{}" {
		t.Fatalf("keepalive: %q", got)
	}

	// The last socket of a user drops the user's devices.
	h.RemoveListener(1, a)
	if _, ok := h.userDevices[1]; ok {
		t.Fatal("user 1 devices kept")
	}
	if _, ok := h.deviceUsers[10]; ok {
		t.Fatal("device 10 users kept")
	}
	if _, ok := <-a.Messages(); ok {
		t.Fatal("removed listener still open")
	}
	h.UpdateDevice(model.Device{ID: 10}) // no panic on a closed listener
}

func TestNilHub(t *testing.T) {
	var h *Hub
	h.UpdateDevice(model.Device{ID: 1})
	h.UpdatePosition(&model.Position{DeviceID: 1})
	h.LinkDevice(1, 1)
	h.Keepalive()
}
