package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// UserByLogin finds the user whose e-mail or login equals name, ignoring case
// (LoginService.login: LOWER(email) = ? OR LOWER(login) = ?). With several matches it
// returns the first the database yields, as Java's getObject does.
func (s *Store) UserByLogin(ctx context.Context, name string) (model.User, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	rows, err := query(ctx, s, userTable, " WHERE LOWER(email) = $1 OR LOWER(login) = $1 LIMIT 1", name)
	if err != nil {
		return model.User{}, err
	}
	if len(rows) == 0 {
		return model.User{}, fmt.Errorf("user %q: %w", name, ErrNotFound)
	}
	return rows[0], nil
}

// NoUsers reports whether tc_users is empty (UserUtil.isEmpty: the first user to register
// becomes administrator, and GET /api/server reports newServer).
func (s *Store) NoUsers(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM tc_users)").Scan(&exists); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return !exists, nil
}

// Manages reports whether manager has managedUser in tc_user_user.
func (s *Store) Manages(ctx context.Context, manager, managedUser int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM tc_user_user WHERE userid = $1::bigint AND manageduserid = $2::bigint)",
		manager, managedUser).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("tc_user_user: %w", err)
	}
	return exists, nil
}

// KeyPair returns the token signing key pair (DER: X.509 public, PKCS#8 private). Java
// reads tc_keystore without an ORDER BY; with one row that is the same row, and the
// lowest id is the stable choice if Java ever wrote two.
func (s *Store) KeyPair(ctx context.Context) (public, private []byte, err error) {
	err = s.pool.QueryRow(ctx, "SELECT publickey, privatekey FROM tc_keystore ORDER BY id LIMIT 1").Scan(&public, &private)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("tc_keystore: %w", ErrNotFound)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("tc_keystore: %w", err)
	}
	return public, private, nil
}

// InsertKeyPair stores a new signing key pair (CryptoManager.loadOrGenerate).
func (s *Store) InsertKeyPair(ctx context.Context, public, private []byte) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO tc_keystore (publickey, privatekey) VALUES ($1, $2)", public, private)
	if err != nil {
		return fmt.Errorf("tc_keystore: %w", err)
	}
	return nil
}

// TokenRevoked reports whether a token id is in tc_revoked_tokens.
func (s *Store) TokenRevoked(ctx context.Context, id int64) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM tc_revoked_tokens WHERE id = $1)", id).Scan(&exists); err != nil {
		return false, fmt.Errorf("tc_revoked_tokens: %w", err)
	}
	return exists, nil
}

// ErrDuplicate is returned when a row with the same key exists.
var ErrDuplicate = errors.New("already exists")

// RevokeToken adds a token id to tc_revoked_tokens. Revoking twice is ErrDuplicate, as
// Java's insert fails on the primary key.
func (s *Store) RevokeToken(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO tc_revoked_tokens (id) VALUES ($1)", id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("token %d: %w", id, ErrDuplicate)
	}
	if err != nil {
		return fmt.Errorf("tc_revoked_tokens: %w", err)
	}
	return nil
}
