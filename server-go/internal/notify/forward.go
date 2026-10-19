package notify

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/config"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// forwarder is EventForwarderJson: each event is posted to event.forward.url as JSON.
type forwarder struct {
	url     string
	headers [][2]string
	client  *http.Client
	log     *slog.Logger
	// badHeader is a header line without ':', which makes every forward fail (Java's
	// ArrayIndexOutOfBoundsException).
	badHeader string
}

func newForwarder(cfg *config.Config, log *slog.Logger) (*forwarder, error) {
	url, ok := cfg.Lookup("event.forward.url")
	if !ok {
		return nil, nil
	}
	if t := cfg.String("event.forward.type", "json"); t != "json" {
		log.Warn("event forwarder not ported; events are not forwarded", "event.forward.type", t)
		return nil, nil
	}
	if cfg.Bool("forward.retry.enable", false) {
		log.Warn("forward.retry is not ported; failed forwards are not retried")
	}
	f := &forwarder{url: url, client: &http.Client{Timeout: 30 * time.Second}, log: log}
	if h := cfg.String("event.forward.header", ""); h != "" {
		for _, line := range strings.FieldsFunc(h, func(r rune) bool { return r == '\n' }) {
			line = strings.TrimSuffix(line, "\r")
			name, value, ok := strings.Cut(line, ":")
			if !ok {
				f.badHeader = line
				continue
			}
			f.headers = append(f.headers, [2]string{strings.TrimSpace(name), strings.TrimSpace(value)})
		}
	}
	return f, nil
}

// eventData is EventData's JSON (Jackson, NON_NULL): event, position, device, geofence,
// maintenance.
func eventData(e *model.Event, pos *model.Position, device *model.Device, geofence *model.Geofence, maintenance *model.Maintenance) ([]byte, error) {
	b := []byte{'{'}
	add := func(key string, v interface{ MarshalJSON() ([]byte, error) }) error {
		data, err := v.MarshalJSON()
		if err != nil {
			return err
		}
		if len(b) > 1 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = append(b, key...)
		b = append(b, '"', ':')
		b = append(b, data...)
		return nil
	}
	if err := add("event", *e); err != nil {
		return nil, err
	}
	if pos != nil {
		if err := add("position", *pos); err != nil {
			return nil, err
		}
	}
	if device != nil {
		if err := add("device", *device); err != nil {
			return nil, err
		}
	}
	if geofence != nil {
		if err := add("geofence", *geofence); err != nil {
			return nil, err
		}
	}
	if maintenance != nil {
		if err := add("maintenance", *maintenance); err != nil {
			return nil, err
		}
	}
	return append(b, '}'), nil
}

// forward is NotificationManager.forwardEvent: the payload is built now (with the cached
// device, the event's geofence and maintenance) and posted in the background.
func (m *Manager) forward(c *eventContext, e *model.Event, pos *model.Position) {
	f := m.forwarder
	if f == nil {
		return
	}
	device := c.device
	var geofence *model.Geofence
	if e.GeofenceID != 0 {
		if g, err := store.Get[model.Geofence](c.ctx, m.st, e.GeofenceID); err == nil {
			geofence = &g
		}
	}
	var maintenance *model.Maintenance
	if e.MaintenanceID != 0 {
		if mt, err := store.Get[model.Maintenance](c.ctx, m.st, e.MaintenanceID); err == nil {
			maintenance = &mt
		}
	}
	body, err := eventData(e, pos, &device, geofence, maintenance)
	if err != nil {
		m.log.Warn("Event forwarding failed", "error", err)
		return
	}
	go func() {
		if err := f.post(body); err != nil {
			m.log.Warn("Event forwarding failed", "error", err)
		}
	}()
}

func (f *forwarder) post(body []byte) error {
	if f.badHeader != "" {
		return fmt.Errorf("event.forward.header: %q has no ':'", f.badHeader)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for _, h := range f.headers {
		req.Header.Set(h[0], h[1])
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP code %d", resp.StatusCode)
	}
	return nil
}

// localAddress is InetAddress.getLocalHost().getHostAddress(): the host name's address.
func localAddress() string {
	host, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		return "localhost"
	}
	return addrs[0]
}
