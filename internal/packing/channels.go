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
	sdtypes "github.com/rusq/slackdump/v3/types"
	"os"
)

func PackChannels(sd *slackdump.Session, db *pgx.Conn) (sdtypes.Channels, error) {
	channels, err := sd.GetChannels(context.Background(), "public_channel")
	if err != nil {
		fmt.Fprintf(os.Stderr, "slackdumpclient 'GetChannels' failed: %v\n", err)
		return nil, err
	}

	count := 0
	for _, channel := range channels {
		jsonData, err := json.Marshal(channel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON marshal failed: %v\n", err)
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
		tag, err := db.Exec(context.Background(), query, channel.ID, string(jsonData))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == dbutils.ErrCodeUniqueConstraintViolation {
				// channel is up-to-date
				continue
			}
			fmt.Fprintf(os.Stderr, "Insert failed for channel %s: %v\n", channel.ID, err)
			continue
		}

		if tag.RowsAffected() > 0 {
			fmt.Printf("Inserted new snapshot for channel %s\n", channel.ID)
			count++
		}
	}

	if count == 0 {
		fmt.Printf("Channel snapshots are up-to-date\n")
	} else {
		fmt.Printf("Inserted %d channel snapshots\n", count)
	}

	return channels, nil
}
