package packing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mathiazom/slackpack/internal/dbutils"
	"github.com/mathiazom/slackpack/internal/slackapi"
	"os"
)

// volatileUserKeys change without anything worth a snapshot happening (statuses and
// huddles come and go during the day), so they aren't stored.
var volatileUserKeys = [][]string{
	{"profile", "status_text"},
	{"profile", "status_text_canonical"},
	{"profile", "status_emoji"},
	{"profile", "status_emoji_display_info"},
	{"profile", "status_expiration"},
	{"profile", "huddle_state"},
	{"profile", "huddle_state_expiration_ts"},
}

func PackUsers(slackClient *slackapi.Client, db *pgx.Conn, seaweedMasterUrl string) {
	packUserSnapshots(slackClient, db)
	archiveSnapshotAvatars(db, seaweedMasterUrl)
	embedSnapshotAvatarSince(db)
}

func packUserSnapshots(slackClient *slackapi.Client, db *pgx.Conn) {
	users, err := slackClient.Users(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list users: %v\n", err)
		return
	}

	count := 0
	for _, user := range users {
		var ids struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(user, &ids); err != nil || ids.ID == "" {
			fmt.Fprintf(os.Stderr, "failed to parse user: %v\n", err)
			continue
		}
		jsonData, err := stripKeys(user, volatileUserKeys...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to parse user %s: %v\n", ids.ID, err)
			continue
		}
		// archived avatar pointers and avatar since are embedded after insertion, so they
		// are not part of the user's Slack data and must not count as a change
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
				WHERE data #- '{profile,archived_avatar_72_id}' #- '{profile,archived_avatar_512_id}' #- '{profile,archived_avatar_since}' = $2::jsonb
			)
		`
		tag, err := db.Exec(context.Background(), query, ids.ID, string(jsonData))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == dbutils.ErrCodeUniqueConstraintViolation {
				// user is up-to-date
				continue
			}
			fmt.Fprintf(os.Stderr, "insert failed for user %s: %v\n", ids.ID, err)
			continue
		}

		if tag.RowsAffected() > 0 {
			fmt.Printf("Inserted new snapshot for user %s\n", ids.ID)
			count++
		}
	}

	if count == 0 {
		fmt.Printf("User snapshots are up-to-date\n")
		return
	}

	fmt.Printf("Inserted %d user snapshots\n", count)
}
