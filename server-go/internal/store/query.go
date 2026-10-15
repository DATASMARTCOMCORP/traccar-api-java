package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Kind names a model class in Java's permission scheme. Link tables are
// tc_<owner>_<property> with columns <kind>Id; ManagedUser and LinkedDevice are users and
// devices seen from the property side of tc_user_user and tc_device_device.
type Kind string

const (
	KindUser         Kind = "user"
	KindManagedUser  Kind = "managedUser"
	KindDevice       Kind = "device"
	KindLinkedDevice Kind = "linkedDevice"
	KindGroup        Kind = "group"
	KindGeofence     Kind = "geofence"
	KindNotification Kind = "notification"
	KindDriver       Kind = "driver"
	KindAttribute    Kind = "attribute"
	KindCalendar     Kind = "calendar"
	KindCommand      Kind = "command"
	KindMaintenance  Kind = "maintenance"
	KindOrder        Kind = "order"
	KindReport       Kind = "report"
)

// Key is the link-table column for the kind (Permission.getKey: "deviceId").
func (k Kind) Key() string { return string(k) + "Id" }

// linkName is the kind's name in a link table name: Managed/Linked prefixes are dropped,
// but only on the property side (Permission.getStorageName).
func (k Kind) linkName() string {
	switch k {
	case KindManagedUser:
		return "user"
	case KindLinkedDevice:
		return "device"
	}
	return string(k)
}

// grouped reports whether the kind is a GroupedModel (has a groupId).
func (k Kind) grouped() bool { return k == KindDevice || k == KindLinkedDevice || k == KindGroup }

func (k Kind) isDevice() bool { return k == KindDevice || k == KindLinkedDevice }

func linkTable(owner, property Kind) string {
	return "tc_" + string(owner) + "_" + property.linkName()
}

// Cond is a WHERE clause fragment with its arguments, written with ? placeholders that
// the query numbers ($1, $2...) when it is assembled.
type Cond struct {
	sql  string
	args []any
}

// Equals is "column = value". Integers are bound as bigint, as Java binds ids (long): an
// id beyond the column's int range simply matches nothing.
func Equals(column string, value any) Cond {
	if _, ok := value.(int64); ok {
		return Cond{column + " = ?::bigint", []any{value}}
	}
	return Cond{column + " = ?", []any{value}}
}

// Contains is Java's keyword search: LOWER(column) LIKE '%keyword%' on any of the
// columns. Like Java, % and _ in the keyword are not escaped.
func Contains(keyword string, columns ...string) Cond {
	pattern := "%" + strings.ToLower(keyword) + "%"
	parts := make([]string, len(columns))
	args := make([]any, len(columns))
	for i, c := range columns {
		parts[i] = "LOWER(" + c + ") LIKE ?"
		args[i] = pattern
	}
	return Cond{"(" + strings.Join(parts, " OR ") + ")", args}
}

// Permission restricts ids to objects linked to an owner (Condition.Permission): the
// owner's own links, plus, when either side has groups, links reached through the group
// tree. Use the result on the property's table ("id IN (...)").
func Permission(owner Kind, ownerID int64, property Kind) Cond {
	return permission(owner, ownerID, property, 0, false)
}

// PermissionNoGroups is Permission(...).excludeGroups(): direct links only.
func PermissionNoGroups(owner Kind, ownerID int64, property Kind) Cond {
	return permission(owner, ownerID, property, 0, true)
}

// PermissionOf is the reverse direction: owners linked to a property id, on the owner's
// table (Condition.Permission(ownerClass, propertyClass, propertyId)).
func PermissionOf(owner, property Kind, propertyID int64, excludeGroups bool) Cond {
	return permission(owner, 0, property, propertyID, excludeGroups)
}

// permission is DatabaseStorage.formatPermissionQuery, statement for statement. Group
// nesting is expanded to three levels (a group, its parent and grandparent), exactly as
// Java does; deeper ancestors are not followed by Java either.
func permission(owner Kind, ownerID int64, property Kind, propertyID int64, excludeGroups bool) Cond {
	var outputKey, conditionKey string
	id := propertyID
	if ownerID > 0 {
		outputKey, conditionKey, id = property.Key(), owner.Key(), ownerID
	} else {
		outputKey, conditionKey = owner.Key(), property.Key()
	}
	storage := linkTable(owner, property)
	var b strings.Builder
	b.WriteString("id IN (SELECT " + storage + "." + outputKey + " FROM " + storage + " WHERE " + conditionKey + " = ?::bigint")
	args := []any{id}
	if (owner.grouped() || property.grouped()) && !excludeGroups {
		var expandDevices bool
		var groupStorage string
		if owner.grouped() {
			expandDevices = owner.isDevice()
			groupStorage = linkTable(KindGroup, property)
		} else {
			expandDevices = property.isDevice()
			groupStorage = linkTable(owner, KindGroup)
		}
		b.WriteString(" UNION SELECT DISTINCT ")
		if !expandDevices {
			if outputKey == "groupId" {
				b.WriteString("all_groups.")
			} else {
				b.WriteString(groupStorage + ".")
			}
		}
		b.WriteString(outputKey + " FROM " + groupStorage + " INNER JOIN (" +
			"SELECT id as parentId, id as groupId FROM tc_groups" +
			" UNION SELECT groupId as parentId, id as groupId FROM tc_groups WHERE groupId IS NOT NULL" +
			" UNION SELECT g2.groupId as parentId, g1.id as groupId FROM tc_groups AS g2" +
			" INNER JOIN tc_groups AS g1 ON g2.id = g1.groupId WHERE g2.groupId IS NOT NULL" +
			") AS all_groups ON " + groupStorage + ".groupId = all_groups.parentId")
		if expandDevices {
			b.WriteString(" INNER JOIN (SELECT groupId as parentId, id as deviceId FROM tc_devices" +
				" WHERE groupId IS NOT NULL) AS devices ON all_groups.groupId = devices.parentId")
		}
		b.WriteString(" WHERE " + conditionKey + " = ?::bigint")
		args = append(args, id)
	}
	b.WriteString(")")
	return Cond{b.String(), args}
}

// ListOptions shape a list query: conditions joined with AND, ORDER BY with optional
// LIMIT/OFFSET (only when Limit > 0, as in Java), and Java's excludeAttributes.
type ListOptions struct {
	Where             []Cond
	OrderBy           string
	Desc              bool
	Limit, Offset     int
	ExcludeAttributes bool
}

// List runs a filtered, ordered query on T's table.
func List[T Entity](ctx context.Context, s *Store, o ListOptions) ([]T, error) {
	t := tableOf[T]()
	sql, args := buildQuery(selectList(t, o.ExcludeAttributes), o)
	return query(ctx, s, t, sql, args...)
}

// Exists reports whether a row of T's table matches every condition.
func Exists[T Entity](ctx context.Context, s *Store, where ...Cond) (bool, error) {
	t := tableOf[T]()
	sql, args := buildQuery("SELECT EXISTS (SELECT 1 FROM "+t.name, ListOptions{Where: where})
	var ok bool
	if err := s.pool.QueryRow(ctx, sql+")", args...).Scan(&ok); err != nil {
		return false, fmt.Errorf("select %s: %w", t.name, err)
	}
	return ok, nil
}

// IDs returns the ids of T's rows matching the conditions.
func IDs[T Entity](ctx context.Context, s *Store, where ...Cond) ([]int64, error) {
	t := tableOf[T]()
	sql, args := buildQuery("SELECT id FROM "+t.name, ListOptions{Where: where})
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", t.name, err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func selectList[T any](t *table[T], excludeAttributes bool) string {
	sql := t.selectSQL()
	if excludeAttributes {
		// Columns.Exclude("attributes"): the column isn't read, so the default (empty) map
		// is returned.
		sql = strings.Replace(sql, "SELECT id, attributes,", "SELECT id, NULL AS attributes,", 1)
	}
	return sql
}

// buildQuery appends WHERE/ORDER BY/LIMIT to a SELECT and numbers the placeholders.
func buildQuery(base string, o ListOptions) (string, []any) {
	var b strings.Builder
	b.WriteString(base)
	var args []any
	for i, c := range o.Where {
		if i == 0 {
			b.WriteString(" WHERE ")
		} else {
			b.WriteString(" AND ")
		}
		b.WriteString(c.sql)
		args = append(args, c.args...)
	}
	if o.OrderBy != "" {
		b.WriteString(" ORDER BY " + o.OrderBy)
		if o.Desc {
			b.WriteString(" DESC")
		}
		if o.Limit > 0 {
			b.WriteString(" LIMIT " + strconv.Itoa(o.Limit))
			if o.Offset > 0 {
				b.WriteString(" OFFSET " + strconv.Itoa(o.Offset))
			}
		}
	}
	return number(b.String()), args
}

// number replaces ? placeholders with $1, $2, ... in order.
func number(sql string) string {
	var b strings.Builder
	n := 0
	for _, r := range sql {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// LatestPositions is Condition.LatestPositions: the positions devices point to. With a
// device id, only that device's; otherwise all, limited to fixes newer than period
// (database.positionPeriod) when period > 0. Order is whatever the database returns,
// as in Java.
func (s *Store) LatestPositions(ctx context.Context, deviceID int64, period time.Duration) ([]model.Position, error) {
	var where []Cond
	if deviceID > 0 {
		where = []Cond{
			Equals("deviceId", deviceID),
			{"id IN (SELECT positionId FROM tc_devices WHERE id = ?::bigint)", []any{deviceID}},
		}
	} else {
		if period > 0 {
			since := model.NewTime(time.Now().Add(-period))
			where = append(where, Cond{"fixTime > ?", []any{s.ts(&since)}})
		}
		where = append(where, Cond{"id IN (SELECT positionId FROM tc_devices)", nil})
	}
	sql, args := buildQuery(positionTable.selectSQL(), ListOptions{Where: where})
	return query(ctx, s, positionTable, sql, args...)
}

// Positions returns a device's positions with fixTime between from and to, oldest first,
// at most limit (0: no limit) (PositionUtil.getPositionsStream).
func (s *Store) Positions(ctx context.Context, deviceID int64, from, to model.Time, limit int) ([]model.Position, error) {
	where := []Cond{Equals("deviceId", deviceID), {"fixTime BETWEEN ? AND ?", []any{s.ts(&from), s.ts(&to)}}}
	sql, args := buildQuery(positionTable.selectSQL(), ListOptions{Where: where, OrderBy: "fixTime", Limit: limit})
	return query(ctx, s, positionTable, sql, args...)
}

// Between is "column BETWEEN from AND to" for timestamps.
func (s *Store) Between(column string, from, to model.Time) Cond {
	return Cond{column + " BETWEEN ? AND ?", []any{s.ts(&from), s.ts(&to)}}
}

// LinkRows returns rows of a link table for GET /api/permissions: all links of an owner
// and/or a property (0 means any), keyed by the two column names. Java takes the keys
// from the result set's column labels, which Postgres folds to lower case ("deviceid").
func (s *Store) LinkRows(ctx context.Context, owner Kind, ownerID int64, property Kind, propertyID int64) ([]model.Permission, error) {
	var where []Cond
	if ownerID > 0 {
		where = append(where, Equals(owner.Key(), ownerID))
	}
	if propertyID > 0 {
		where = append(where, Equals(property.Key(), propertyID))
	}
	sql, args := buildQuery("SELECT "+owner.Key()+", "+property.Key()+" FROM "+linkTable(owner, property), ListOptions{Where: where})
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("select %s: %w", linkTable(owner, property), err)
	}
	defer rows.Close()
	var out []model.Permission
	for rows.Next() {
		p := model.Permission{OwnerKey: strings.ToLower(owner.Key()), PropertyKey: strings.ToLower(property.Key())}
		if err := rows.Scan(&p.OwnerID, &p.PropertyID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
