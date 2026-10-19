// Package notify is what happens to an event after it's stored (NotificationManager): it
// is forwarded (event.forward), then each notification that applies sends it to its users
// through the notificators (web, mail, sms, command), with texts from the Velocity
// templates under templates.root.
package notify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/javatime"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/velocity"
)

// Message is a NotificationMessage. Subject is nil when the template sets none; Digest
// falls back to the body.
type Message struct {
	Subject  *string
	Digest   string
	Body     string
	Priority bool
}

// Formatter is NotificationFormatter and TextTemplateFormatter.
type Formatter struct {
	Root         string         // templates.root
	Translations string         // web.localizationPath
	WebURL       string         // WebHelper.retrieveWebUrl
	Zone         *time.Location // the JVM's default zone
	// Token makes a user's token (TokenManager.generateToken); an error leaves $token
	// unset, as Java logs and continues.
	Token func(userID int64) (string, error)
	Now   func() time.Time // for $dateTool.getDate()
}

// Input is what a notification is about: the cached objects Java's formatter reads.
type Input struct {
	Notification *model.Notification // nil outside notifications
	User         *model.User
	Server       *model.Server
	Device       *model.Device
	Event        *model.Event
	Position     *model.Position
	Geofence     *model.Geofence // read when the event has a geofence id (nil if missing)
	Maintenance  *model.Maintenance
	// Driver is the linked driver with the event's driverUniqueId; DriverSet says the event
	// has one (then $driver is defined, possibly null).
	Driver    *model.Driver
	DriverSet bool
}

var languagePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// errInvalidLanguage is LocaleManager's IllegalArgumentException.
var errInvalidLanguage = errors.New("invalid language")

// lookupString is UserUtil.lookupStringAttribute.
func lookupString(server *model.Server, user *model.User, key string) *string {
	sp := server.Attributes.JavaString(key)
	up := user.Attributes.JavaString(key)
	if server.ForceSettings {
		if sp != nil {
			return sp
		}
		return up
	}
	if up != nil {
		return up
	}
	return sp
}

func stringOr(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}

// prepare is TextTemplateFormatter.prepareContext.
func (f *Formatter) prepare(server *model.Server, user *model.User) velocity.Context {
	ctx := velocity.Context{}
	put := func(k string, v any) {
		if v != nil {
			ctx[k] = v
		}
	}
	zone := f.Zone
	if zone == nil {
		zone = time.Local
	}
	if user != nil {
		put("user", userBean(user))
		tz := zone
		if id := lookupString(server, user, "timezone"); id != nil {
			tz = javatime.TimeZone(*id)
		}
		put("timezone", velocity.TimeZone{Location: tz})
		if f.Token != nil {
			if token, err := f.Token(user.ID); err == nil {
				put("token", token)
			}
		}
	}
	put("webUrl", f.WebURL)
	put("dateTool", velocity.DateTool{Zone: zone, Now: f.Now})
	put("esc", velocity.EscapeTool{})
	put("numberTool", velocity.NumberTool{})
	put("locale", velocity.Locale("en_US"))
	if lang := lookupString(server, user, "language"); lang != nil {
		put("language", *lang)
	}
	return ctx
}

// context is prepareContext plus formatMessage's objects.
func (f *Formatter) context(in Input) velocity.Context {
	ctx := f.prepare(in.Server, in.User)
	put := func(k string, v any) {
		if v != nil {
			ctx[k] = v
		}
	}
	put("notification", notificationBean(in.Notification))
	put("device", deviceBean(in.Device))
	put("event", eventBean(in.Event))
	if in.Position != nil {
		put("position", positionBean(in.Position))
		put("speedUnit", stringOr(lookupString(in.Server, in.User, "speedUnit"), "kn"))
		put("distanceUnit", stringOr(lookupString(in.Server, in.User, "distanceUnit"), "km"))
		put("volumeUnit", stringOr(lookupString(in.Server, in.User, "volumeUnit"), "ltr"))
	}
	if in.Event.GeofenceID != 0 {
		put("geofence", geofenceBean(in.Geofence))
	}
	if in.Event.MaintenanceID != 0 {
		put("maintenance", maintenanceBean(in.Maintenance))
	}
	if in.DriverSet {
		put("driver", driverBean(in.Driver))
	}
	return ctx
}

// Format is NotificationFormatter.formatMessage: the template named after the event type,
// in the user's language or English.
func (f *Formatter) Format(in Input) (Message, error) {
	ctx := f.context(in)
	lang, _ := ctx["language"].(string)
	if lang != "" && !languagePattern.MatchString(lang) {
		return Message{}, errInvalidLanguage
	}
	bundle, err := f.bundle(lang)
	if err != nil {
		return Message{}, err
	}
	ctx["translations"] = bundle
	priority := in.Notification != nil && in.Notification.Attributes.JavaBool("priority")
	name := in.Event.Type
	path := f.templateFile(lang, name+".vm")
	if path == "" {
		return Message{Subject: &name, Digest: name, Body: name, Priority: priority}, nil
	}
	return renderFile(path, ctx, priority)
}

// TextExtra are the variables a text template gets besides prepareContext's.
type TextExtra struct {
	Expiration model.Time    // $expiration (expiration mails)
	Device     *model.Device // $device (device expiration mails)
	ReportURL  string        // $reportUrl (scheduled reports)
}

// Text is TextTemplateFormatter.formatMessage: the named template in the user's language
// or English, without the notification's objects and translations.
func (f *Formatter) Text(server *model.Server, user *model.User, name string, x TextExtra) (Message, error) {
	ctx := f.prepare(server, user)
	if !x.Expiration.IsZero() {
		ctx["expiration"] = velocity.NewDate(x.Expiration.Time)
	}
	if x.Device != nil {
		ctx["device"] = deviceBean(x.Device)
	}
	if x.ReportURL != "" {
		ctx["reportUrl"] = x.ReportURL
	}
	lang, _ := ctx["language"].(string)
	if lang != "" && !languagePattern.MatchString(lang) {
		return Message{}, errInvalidLanguage
	}
	path := f.templateFile(lang, name+".vm")
	if path == "" {
		return Message{Subject: &name, Digest: name, Body: name}, nil
	}
	return renderFile(path, ctx, false)
}

// templateFile is LocaleManager.getTemplateFile for notifications ("" when none).
func (f *Formatter) templateFile(lang, file string) string {
	languages := []string{"en"}
	if lang != "" {
		languages = []string{lang, "en"}
	}
	for _, l := range languages {
		path := filepath.Join(f.Root, "notifications", l, file)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// renderFile merges a template file with the context: subject and digest are the
// variables it sets.
func renderFile(path string, ctx velocity.Context, priority bool) (Message, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return Message{}, err
	}
	tpl, err := velocity.Parse(string(src))
	if err != nil {
		return Message{}, err
	}
	body, err := tpl.Render(ctx)
	if err != nil {
		return Message{}, err
	}
	m := Message{Body: body, Priority: priority, Digest: body}
	if v, ok := ctx["subject"]; ok {
		s := velocity.String(v)
		m.Subject = &s
	}
	if v, ok := ctx["digest"]; ok {
		m.Digest = velocity.String(v)
	}
	return m, nil
}

// bundle is LocaleManager.getBundle: <web.localizationPath>/<language>.json, else en.json,
// else empty.
func (f *Formatter) bundle(lang string) (map[string]string, error) {
	if lang == "" {
		lang = "en"
	}
	path := filepath.Join(f.Translations, lang+".json")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(f.Translations, "en.json")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}
