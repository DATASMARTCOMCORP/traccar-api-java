package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/auth"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// postUser is UserResource.add: anonymous registration (the first user becomes
// administrator), managers creating managed users (with their user limit), and
// administrators creating anyone.
func (a *API) postUser(w http.ResponseWriter, r *request) error {
	var u model.User
	if err := a.readForInsert(r, userBean(&u), "user"); err != nil {
		return err
	}
	cur := r.user
	if cur == nil || !cur.Administrator {
		if err := a.checkUserUpdate(r, &model.User{}, &u); err != nil {
			return err
		}
		if cur != nil && cur.UserLimit != 0 {
			if cur.UserLimit > 0 {
				ids, err := store.IDs[model.User](r.Context(), a.st, store.PermissionNoGroups(store.KindUser, cur.ID, store.KindManagedUser))
				if err != nil {
					return err
				}
				if len(ids) >= cur.UserLimit {
					return errors.New("Manager user limit reached")
				}
			}
		} else {
			empty, err := a.st.NoUsers(r.Context())
			if err != nil {
				return err
			}
			srv, err := a.server(r)
			if err != nil {
				return err
			}
			switch {
			case empty:
				u.Administrator = true
			case !srv.Registration:
				return errors.New("Registration disabled")
			}
			if serverFlag(srv, "totpForce") && u.TotpKey == nil {
				return errors.New("One-time password key is required")
			}
			u.DeviceLimit = a.opts.DefaultDeviceLimit
			if a.opts.DefaultExpirationDays > 0 {
				u.ExpirationTime = model.NewTime(time.Now().Add(time.Duration(a.opts.DefaultExpirationDays) * 24 * time.Hour))
			}
		}
	}
	if err := store.Insert(r.Context(), a.st, &u); err != nil {
		return err
	}
	if err := a.st.UpdateUserPassword(r.Context(), &u); err != nil {
		return err
	}
	a.audit(r.Context(), r.Request, "create", r.userID(), "user", u.ID)
	if cur != nil && cur.UserLimit != 0 {
		if err := a.st.AddLink(r.Context(), store.KindUser, cur.ID, store.KindManagedUser, u.ID); err != nil {
			return err
		}
		a.auditLink(r, "link", store.KindUser, cur.ID, store.KindManagedUser, u.ID)
	}
	return writeJSON(w, http.StatusOK, u)
}

// serverFlag reads a boolean server attribute (Keys of type SERVER: totpEnable, ...).
func serverFlag(s *model.Server, key string) bool {
	v, ok := s.Attributes.Get(key)
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true")
	}
	return false
}

// checkUserUpdate is PermissionsService.checkUserUpdate: who may change privileges,
// limits, expiration and restrictions. r.user is nil for anonymous registration, where
// Java's admin check fails with a NullPointerException (also a 400).
func (a *API) checkUserUpdate(r *request, before, after *model.User) error {
	admin := func() error {
		if r.user == nil {
			return errors.New("not authenticated")
		}
		return checkAdmin(r)
	}
	if before.Administrator != after.Administrator || before.DeviceLimit != after.DeviceLimit || before.UserLimit != after.UserLimit {
		if err := admin(); err != nil {
			return err
		}
	}
	if u := r.user; u != nil && !u.ExpirationTime.IsZero() && !before.ExpirationTime.Time.Equal(after.ExpirationTime.Time) &&
		(after.ExpirationTime.IsZero() || u.ExpirationTime.Time.Before(after.ExpirationTime.Time)) {
		if err := admin(); err != nil {
			return err
		}
	}
	if before.Readonly != after.Readonly || before.DeviceReadonly != after.DeviceReadonly || before.Disabled != after.Disabled ||
		before.LimitCommands != after.LimitCommands || before.DisableReports != after.DisableReports || before.FixedEmail != after.FixedEmail {
		switch {
		case r.userID() == after.ID:
			if err := admin(); err != nil {
				return err
			}
		case after.ID > 0:
			if err := a.checkUser(r, after.ID); err != nil {
				return err
			}
		default:
			if r.user == nil {
				return errors.New("not authenticated")
			}
			if err := checkManager(r); err != nil {
				return err
			}
		}
	}
	if before.FixedEmail && before.Email != after.Email {
		return admin()
	}
	return nil
}

// putUser is BaseObjectResource.update for users: the privilege checks above, and a user
// changing only notification tokens or terms acceptance on themselves may do so even when
// read-only.
func (a *API) putUser(w http.ResponseWriter, r *request) error {
	var u model.User
	if err := readJSON(r, userBean(&u)); err != nil {
		return err
	}
	if err := a.checkPermission(r, store.KindUser, u.ID); err != nil {
		return err
	}
	before, err := store.Get[model.User](r.Context(), a.st, u.ID)
	if err != nil {
		return err // missing user: NullPointerException in Java
	}
	if err := a.checkUserUpdate(r, &before, &u); err != nil {
		return err
	}
	skipReadonly := sameUser(r.user, &u, "notificationTokens", "termsAccepted")
	if err := a.checkEditKind(r, store.KindUser, false, skipReadonly); err != nil {
		return err
	}
	if err := store.Update(r.Context(), a.st, &u); err != nil {
		return err
	}
	if u.HashedPassword != nil {
		if err := a.st.UpdateUserPassword(r.Context(), &u); err != nil {
			return err
		}
	}
	a.audit(r.Context(), r.Request, "edit", r.user.ID, "user", u.ID)
	return writeJSON(w, http.StatusOK, u)
}

// sameUser is User.compare: every field equal (ignoring the password hash), and the
// attributes equal apart from the excluded keys.
func sameUser(a, b *model.User, excluded ...string) bool {
	eq := func(x, y *string) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	if a.ID != b.ID || a.Name != b.Name || !eq(a.Login, b.Login) || a.Email != b.Email || !eq(a.Phone, b.Phone) ||
		a.Readonly != b.Readonly || a.Administrator != b.Administrator || !eq(a.Map, b.Map) ||
		a.Latitude != b.Latitude || a.Longitude != b.Longitude || a.Zoom != b.Zoom ||
		!eq(a.CoordinateFormat, b.CoordinateFormat) || a.Disabled != b.Disabled ||
		!a.ExpirationTime.Time.Equal(b.ExpirationTime.Time) || a.DeviceLimit != b.DeviceLimit ||
		a.UserLimit != b.UserLimit || a.DeviceReadonly != b.DeviceReadonly || a.LimitCommands != b.LimitCommands ||
		a.DisableReports != b.DisableReports || a.FixedEmail != b.FixedEmail || !eq(a.PoiLayer, b.PoiLayer) ||
		!eq(a.TotpKey, b.TotpKey) || a.Temporary != b.Temporary {
		return false
	}
	strip := func(attrs *model.Attributes) map[string]string {
		m := map[string]string{}
		for k, v := range attrs.All() {
			if !slices.Contains(excluded, k) {
				var one model.Attributes
				one.Set("v", v)
				m[k] = one.String()
			}
		}
		return m
	}
	x, y := strip(&a.Attributes), strip(&b.Attributes)
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}

// deleteUser is UserResource.remove; deleting yourself also logs the session out.
func (a *API) deleteUser(w http.ResponseWriter, r *request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := a.checkPermission(r, store.KindUser, id); err != nil {
		return err
	}
	if err := a.checkEditKind(r, store.KindUser, false, false); err != nil {
		return err
	}
	if err := store.Delete[model.User](r.Context(), a.st, id); err != nil {
		return err
	}
	a.audit(r.Context(), r.Request, "remove", r.user.ID, "user", id)
	if r.user.ID == id {
		sess := a.sessions.getOrCreate(w, r.session)
		sess.userID = 0
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// postTotp returns a new random TOTP key when the server enables TOTP (totpEnable).
func (a *API) postTotp(w http.ResponseWriter, r *request) error {
	srv, err := a.server(r)
	if err != nil {
		return err
	}
	if !serverFlag(srv, "totpEnable") {
		return errors.New("One-time password is disabled")
	}
	key := make([]byte, 20) // googleauth default: 160-bit secret, base32
	if _, err := rand.Read(key); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write([]byte(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key)))
	return err
}

// putServer is ServerResource.update (administrators). The response echoes the request,
// with the version as GET reports it.
func (a *API) putServer(w http.ResponseWriter, r *request) error {
	if err := checkAdmin(r); err != nil {
		return err
	}
	var s model.ServerInfo
	if err := readJSON(r, serverBean(&s)); err != nil {
		return err
	}
	if err := store.Update(r.Context(), a.st, &s.Server); err != nil {
		return err
	}
	a.audit(r.Context(), r.Request, "edit", r.user.ID, "server", s.ID)
	s.Version = Version
	return writeJSON(w, http.StatusOK, s)
}

// link is one permission entry: the first key is the owner, the second the property.
type link struct {
	owner, property     store.Kind
	ownerID, propertyID int64
}

// parseLink reads {"userId": 1, "deviceId": 2} as Java's Permission constructor does.
func parseLink(raw json.RawMessage) (link, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return link{}, errors.New("permission must be an object")
	}
	var keys []string
	var ids []int64
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return link{}, err
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return link{}, err
		}
		if isNull(value) {
			return link{}, errors.New("null permission id") // unboxing NullPointerException
		}
		id, err := coerceInt(value, -1<<63, 1<<63-1)
		if err != nil {
			return link{}, err
		}
		keys = append(keys, tok.(string))
		ids = append(ids, id)
	}
	if len(keys) != 2 {
		return link{}, errors.New("Invalid permission")
	}
	owner, ok1 := kindByKey[strings.ToLower(keys[0])]
	property, ok2 := kindByKey[strings.ToLower(keys[1])]
	if !ok1 || !ok2 {
		return link{}, fmt.Errorf("unknown permission keys %v", keys)
	}
	return link{owner, property, ids[0], ids[1]}, nil
}

func (a *API) linkOne(add bool) handlerFunc {
	return func(w http.ResponseWriter, r *request) error {
		body, err := readBody(r)
		if err != nil {
			return err
		}
		return a.applyLinks(w, r, []json.RawMessage{body}, add)
	}
}

func (a *API) linkBulk(add bool) handlerFunc {
	return func(w http.ResponseWriter, r *request) error {
		body, err := readBody(r)
		if err != nil {
			return err
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(body, &entries); err != nil {
			return err
		}
		return a.applyLinks(w, r, entries, add)
	}
}

func readBody(r *request) ([]byte, error) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return nil, errStatus(http.StatusUnsupportedMediaType, "Unsupported Media Type")
	}
	return io.ReadAll(r.Body)
}

// applyLinks is PermissionsResource.add/remove: every entry must have the same pair of
// keys; entries are applied one by one (no transaction, as in Java), each checked,
// stored and audited.
func (a *API) applyLinks(w http.ResponseWriter, r *request, entries []json.RawMessage, add bool) error {
	if err := a.checkRestriction(r, func(x restrictions) bool { return x.readonly }); err != nil {
		return err
	}
	var keys []string
	for _, e := range entries {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(e, &m); err != nil {
			return err
		}
		k := make([]string, 0, len(m))
		for key := range m {
			k = append(k, key)
		}
		slices.Sort(k)
		if keys != nil && !slices.Equal(keys, k) {
			return errStatus(http.StatusBadRequest, "permission types differ")
		}
		keys = k
	}
	for _, e := range entries {
		l, err := parseLink(e)
		if err != nil {
			return err
		}
		if !r.user.Administrator {
			if err := a.checkPermission(r, l.owner, l.ownerID); err != nil {
				return err
			}
			if err := a.checkPermission(r, l.property, l.propertyID); err != nil {
				return err
			}
		}
		action := "link"
		if add {
			err = a.st.AddLink(r.Context(), l.owner, l.ownerID, l.property, l.propertyID)
		} else {
			action = "unlink"
			err = a.st.RemoveLink(r.Context(), l.owner, l.ownerID, l.property, l.propertyID)
		}
		if err != nil {
			return err
		}
		a.auditLink(r, action, l.owner, l.ownerID, l.property, l.propertyID)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// putAccumulators is DeviceResource.updateAccumulators: the device's latest position is
// stored again as a new position with the new totals, and becomes the latest.
func (a *API) putAccumulators(w http.ResponseWriter, r *request) error {
	var deviceID int64
	var total *float64
	var hours *int64
	b := bean{props: props{
		"deviceId": longProp(&deviceID),
		"totalDistance": func(raw json.RawMessage) error {
			if isNull(raw) {
				total = nil
				return nil
			}
			var v float64
			total = &v
			return doubleProp(&v)(raw)
		},
		"hours": func(raw json.RawMessage) error {
			if isNull(raw) {
				hours = nil
				return nil
			}
			var v int64
			hours = &v
			return longProp(&v)(raw)
		},
	}}
	if err := readJSON(r, b); err != nil {
		return err
	}
	if err := a.checkPermission(r, store.KindDevice, deviceID); err != nil {
		return err
	}
	if err := a.checkEditKind(r, store.KindDevice, false, false); err != nil {
		return err
	}
	positions, err := a.st.LatestPositions(r.Context(), deviceID, 0)
	if err != nil {
		return err
	}
	if len(positions) == 0 {
		return errors.New("no position") // IllegalArgumentException in Java
	}
	p := positions[0]
	if total != nil {
		p.Attributes.Set("totalDistance", *total)
	}
	if hours != nil {
		p.Attributes.Set("hours", *hours)
	}
	p.ServerTime = model.NewTime(time.Now())
	if err := store.Insert(r.Context(), a.st, &p); err != nil {
		return err
	}
	if err := a.st.UpdateDevicePosition(r.Context(), &model.Device{ID: p.DeviceID, PositionID: p.ID}); err != nil {
		return err
	}
	a.audit(r.Context(), r.Request, "accumulators", r.user.ID, "device", deviceID)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// imageSizeLimit is DeviceResource.IMAGE_SIZE_LIMIT.
const imageSizeLimit = 500000

// postDeviceImage stores <media.path>/<uniqueId>/device.<ext> and returns the file name.
func (a *API) postDeviceImage(w http.ResponseWriter, r *request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ct := r.Header.Get("Content-Type")
	if mt, _, _ := mime.ParseMediaType(ct); !strings.HasPrefix(mt, "image/") {
		return errStatus(http.StatusUnsupportedMediaType, "Unsupported Media Type")
	}
	if err := a.checkEditKind(r, store.KindDevice, false, false); err != nil {
		return err
	}
	devices, err := store.List[model.Device](r.Context(), a.st, store.ListOptions{Where: []store.Cond{
		store.Equals("id", id), store.Permission(store.KindUser, r.user.ID, store.KindDevice)}})
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return errNoEntity
	}
	ext, ok := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp"}[ct]
	if !ok {
		return errors.New("Unsupported image type")
	}
	name := "device." + ext
	path, err := a.mediaFile(devices[0].UniqueID, name)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r.Body, imageSizeLimit+1))
	if err != nil {
		return err
	}
	if n > imageSizeLimit {
		return errors.New("Image size limit exceeded")
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write([]byte(name))
	return err
}

// mediaFile is MediaManager.createFile: <media.path>/<uniqueId>/<name>, kept inside
// media.path, with directories created.
func (a *API) mediaFile(uniqueID, name string) (string, error) {
	root, err := filepath.Abs(a.opts.MediaPath)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, uniqueID, name)
	if !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", errors.New("Invalid media path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// deletePosition is PositionResource.removeById.
func (a *API) deletePosition(w http.ResponseWriter, r *request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if err := a.checkRestriction(r, func(x restrictions) bool { return x.readonly }); err != nil {
		return err
	}
	p, err := store.Get[model.Position](r.Context(), a.st, id)
	if errors.Is(err, store.ErrNotFound) {
		return errNoEntity
	}
	if err != nil {
		return err
	}
	if err := a.checkPermission(r, store.KindDevice, p.DeviceID); err != nil {
		return err
	}
	if err := store.Delete[model.Position](r.Context(), a.st, id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// deletePositions is PositionResource.remove: a device's positions in a period.
func (a *API) deletePositions(w http.ResponseWriter, r *request) error {
	deviceID, err := qInt64(r, "deviceId")
	if err != nil {
		return err
	}
	from, err := qDate(r, "from")
	if err != nil {
		return err
	}
	to, err := qDate(r, "to")
	if err != nil {
		return err
	}
	if err := a.checkPermission(r, store.KindDevice, deviceID); err != nil {
		return err
	}
	if err := a.checkRestriction(r, func(x restrictions) bool { return x.readonly }); err != nil {
		return err
	}
	if from.IsZero() || to.IsZero() {
		return errors.New("from and to are required")
	}
	if err := a.st.DeletePositions(r.Context(), deviceID, from, to); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// sendCommand is CommandResource.send. Commands for devices without a live connection are
// queued (202 with the queued command). Live delivery, SMS and push senders come with
// device sessions and notificators (MIGRATION.md Steps 6 and 9).
func (a *API) sendCommand(w http.ResponseWriter, r *request) error {
	groupID, err := qInt64(r, "groupId")
	if err != nil {
		return err
	}
	var c model.Command
	if err := readJSON(r, commandBean(&c)); err != nil {
		return err
	}
	if c.ID > 0 {
		if err := a.checkPermission(r, store.KindCommand, c.ID); err != nil {
			return err
		}
		saved, err := store.Get[model.Command](r.Context(), a.st, c.ID)
		if err != nil {
			return err
		}
		saved.DeviceID = c.DeviceID
		c = saved
	} else if err := a.checkRestriction(r, func(x restrictions) bool { return x.limitCommands }); err != nil {
		return err
	}
	if groupID > 0 {
		if err := a.checkPermission(r, store.KindGroup, groupID); err != nil {
			return err
		}
		devices, err := a.devicesInGroup(r, groupID)
		if err != nil {
			return err
		}
		var queued []model.QueuedCommand
		for _, id := range devices {
			one := c
			one.DeviceID = id
			q, err := a.deliver(r, one)
			if err != nil {
				return err
			}
			if q != nil {
				queued = append(queued, *q)
			}
		}
		if len(queued) > 0 {
			return writeListStatus(w, http.StatusAccepted, queued)
		}
	} else {
		if err := a.checkPermission(r, store.KindDevice, c.DeviceID); err != nil {
			return err
		}
		q, err := a.deliver(r, c)
		if err != nil {
			return err
		}
		if q != nil {
			return writeJSON(w, http.StatusAccepted, *q)
		}
	}
	a.auditCommand(r, groupID, c.DeviceID)
	return writeJSON(w, http.StatusOK, c)
}

func (a *API) auditCommand(r *request, groupID, deviceID int64) {
	if groupID > 0 {
		a.audit(r.Context(), r.Request, "command", r.user.ID, "group", groupID)
	} else {
		a.audit(r.Context(), r.Request, "command", r.user.ID, "device", deviceID)
	}
}

// deliver is CommandsManager.sendCommand without live connections: the text channel needs
// SMS, push senders aren't ported yet, and everything else is queued unless "noQueue".
func (a *API) deliver(r *request, c model.Command) (*model.QueuedCommand, error) {
	device, err := store.Get[model.Device](r.Context(), a.st, c.DeviceID)
	if err != nil {
		return nil, err
	}
	if c.TextChannel {
		if !a.opts.TextEnabled {
			return nil, errors.New("SMS not configured")
		}
		return nil, errStatus(http.StatusNotImplemented, "SMS commands are not ported yet")
	}
	if _, ok := a.commandSender(&device); ok {
		return nil, errStatus(http.StatusNotImplemented, "push command senders are not ported yet")
	}
	if v, ok := c.Attributes.Get("noQueue"); ok && (v == true || v == "true") {
		return nil, errors.New("Failed to send command")
	}
	q := model.QueuedCommand{DeviceID: c.DeviceID, Type: c.Type, TextChannel: c.TextChannel}
	for k, v := range c.Attributes.All() {
		q.Attributes.Set(k, v)
	}
	if err := store.Insert(r.Context(), a.st, &q); err != nil {
		return nil, err
	}
	return &q, nil
}

// devicesInGroup is DeviceUtil.getAccessibleDevices for one group: the user's devices in
// the group and its subgroups. Java collects them in a HashSet of identity-hashed objects,
// so its order changes between runs; here they are in id order.
func (a *API) devicesInGroup(r *request, groupID int64) ([]int64, error) {
	devices, err := store.List[model.Device](r.Context(), a.st, store.ListOptions{
		Where: []store.Cond{store.Permission(store.KindUser, r.user.ID, store.KindDevice)}, OrderBy: "id"})
	if err != nil {
		return nil, err
	}
	groups, err := store.List[model.Group](r.Context(), a.st, store.ListOptions{
		Where: []store.Cond{store.Permission(store.KindUser, r.user.ID, store.KindGroup)}})
	if err != nil {
		return nil, err
	}
	want := map[int64]bool{}
	queue := []int64{groupID}
	for len(queue) > 0 {
		g := queue[0]
		queue = queue[1:]
		if want[g] {
			continue
		}
		want[g] = true
		for _, child := range groups {
			if child.GroupID == g {
				queue = append(queue, child.ID)
			}
		}
	}
	var ids []int64
	for _, d := range devices {
		if d.GroupID > 0 && want[d.GroupID] {
			ids = append(ids, d.ID)
		}
	}
	return ids, nil
}

// share is ShareResource: a temporary read-only user for one device or group, and a token
// for it.
func (a *API) share(kind store.Kind) handlerFunc {
	return func(w http.ResponseWriter, r *request) error {
		f, err := form(r)
		if err != nil {
			return err
		}
		var id int64
		if v := f.get(kind.Key()); v != nil && *v != "" {
			if id, err = parseInt64(*v); err != nil {
				return errStatus(http.StatusBadRequest, "%s: %v", kind.Key(), err)
			}
		}
		var expiration time.Time
		if v := f.get("expiration"); v != nil {
			if expiration, err = parseZonedDate(*v); err != nil {
				return errStatus(http.StatusBadRequest, "expiration: %v", err)
			}
		}
		srv, err := a.server(r)
		if err != nil {
			return err
		}
		if serverFlag(srv, "disableShare") {
			return errors.New("Sharing is disabled")
		}
		u := r.user
		if u.Temporary {
			return errors.New("Temporary user")
		}
		if !u.ExpirationTime.IsZero() {
			if expiration.IsZero() {
				return errors.New("expiration: null value")
			}
			if u.ExpirationTime.Time.Before(expiration) {
				expiration = u.ExpirationTime.Time
			}
		}
		var name, email string
		if kind == store.KindDevice {
			d, err := store.List[model.Device](r.Context(), a.st, store.ListOptions{Where: []store.Cond{
				store.Equals("id", id), store.Permission(store.KindUser, u.ID, store.KindDevice)}})
			if err != nil || len(d) == 0 {
				return errors.New("device not found")
			}
			name, email = d[0].Name, u.Email+":"+d[0].UniqueID
		} else {
			g, err := store.List[model.Group](r.Context(), a.st, store.ListOptions{Where: []store.Cond{
				store.Equals("id", id), store.Permission(store.KindUser, u.ID, store.KindGroup)}})
			if err != nil || len(g) == 0 {
				return errors.New("group not found")
			}
			name, email = g[0].Name, fmt.Sprintf("%s:group:%d", u.Email, g[0].ID)
		}
		existing, err := store.List[model.User](r.Context(), a.st, store.ListOptions{Where: []store.Cond{store.Equals("email", email)}})
		if err != nil {
			return err
		}
		var shareID int64
		if len(existing) > 0 {
			shareID = existing[0].ID
		} else {
			s := model.User{
				Name: name, Email: email, ExpirationTime: model.NewTime(expiration), Temporary: true, Readonly: true,
				LimitCommands:  u.LimitCommands || !a.opts.ShareCommands,
				DisableReports: u.DisableReports || !a.opts.ShareReports,
			}
			if err := store.Insert(r.Context(), a.st, &s); err != nil {
				return err
			}
			if err := a.st.AddLink(r.Context(), store.KindUser, s.ID, kind, id); err != nil {
				return err
			}
			shareID = s.ID
		}
		token, _, err := a.tokens.Issue(r.Context(), shareID, expiration)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write([]byte(token))
		return err
	}
}

// updatePassword is PasswordResource.update: set a new password with a reset token.
func (a *API) updatePassword(w http.ResponseWriter, r *request) error {
	f, err := form(r)
	if err != nil {
		return err
	}
	token := f.get("token")
	if token == nil {
		return errors.New("token: null value")
	}
	data, err := a.tokens.Verify(r.Context(), *token)
	if err != nil {
		return err // every token failure is a 400 here
	}
	u, err := store.Get[model.User](r.Context(), a.st, data.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return errNoEntity
	}
	if err != nil {
		return err
	}
	if p := f.get("password"); p != nil && *p != "" {
		hash, salt, err := auth.HashPassword(*p)
		if err != nil {
			return err
		}
		u.HashedPassword, u.Salt = &hash, &salt
	}
	if err := a.st.UpdateUserPassword(r.Context(), &u); err != nil {
		return err
	}
	w.WriteHeader(http.StatusOK)
	return nil
}
