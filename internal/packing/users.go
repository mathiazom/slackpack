package packing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mathiazom/slackpack/internal/dbutils"
	"github.com/rusq/slackdump/v3"
	"os"
)

func PackUsers(sd *slackdump.Session, db *pgx.Conn) {
	users, err := sd.GetUsers(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "slackdump 'GetUsers' failed: %v\n", err)
		return
	}

	count := 0
	for _, user := range users {
		jsonData, err := json.Marshal(user)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON marshal failed: %v\n", err)
			continue
		}
		query := `
			WITH latest_snapshot AS (
				SELECT data
				FROM "user"
				WHERE public_id = $1
				ORDER BY timestamp DESC
				LIMIT 1
			)
			INSERT INTO "user" (public_id, data)
			SELECT $1::text, $2::jsonb
			WHERE NOT EXISTS (
				SELECT 1 
				FROM latest_snapshot 
				WHERE data = $2::jsonb
			)
		`
		tag, err := db.Exec(context.Background(), query, user.ID, string(jsonData))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == dbutils.ErrCodeUniqueConstraintViolation {
				// user is up-to-date
				continue
			}
			fmt.Fprintf(os.Stderr, "insert failed for user %s: %v\n", user.ID, err)
			continue
		}

		if tag.RowsAffected() > 0 {
			fmt.Printf("Inserted new snapshot for user %s\n", user.ID)
			count++
		}
	}

	if count == 0 {
		fmt.Printf("User snapshots are up-to-date\n")
		return
	}

	fmt.Printf("Inserted %d user snapshots\n", count)
}
