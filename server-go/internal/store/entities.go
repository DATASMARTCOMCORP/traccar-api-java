package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// table describes how one entity maps to its table. columns are the columns an ordinary
// insert or update writes, the same set Java writes (getters without @QueryIgnore,
// minus id). extra are columns that are only read here and written by targeted updates
// (device state, password hash).
type table[T any] struct {
	name    string
	columns []string
	extra   []string
	// values returns the values of columns, in order.
	values func(s *Store, v *T) ([]any, error)
	// targets returns scan targets for id, columns, then extra.
	targets func(s *Store, v *T) []any
	id      func(v *T) *int64
}

func (t *table[T]) selectSQL() string {
	all := append(append([]string{"id"}, t.columns...), t.extra...)
	return "SELECT " + strings.Join(all, ", ") + " FROM " + t.name
}

// Entity lists the types the store can read and write with All, Get, Insert and Update.
type Entity interface {
	model.Server | model.User | model.Group | model.Device | model.Position | model.Event |
		model.Geofence | model.Driver | model.ComputedAttribute | model.Calendar | model.Command |
		model.QueuedCommand | model.Notification | model.Maintenance | model.Order | model.Report |
		model.Statistics | model.Action
}

func tableOf[T Entity]() *table[T] {
	var t any
	switch any((*T)(nil)).(type) {
	case *model.Server:
		t = serverTable
	case *model.User:
		t = userTable
	case *model.Group:
		t = groupTable
	case *model.Device:
		t = deviceTable
	case *model.Position:
		t = positionTable
	case *model.Event:
		t = eventTable
	case *model.Geofence:
		t = geofenceTable
	case *model.Driver:
		t = driverTable
	case *model.ComputedAttribute:
		t = computedAttributeTable
	case *model.Calendar:
		t = calendarTable
	case *model.Command:
		t = commandTable
	case *model.QueuedCommand:
		t = queuedCommandTable
	case *model.Notification:
		t = notificationTable
	case *model.Maintenance:
		t = maintenanceTable
	case *model.Order:
		t = orderTable
	case *model.Report:
		t = reportTable
	case *model.Statistics:
		t = statisticsTable
	case *model.Action:
		t = actionTable
	}
	return t.(*table[T])
}

// All returns every row of T's table, ordered by id.
func All[T Entity](ctx context.Context, s *Store) ([]T, error) {
	return query(ctx, s, tableOf[T](), " ORDER BY id")
}

// Get returns the row with the given id, or ErrNotFound. Ids are Java longs while most id
// columns are integer, so the parameter is bigint: an out-of-range id is simply not found.
func Get[T Entity](ctx context.Context, s *Store, id int64) (T, error) {
	rows, err := query(ctx, s, tableOf[T](), " WHERE id = $1::bigint", id)
	if err != nil {
		var zero T
		return zero, err
	}
	if len(rows) == 0 {
		var zero T
		return zero, fmt.Errorf("%s %d: %w", tableOf[T]().name, id, ErrNotFound)
	}
	return rows[0], nil
}

func query[T any](ctx context.Context, s *Store, t *table[T], where string, args ...any) ([]T, error) {
	rows, err := s.pool.Query(ctx, t.selectSQL()+where, args...)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", t.name, err)
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var v T
		if err := rows.Scan(t.targets(s, &v)...); err != nil {
			return nil, fmt.Errorf("scan %s: %w", t.name, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select %s: %w", t.name, err)
	}
	return out, nil
}

// Insert writes a new row with a database-generated id and stores the id in v.
func Insert[T Entity](ctx context.Context, s *Store, v *T) error {
	t := tableOf[T]()
	values, err := t.values(s, v)
	if err != nil {
		return fmt.Errorf("insert %s: %w", t.name, err)
	}
	placeholders := make([]string, len(values))
	for i := range values {
		placeholders[i] = "$" + itoa(i+1)
	}
	sql := "INSERT INTO " + t.name + " (" + strings.Join(t.columns, ", ") + ") VALUES (" +
		strings.Join(placeholders, ", ") + ") RETURNING id"
	if err := s.pool.QueryRow(ctx, sql, values...).Scan(t.id(v)); err != nil {
		return fmt.Errorf("insert %s: %w", t.name, err)
	}
	return nil
}

// Update rewrites every ordinary column of the row with v's id.
func Update[T Entity](ctx context.Context, s *Store, v *T) error {
	t := tableOf[T]()
	values, err := t.values(s, v)
	if err != nil {
		return fmt.Errorf("update %s: %w", t.name, err)
	}
	return s.update(ctx, t.name, *t.id(v), t.columns, values)
}

func (s *Store) update(ctx context.Context, tableName string, id int64, columns []string, values []any) error {
	sets := make([]string, len(columns))
	for i, c := range columns {
		sets[i] = c + " = $" + itoa(i+1)
	}
	sql := "UPDATE " + tableName + " SET " + strings.Join(sets, ", ") + " WHERE id = $" + itoa(len(columns)+1) + "::bigint"
	tag, err := s.pool.Exec(ctx, sql, append(values, id)...)
	if err != nil {
		return fmt.Errorf("update %s: %w", tableName, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update %s %d: %w", tableName, id, ErrNotFound)
	}
	return nil
}

func attrsValue(a *model.Attributes) (string, error) {
	b, err := a.MarshalJSON()
	return string(b), err
}

// Targeted updates, each writing the same columns as the Java call it mirrors.

// UpdateDeviceStatus writes status and lastUpdate (ConnectionManager.updateDevice).
// A nil status is written as "offline": Java's getter never returns null.
func (s *Store) UpdateDeviceStatus(ctx context.Context, d *model.Device) error {
	status := model.StatusOffline
	if d.Status != nil {
		status = *d.Status
	}
	return s.update(ctx, "tc_devices", d.ID, []string{"status", "lastupdate"},
		[]any{status, s.ts(&d.LastUpdate)})
}

// UpdateDevicePosition writes the latest position id (PostProcessHandler).
func (s *Store) UpdateDevicePosition(ctx context.Context, d *model.Device) error {
	return s.update(ctx, "tc_devices", d.ID, []string{"positionid"}, []any{id(d.PositionID)})
}

// UpdateDeviceMotion writes the motion state of the current trip logic
// (MotionEventHandler, including the device attributes).
func (s *Store) UpdateDeviceMotion(ctx context.Context, d *model.Device) error {
	a, err := attrsValue(&d.Attributes)
	if err != nil {
		return err
	}
	return s.update(ctx, "tc_devices", d.ID,
		[]string{"motionstreak", "motiontime", "motionlatitude", "motionlongitude", "attributes"},
		[]any{d.MotionStreak, s.ts(&d.MotionTime), d.MotionLatitude, d.MotionLongitude, a})
}

// UpdateDeviceMotionLegacy writes the motion state of the old trip logic
// (MotionEventHandler.handleOldLogic).
func (s *Store) UpdateDeviceMotionLegacy(ctx context.Context, d *model.Device) error {
	return s.update(ctx, "tc_devices", d.ID,
		[]string{"motionstreak", "motionstate", "motionpositionid", "motiontime", "motiondistance"},
		[]any{d.MotionStreak, d.MotionState, id(d.MotionPositionID), s.ts(&d.MotionTime), d.MotionDistance})
}

// UpdateDeviceOverspeed writes the overspeed state (OverspeedEventHandler).
func (s *Store) UpdateDeviceOverspeed(ctx context.Context, d *model.Device) error {
	return s.update(ctx, "tc_devices", d.ID,
		[]string{"overspeedstate", "overspeedtime", "overspeedgeofenceid"},
		[]any{d.OverspeedState, s.ts(&d.OverspeedTime), id(d.OverspeedGeofenceID)})
}

// ResetDeviceStatuses sets every device's status to "offline", as Java does at startup
// (DeviceUtil.resetStatus).
func (s *Store) ResetDeviceStatuses(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "UPDATE tc_devices SET status = $1", model.StatusOffline)
	if err != nil {
		return fmt.Errorf("reset device statuses: %w", err)
	}
	return nil
}

// UpdateUserPassword writes the PBKDF2 hash and salt (UserResource, PasswordResource).
func (s *Store) UpdateUserPassword(ctx context.Context, u *model.User) error {
	return s.update(ctx, "tc_users", u.ID, []string{"hashedpassword", "salt"}, []any{u.HashedPassword, u.Salt})
}
