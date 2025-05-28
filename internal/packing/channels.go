package packing

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/rusq/slackdump/v3"
	"os"
)

func PackChannels(sdClient *slackdump.Session, dbClient *pgx.Conn) {
	channels, err := sdClient.GetChannels(context.Background(), "public_channel")
	if err != nil {
		fmt.Fprintf(os.Stderr, "slackdumpclient 'GetChannels' failed: %v\n", err)
		return
	}

	for _, channel := range channels {
		jsonData, err := json.Marshal(channel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON marshal failed: %v\n", err)
			continue
		}

		_, err = dbClient.Exec(context.Background(), "INSERT INTO channel (public_id, data) VALUES ($1, $2)", channel.ID, string(jsonData))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Insert failed for %s: %v\n", channel.ID, err)
			continue
		}
	}

	fmt.Printf("Inserted %d channels successfully\n", len(channels))
}
