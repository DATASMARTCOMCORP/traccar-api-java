package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/live"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// notificator is a Notificator: it sends one notification of an event to one user.
type notificator interface {
	send(ctx context.Context, in Input) error
}

// webNotificator is NotificatorWeb: a copy of the event, with the digest as its "message"
// attribute, goes to the user's sockets.
type webNotificator struct {
	format *Formatter
	hub    *live.Hub
}

func (n *webNotificator) send(_ context.Context, in Input) error {
	m, err := n.format.Format(in)
	if err != nil {
		return err
	}
	e := *in.Event
	e.Attributes = model.Attributes{}
	for k, v := range in.Event.Attributes.All() {
		e.Attributes.Set(k, v)
	}
	e.Attributes.Set("message", m.Digest)
	n.hub.UpdateEvent(in.User.ID, e)
	return nil
}

// mailNotificator is NotificatorMail: subject and body to the user's e-mail.
type mailNotificator struct {
	format *Formatter
	mail   *mailer
	sent   func()
}

func (n *mailNotificator) send(_ context.Context, in Input) error {
	m, err := n.format.Format(in)
	if err != nil {
		return err
	}
	return n.mail.send(in.User, false, m.Subject, m.Body, n.sent)
}

// smsNotificator is NotificatorSms: the digest to the user's phone, if any.
type smsNotificator struct {
	format *Formatter
	sms    *smsClient
	sent   func()
}

func (n *smsNotificator) send(_ context.Context, in Input) error {
	m, err := n.format.Format(in)
	if err != nil {
		return err
	}
	if in.User.Phone == nil {
		return nil
	}
	if n.sms == nil {
		return errors.New("SMS not configured")
	}
	n.sent()
	return n.sms.send(*in.User.Phone, m.Digest)
}

// commandNotificator is NotificatorCommand: the notification's saved command goes to the
// event's device (CommandsManager.sendCommand: live, else queued).
type commandNotificator struct {
	st      *store.Store
	deliver func(ctx context.Context, c *model.Command) (bool, error)
}

func (n *commandNotificator) sendCommand(ctx context.Context, c *model.Command) error {
	if c.TextChannel {
		return errors.New("SMS commands are not ported")
	}
	sent, err := n.deliver(ctx, c)
	if err != nil || sent {
		return err
	}
	if c.Attributes.JavaBool(model.CommandKeyNoQueue) {
		return errors.New("Failed to send command")
	}
	q := model.QueuedCommand{DeviceID: c.DeviceID, Type: c.Type, TextChannel: c.TextChannel}
	for k, v := range c.Attributes.All() {
		q.Attributes.Set(k, v)
	}
	return store.Insert(ctx, n.st, &q)
}

func (n *commandNotificator) send(ctx context.Context, in Input) error {
	if in.Notification == nil || in.Notification.CommandID <= 0 {
		return errors.New("Saved command not provided")
	}
	c, err := store.Get[model.Command](ctx, n.st, in.Notification.CommandID)
	if err != nil {
		return fmt.Errorf("saved command %d: %w", in.Notification.CommandID, err)
	}
	c.DeviceID = in.Event.DeviceID
	return n.sendCommand(ctx, &c)
}
