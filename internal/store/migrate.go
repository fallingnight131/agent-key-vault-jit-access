package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration is an immutable, checksummed database change.
type Migration struct {
	Version  string
	SQL      string
	Checksum string
}

// MigrationStore persists migration state and applies each change atomically.
type MigrationStore interface {
	AppliedMigrations(context.Context) (map[string]string, error)
	ApplyMigration(context.Context, Migration) error
}

// LoadMigrations returns embedded migrations in version order.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		version := strings.TrimSuffix(entry.Name(), ".sql")
		if version == "" {
			return nil, fmt.Errorf("migration %q has empty version", entry.Name())
		}
		digest := sha256.Sum256(contents)
		migrations = append(migrations, Migration{
			Version:  version,
			SQL:      string(contents),
			Checksum: hex.EncodeToString(digest[:]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

// Migrate applies missing migrations and refuses changed migration history.
func Migrate(ctx context.Context, migrationStore MigrationStore) error {
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}
	applied, err := migrationStore.AppliedMigrations(ctx)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}

	for _, migration := range migrations {
		if checksum, ok := applied[migration.Version]; ok {
			if checksum != migration.Checksum {
				return fmt.Errorf("migration %s checksum changed", migration.Version)
			}
			continue
		}
		if err := migrationStore.ApplyMigration(ctx, migration); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.Version, err)
		}
	}
	return nil
}
