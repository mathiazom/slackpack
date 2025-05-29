package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

type Migration struct {
	Timestamp string
	Direction string
	Filename  string
	Content   string
}

func RunMigrations() error {
	conn, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_CONNECTION_STRING"))
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer conn.Close(context.Background())

	if err := createMigrationTable(conn); err != nil {
		return err
	}

	migrations, err := loadMigrations("./migrations")
	if err != nil {
		return err
	}

	applied, err := getAppliedMigrations(conn)
	if err != nil {
		return err
	}

	return applyPendingMigrations(conn, migrations, applied)
}

func createMigrationTable(conn *pgx.Conn) error {
	query := `
        CREATE TABLE IF NOT EXISTS migration_history (
            migration_id VARCHAR(14) PRIMARY KEY
        )`

	_, err := conn.Exec(context.Background(), query)
	if err != nil {
		return fmt.Errorf("failed to create migration_history table: %w", err)
	}

	fmt.Println("Migration history table ready")
	return nil
}

func loadMigrations(dir string) ([]Migration, error) {
	var migrations []Migration

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}

		filename := d.Name()
		parts := strings.Split(filename, "_")
		if len(parts) < 3 {
			return nil
		}

		timestamp := parts[0]
		direction := parts[1]

		// Only process "up" migrations
		if direction != "up" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", path, err)
		}

		migrations = append(migrations, Migration{
			Timestamp: timestamp,
			Direction: direction,
			Filename:  filename,
			Content:   string(content),
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to load migrations: %w", err)
	}

	// Sort migrations by timestamp
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Timestamp < migrations[j].Timestamp
	})

	return migrations, nil
}

func getAppliedMigrations(conn *pgx.Conn) (map[string]bool, error) {
	applied := make(map[string]bool)

	rows, err := conn.Query(context.Background(), "SELECT migration_id FROM migration_history")
	if err != nil {
		return nil, fmt.Errorf("failed to query migration history: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var migrationID string
		if err := rows.Scan(&migrationID); err != nil {
			return nil, fmt.Errorf("failed to scan migration ID: %w", err)
		}
		applied[migrationID] = true
	}

	return applied, nil
}

func applyPendingMigrations(conn *pgx.Conn, migrations []Migration, applied map[string]bool) error {
	ctx := context.Background()
	foundNonAppliedMigrations := false

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	for _, migration := range migrations {
		if applied[migration.Timestamp] {
			if foundNonAppliedMigrations {
				return fmt.Errorf("detected gap in applied migrations, aborting")
			}
			fmt.Printf("Migration %s already applied, skipping\n", migration.Timestamp)
			continue
		}

		foundNonAppliedMigrations = true

		fmt.Printf("Applying migration %s...\n", migration.Filename)

		// Execute migration
		_, err = tx.Exec(ctx, migration.Content)
		if err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", migration.Filename, err)
		}

		// Record migration in history
		_, err = tx.Exec(ctx, "INSERT INTO migration_history (migration_id) VALUES ($1)", migration.Timestamp)
		if err != nil {
			return fmt.Errorf("failed to record migration %s: %w", migration.Timestamp, err)
		}

		fmt.Printf("Migration %s applied successfully\n", migration.Timestamp)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit migrations %w", err)
	}

	fmt.Println("All migrations committed successfully")
	return nil
}
