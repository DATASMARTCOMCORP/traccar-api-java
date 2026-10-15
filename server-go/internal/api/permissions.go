package api

import (
	"errors"
	"fmt"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/store"
)

// Permission checks of Java's PermissionsService. A denial is a SecurityException there,
// which the API answers with 400, so these return plain errors.

func checkAdmin(r *request) error {
	if !r.user.Administrator {
		return errors.New("Administrator access required")
	}
	return nil
}

func checkManager(r *request) error {
	if !r.user.Administrator && r.user.UserLimit == 0 {
		return errors.New("Manager access required")
	}
	return nil
}

// checkPermission requires that the user may see the object: administrators see all,
// users see themselves, others need a link (directly or through groups).
func (a *API) checkPermission(r *request, kind store.Kind, id int64) error {
	if kind == store.KindLinkedDevice {
		kind = store.KindDevice
	}
	if r.user.Administrator || kind == store.KindUser && r.user.ID == id {
		return nil
	}
	property := kind
	if kind == store.KindUser {
		property = store.KindManagedUser
	}
	cond := []store.Cond{store.Equals("id", id), store.Permission(store.KindUser, r.user.ID, property)}
	var ok bool
	var err error
	switch kind {
	case store.KindUser:
		ok, err = store.Exists[model.User](r.Context(), a.st, cond...)
	case store.KindDevice:
		ok, err = store.Exists[model.Device](r.Context(), a.st, cond...)
	case store.KindGroup:
		ok, err = store.Exists[model.Group](r.Context(), a.st, cond...)
	case store.KindGeofence:
		ok, err = store.Exists[model.Geofence](r.Context(), a.st, cond...)
	case store.KindNotification:
		ok, err = store.Exists[model.Notification](r.Context(), a.st, cond...)
	case store.KindDriver:
		ok, err = store.Exists[model.Driver](r.Context(), a.st, cond...)
	case store.KindAttribute:
		ok, err = store.Exists[model.ComputedAttribute](r.Context(), a.st, cond...)
	case store.KindCalendar:
		ok, err = store.Exists[model.Calendar](r.Context(), a.st, cond...)
	case store.KindCommand:
		ok, err = store.Exists[model.Command](r.Context(), a.st, cond...)
	case store.KindMaintenance:
		ok, err = store.Exists[model.Maintenance](r.Context(), a.st, cond...)
	case store.KindOrder:
		ok, err = store.Exists[model.Order](r.Context(), a.st, cond...)
	case store.KindReport:
		ok, err = store.Exists[model.Report](r.Context(), a.st, cond...)
	default:
		return fmt.Errorf("no permission check for %s", kind)
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s access denied", javaClassName(kind))
	}
	return nil
}

// restrictions are Java's UserRestrictions, which both the server and users carry.
type restrictions struct {
	readonly, deviceReadonly, limitCommands, disableReports bool
}

// checkRestriction denies non-administrators when the server or the user has the
// restriction (PermissionsService.checkRestriction).
func (a *API) checkRestriction(r *request, denied func(restrictions) bool) error {
	if r.user.Administrator {
		return nil
	}
	srv, err := a.server(r)
	if err != nil {
		return err
	}
	u := r.user
	if denied(restrictions{srv.Readonly, srv.DeviceReadonly, srv.LimitCommands, srv.DisableReports}) ||
		denied(restrictions{u.Readonly, u.DeviceReadonly, u.LimitCommands, u.DisableReports}) {
		return errors.New("Operation restricted")
	}
	return nil
}

func (a *API) server(r *request) (*model.Server, error) {
	servers, err := store.All[model.Server](r.Context(), a.st)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, errors.New("tc_servers is empty")
	}
	return &servers[0], nil
}

func javaClassName(k store.Kind) string {
	switch k {
	case store.KindAttribute:
		return "Attribute"
	case store.KindManagedUser:
		return "ManagedUser"
	case store.KindLinkedDevice:
		return "LinkedDevice"
	}
	s := string(k)
	return string(s[0]-'a'+'A') + s[1:]
}
