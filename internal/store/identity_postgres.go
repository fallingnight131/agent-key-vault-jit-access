package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	"github.com/fallingnight/akv/internal/identity"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgreSQLIdentityRepository struct{ database *sql.DB }

func NewPostgreSQLIdentityRepository(database *sql.DB) *PostgreSQLIdentityRepository {
	return &PostgreSQLIdentityRepository{database: database}
}

func (repository *PostgreSQLIdentityRepository) CreateInitialAdmin(ctx context.Context, record identity.AccountRecord) error {
	_, err := repository.database.ExecContext(ctx, `INSERT INTO users (id,username,password_hash,is_admin,approve_all,status,created_at,updated_at) VALUES ($1,$2,$3,true,false,'ACTIVE',$4,$4)`, record.ID, record.Username, string(record.PasswordHash), record.CreatedAt)
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return identity.ErrAlreadyInitialized
	}
	return err
}

func (repository *PostgreSQLIdentityRepository) FindActiveAccountByUsername(ctx context.Context, username string) (identity.AccountRecord, error) {
	var record identity.AccountRecord
	var passwordHash string
	err := repository.database.QueryRowContext(ctx, `SELECT id,username,password_hash,is_admin,approve_all,created_at FROM users WHERE username=$1 AND status='ACTIVE'`, username).Scan(&record.ID, &record.Username, &passwordHash, &record.IsAdmin, &record.ApproveAll, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.AccountRecord{}, identity.ErrNotFound
	}
	record.PasswordHash = []byte(passwordHash)
	record.Active = err == nil
	return record, err
}

func (repository *PostgreSQLIdentityRepository) CreateSession(ctx context.Context, record identity.SessionRecord) error {
	_, err := repository.database.ExecContext(ctx, `INSERT INTO web_sessions (id,user_id,token_hash,expires_at,created_at) VALUES ($1,$2,$3,$4,$5)`, record.ID, record.UserID, record.TokenHash[:], record.ExpiresAt, record.CreatedAt)
	return err
}

func (repository *PostgreSQLIdentityRepository) FindActiveSessionByTokenHash(ctx context.Context, hash [sha256.Size]byte, now time.Time) (identity.User, error) {
	var user identity.User
	err := repository.database.QueryRowContext(ctx, `SELECT u.id,u.username,u.is_admin,u.approve_all FROM web_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>$2 AND u.status='ACTIVE'`, hash[:], now).Scan(&user.ID, &user.Username, &user.IsAdmin, &user.ApproveAll)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.User{}, identity.ErrNotFound
	}
	user.OwnerActive = err == nil
	return user, err
}

func (repository *PostgreSQLIdentityRepository) RevokeSession(ctx context.Context, hash [sha256.Size]byte, now time.Time) error {
	result, err := repository.database.ExecContext(ctx, `UPDATE web_sessions SET revoked_at=$2 WHERE token_hash=$1 AND revoked_at IS NULL`, hash[:], now)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return identity.ErrNotFound
	}
	return nil
}
