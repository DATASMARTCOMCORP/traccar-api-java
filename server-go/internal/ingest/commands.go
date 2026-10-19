package ingest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/protocol/kit"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// sendQueuedCommands is BaseProtocolDecoder.sendQueuedCommands: every queued command of
// the device (deleted as read) goes out on this connection. The first failure ends it;
// Java's exception then aborts the message and closes a TCP connection.
func (c *Conn) sendQueuedCommands(m *Message, deviceID int64) error {
	queued, err := c.QueuedCommands(m.ctx, deviceID, 0)
	if err != nil {
		return err
	}
	for _, q := range queued {
		command := model.Command{ID: q.ID, Attributes: q.Attributes, DeviceID: q.DeviceID, Type: q.Type, TextChannel: q.TextChannel}
		if err := c.sendDataCommand(m, &command); err != nil {
			return err
		}
	}
	return nil
}

// sendDataCommand is BaseProtocol.sendDataCommand followed by BaseProtocolEncoder.write: a
// supported data command goes through the protocol's encoder; a custom command is sent as
// its data (text with "\r" and "\n" unescaped on a text pipeline, hex bytes otherwise);
// anything else fails. It returns once the data is written.
func (c *Conn) sendDataCommand(m *Message, command *model.Command) (err error) {
	defer kit.Recover(&err)
	var data any
	switch {
	case slices.Contains(c.dataCommands, command.Type):
		if c.pipeline == nil || c.pipeline.Encoder == nil {
			return fmt.Errorf("unsupported message type: Command")
		}
		data = c.pipeline.Encoder.Encode(m, command)
		status := "sent"
		if data == nil {
			status = "not sent"
		}
		c.p.log.Info("["+c.protocol+" "+m.remote+"] command "+status, "uniqueId", c.uniqueID(m, command.DeviceID), "type", command.Type)
	case command.Type == model.CommandCustom:
		text := command.Attributes.JavaString(model.CommandKeyData)
		if text == nil {
			return errors.New("NullPointerException: no command data")
		}
		if c.pipeline != nil && c.pipeline.TextOut {
			data = strings.ReplaceAll(strings.ReplaceAll(*text, `\r`, "\r"), `\n`, "\n")
		} else {
			data = kit.ParseHex(*text)
		}
	default:
		return fmt.Errorf("Command %s is not supported in protocol %s", command.Type, c.protocol)
	}
	if c.write == nil {
		return fmt.Errorf("unsupported message type: %T", data) // an HTTP connection
	}
	return c.write(m.remote, data)
}

func (c *Conn) uniqueID(m *Message, deviceID int64) string {
	d, err := c.Device(m.ctx, deviceID)
	if err != nil || d == nil {
		return ""
	}
	return d.UniqueID
}

// SendCommand is DeviceSession.sendCommand for the device's current session: on a TCP or
// UDP connection the command is encoded and written, serialized with the connection's
// decoding. sent is false when the device has no such session (Java then queues it).
func (p *Processor) SendCommand(ctx context.Context, command *model.Command) (sent bool, err error) {
	p.sessions.mu.Lock()
	ds := p.sessions.byDevice[command.DeviceID]
	p.sessions.mu.Unlock()
	if ds == nil || ds.endpoint.conn == nil || ds.endpoint.conn.http {
		return false, nil
	}
	c := ds.endpoint.conn
	c.loop.Lock()
	defer c.loop.Unlock()
	return true, c.sendDataCommand(NewMessage(ctx, c, ds.endpoint.remote, time.Now()), command)
}

// SetSMS sets the SMS sender of text channel commands (SmsManager); a nil phone or
// message is Java's null.
func (p *Processor) SetSMS(send func(phone, message *string) error) { p.sms = send }

// SendTextCommand is CommandsManager.sendCommand for the text channel: by SMS to the
// device's phone, encoded by the protocol of its latest position (BaseProtocol.
// sendTextCommand). Custom commands send their data as is; without a position only they
// can be sent.
func (p *Processor) SendTextCommand(ctx context.Context, command *model.Command) (err error) {
	if p.sms == nil {
		return errors.New("SMS not configured")
	}
	// Java reads the device and its latest position from storage, not the cache.
	device, err := store.Get[model.Device](ctx, p.st, command.DeviceID)
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("NullPointerException: no device")
	}
	if err != nil {
		return err
	}
	var position *model.Position
	if pos, err := p.st.PositionOf(ctx, device.ID, device.PositionID); err == nil {
		position = &pos
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	custom := func() error { return p.sms(device.Phone, command.Attributes.JavaString(model.CommandKeyData)) }
	if position == nil {
		if command.Type == model.CommandCustom {
			return custom()
		}
		return fmt.Errorf("Command %s is not supported", command.Type)
	}
	var proto Protocol
	ok := false
	if position.Protocol != nil {
		p.servedMu.Lock()
		proto, ok = p.served[*position.Protocol]
		p.servedMu.Unlock()
	}
	if !ok {
		return errors.New("NullPointerException: protocol not running")
	}
	if command.Type == model.CommandCustom {
		return custom()
	}
	if proto.TextEncoder == nil || !slices.Contains(protocol.SupportedCommands(proto.Name, true), command.Type) {
		return fmt.Errorf("Command %s is not supported in protocol %s", command.Type, proto.Name)
	}
	defer kit.Recover(&err)
	conn := &Conn{p: p, protocol: proto.Name}
	encoded, _ := proto.TextEncoder.Encode(NewMessage(ctx, conn, "", time.Now()), command).(string)
	if encoded == "" {
		return errors.New("Failed to encode command")
	}
	return p.sms(device.Phone, &encoded)
}
