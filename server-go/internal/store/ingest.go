package store

import (
	"context"
	"fmt"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// Queries of device ingestion (MIGRATION.md Step 6).

// DeviceByUniqueID is DeviceLookupService's query: the device with this unique id, or
// ErrNotFound.
func (s *Store) DeviceByUniqueID(ctx context.Context, uniqueID string) (model.Device, error) {
	rows, err := query(ctx, s, deviceTable, deviceTable.selectSQL()+" WHERE uniqueId = $1", uniqueID)
	if err != nil {
		return model.Device{}, err
	}
	if len(rows) == 0 {
		return model.Device{}, fmt.Errorf("device %q: %w", uniqueID, ErrNotFound)
	}
	return rows[0], nil
}

// PositionOf returns position id of the device (CacheManager: deviceId and id must both
// match), or ErrNotFound.
func (s *Store) PositionOf(ctx context.Context, deviceID, positionID int64) (model.Position, error) {
	rows, err := query(ctx, s, positionTable,
		positionTable.selectSQL()+" WHERE deviceId = $1::bigint AND id = $2::bigint", deviceID, positionID)
	if err != nil {
		return model.Position{}, err
	}
	if len(rows) == 0 {
		return model.Position{}, fmt.Errorf("position %d: %w", positionID, ErrNotFound)
	}
	return rows[0], nil
}

// PositionsWithExtra is PositionUtil.getPositionsStreamWithExtra: the last position
// before from, then the positions from from to to (inclusive), oldest first.
func (s *Store) PositionsWithExtra(ctx context.Context, deviceID int64, from, to model.Time) ([]model.Position, error) {
	extra, err := query(ctx, s, positionTable, positionTable.selectSQL()+
		" WHERE deviceId = $1::bigint AND fixTime < $2 ORDER BY fixTime DESC LIMIT 1", deviceID, s.ts(&from))
	if err != nil {
		return nil, err
	}
	rows, err := s.Positions(ctx, deviceID, from, to, 0)
	if err != nil {
		return nil, err
	}
	return append(extra, rows...), nil
}

// deviceGroups is the device's group and all its ancestors (CacheManager follows group
// links without a depth limit; UNION stops at a cycle).
const deviceGroups = `WITH RECURSIVE ancestors(id) AS (
	SELECT groupId FROM tc_devices WHERE id = $1::bigint AND groupId IS NOT NULL
	UNION SELECT g.groupId FROM tc_groups g JOIN ancestors a ON g.id = a.id WHERE g.groupId IS NOT NULL)`

// deviceObjectsSQL is CacheManager.getDeviceObjects as SQL: objects linked to the device
// directly or to any of its groups, each once, by id. (Java returns them as a set whose
// order changes between runs.)
func deviceObjectsSQL(table string, kind Kind) string {
	k := kind.Key()
	return deviceGroups + " " + table + " WHERE id IN (" +
		"SELECT " + k + " FROM " + linkTable(KindDevice, kind) + " WHERE deviceId = $1::bigint" +
		" UNION SELECT " + k + " FROM " + linkTable(KindGroup, kind) + " WHERE groupId IN (SELECT id FROM ancestors)" +
		") ORDER BY id"
}

// DeviceGeofences returns the geofences that apply to the device.
func (s *Store) DeviceGeofences(ctx context.Context, deviceID int64) ([]model.Geofence, error) {
	return query(ctx, s, geofenceTable, deviceObjectsSQL(geofenceTable.selectSQL(), KindGeofence), deviceID)
}

// DeviceMaintenances returns the maintenance entries that apply to the device.
func (s *Store) DeviceMaintenances(ctx context.Context, deviceID int64) ([]model.Maintenance, error) {
	return query(ctx, s, maintenanceTable, deviceObjectsSQL(maintenanceTable.selectSQL(), KindMaintenance), deviceID)
}

// DeviceDrivers returns the drivers linked to the device or its groups.
func (s *Store) DeviceDrivers(ctx context.Context, deviceID int64) ([]model.Driver, error) {
	return query(ctx, s, driverTable, deviceObjectsSQL(driverTable.selectSQL(), KindDriver), deviceID)
}

// LinkedDevices returns the ids of the devices linked to the device (tc_device_device;
// groups don't link devices in Java's cache).
func (s *Store) LinkedDevices(ctx context.Context, deviceID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT DISTINCT d.id FROM tc_devices d JOIN tc_device_device l ON l.linkedDeviceId = d.id"+
			" WHERE l.deviceId = $1::bigint ORDER BY d.id", deviceID)
	if err != nil {
		return nil, fmt.Errorf("select linked devices: %w", err)
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

// QueuedCommands returns up to limit (0: all) of the device's queued commands, oldest
// first (CommandsManager.readQueuedCommands).
func (s *Store) QueuedCommands(ctx context.Context, deviceID int64, limit int) ([]model.QueuedCommand, error) {
	sql := queuedCommandTable.selectSQL() + " WHERE deviceId = $1::bigint ORDER BY id"
	if limit > 0 {
		sql += " LIMIT " + itoa(limit)
	}
	return query(ctx, s, queuedCommandTable, sql, deviceID)
}

// UpdateDeviceAttributes writes the attributes column (CommandsManager.updateNotificationToken).
func (s *Store) UpdateDeviceAttributes(ctx context.Context, d *model.Device) error {
	a, err := attrsValue(&d.Attributes)
	if err != nil {
		return err
	}
	return s.update(ctx, "tc_devices", d.ID, []string{"attributes"}, []any{a})
}
