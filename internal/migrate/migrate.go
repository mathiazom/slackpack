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
	Name      string
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
            id VARCHAR(14) PRIMARY KEY,
            name TEXT NOT NULL
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

		if d.IsDir() {
			return nil
		}

		filename := d.Name()
		ok, timestamp, name, direction := parseMigrationFilename(filename)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s: unknown migration file format\n", filename)
			return nil
		}

		if direction != Up {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", path, err)
		}

		migrations = append(migrations, Migration{
			Timestamp: timestamp,
			Name:      name,
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

type MigrationDirection int

const (
	Up MigrationDirection = iota
	Down
)

func parseMigrationFilename(filename string) (ok bool, timestamp string, name string, direction MigrationDirection) {
	if !strings.HasSuffix(filename, ".sql") {
		return false, timestamp, name, Up
	}
	nameWithoutExt := strings.TrimSuffix(filename, ".sql")
	lastDot := strings.LastIndex(nameWithoutExt, ".")
	if lastDot == -1 {
		return false, timestamp, name, Up
	}
	directionString := nameWithoutExt[lastDot+1:]
	timeAndName := nameWithoutExt[:lastDot]
	parts := strings.Split(timeAndName, "_")
	if len(parts) < 2 {
		return false, timestamp, name, Up
	}
	timestamp = parts[0]
	name = parts[1]
	if directionString == "up" {
		direction = Up
	} else {
		direction = Down
	}
	return true, timestamp, name, direction
}

func getAppliedMigrations(conn *pgx.Conn) (map[string]bool, error) {
	applied := make(map[string]bool)

	rows, err := conn.Query(context.Background(), "SELECT (id) FROM migration_history")
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

		fmt.Printf("Applying migration %s...\n", migration.Name)

		// Execute migration
		_, err = tx.Exec(ctx, migration.Content)
		if err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", migration.Name, err)
		}

		// Record migration in history
		_, err = tx.Exec(ctx, "INSERT INTO migration_history (id, name) VALUES ($1, $2)", migration.Timestamp, migration.Name)
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
