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

// volatileChannelKeys change without anything worth a snapshot happening, so they
// aren't stored.
var volatileChannelKeys = [][]string{
	{"updated"},
	{"properties", "is_dormant"},
}

// PackChannels snapshots every public channel, returning their IDs.
func PackChannels(slackClient *slackapi.Client, db *pgx.Conn) ([]string, error) {
	channels, err := slackClient.PublicChannels(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list channels: %v\n", err)
		return nil, err
	}

	var channelIds []string
	count := 0
	for _, channel := range channels {
		var ids struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(channel, &ids); err != nil || ids.ID == "" {
			fmt.Fprintf(os.Stderr, "failed to parse channel: %v\n", err)
			continue
		}
		channelIds = append(channelIds, ids.ID)
		jsonData, err := stripKeys(channel, volatileChannelKeys...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to parse channel %s: %v\n", ids.ID, err)
			continue
		}
		query := `
			WITH latest_snapshot AS (
				SELECT data
				FROM channel
				WHERE public_id = $1
				ORDER BY timestamp DESC
				LIMIT 1
			)
			INSERT INTO channel (public_id, data)
			SELECT $1::text, $2::jsonb
			WHERE NOT EXISTS (
				SELECT 1 
				FROM latest_snapshot 
				WHERE data = $2::jsonb
			)
		`
		tag, err := db.Exec(context.Background(), query, ids.ID, string(jsonData))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == dbutils.ErrCodeUniqueConstraintViolation {
				// channel is up-to-date
				continue
			}
			fmt.Fprintf(os.Stderr, "Insert failed for channel %s: %v\n", ids.ID, err)
			continue
		}

		if tag.RowsAffected() > 0 {
			fmt.Printf("Inserted new snapshot for channel %s\n", ids.ID)
			count++
		}
	}

	if count == 0 {
		fmt.Printf("Channel snapshots are up-to-date\n")
	} else {
		fmt.Printf("Inserted %d channel snapshots\n", count)
	}

	return channelIds, nil
}
