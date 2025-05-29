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

func PackChannels(sdClient *slackdump.Session, dbClient *pgx.Conn) {
	channels, err := sdClient.GetChannels(context.Background(), "public_channel")
	if err != nil {
		fmt.Fprintf(os.Stderr, "slackdumpclient 'GetChannels' failed: %v\n", err)
		return
	}

	count := 0
	for _, channel := range channels {
		jsonData, err := json.Marshal(channel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON marshal failed: %v\n", err)
			continue
		}
		_, err = dbClient.Exec(context.Background(), "INSERT INTO channel (public_id, data) VALUES ($1, $2)", channel.ID, string(jsonData))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == dbutils.ErrCodeUniqueConstraintViolation {
				// channel is up-to-date
				continue
			}
			fmt.Fprintf(os.Stderr, "Insert failed for channel %s: %v\n", channel.ID, err)
			continue
		}

		fmt.Printf("Inserted new snapshot for channel %s\n", channel.ID)
		count++
	}

	if count == 0 {
		fmt.Printf("Channel snapshots are up-to-date\n")
		return
	}

	fmt.Printf("Inserted %d channel snapshots\n", count)
}
