package packing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/rusq/slack"
	"github.com/rusq/slackdump/v3"
	sdtypes "github.com/rusq/slackdump/v3/types"
	"os"
)

func PackMessagesFromChannels(channels sdtypes.Channels, sd *slackdump.Session, db *pgx.Conn, seaweedMasterUrl string) {
	for _, channel := range channels {
		PackChannelMessages(channel, sd, db, seaweedMasterUrl)
	}
}

func PackChannelMessages(channel slack.Channel, sd *slackdump.Session, db *pgx.Conn, seaweedMasterUrl string) {
	conversation, err := sd.DumpAll(context.Background(), channel.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "slackdump 'DumpAll' failed for channel %s: %v\n", channel.ID, err)
		return
	}

	channelDbId, err := getChannelDbIdByPublicId(db, channel.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to retrieve database id for channel %s: %v\n", channel.ID, err)
		return
	}

	messages := conversation.Messages
	flattened := messages[:0:0]
	for _, message := range messages {
		flattened = append(flattened, message)
		flattened = append(flattened, message.ThreadReplies...)
	}

	errorCount := 0
	for _, message := range flattened {
		messageId := message.Msg.Timestamp
		jsonData, err := json.Marshal(message)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON marshal failed for message %s: %v\n", messageId, err)
			continue
		}

		jsonData, fileErr := archiveMessageFiles(db, sd, seaweedMasterUrl, jsonData)
		if fileErr != nil {
			fmt.Fprintf(os.Stderr, "file archiving failed for message %s: %v\n", messageId, fileErr)
		}

		var threadTs *string
		if message.Msg.ThreadTimestamp != "" {
			threadTs = &message.Msg.ThreadTimestamp
		}

		_, err = db.Exec(context.Background(), "INSERT INTO message (public_id, channel_id, data, thread_ts) VALUES ($1, $2, $3, $4) ON CONFLICT (public_id) DO UPDATE SET data = $3, thread_ts = $4", messageId, channelDbId, string(jsonData), threadTs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "upsert failed for message %s: %v\n", messageId, err)
			errorCount++
			continue
		}
	}

	if errorCount > 0 {
		if errorCount < len(flattened) {
			fmt.Printf("Message snapshots partially updated (%d failures) for channel %s\n", errorCount, channel.ID)
			return
		}
		fmt.Printf("No message snapshots updated (%d failures) for channel %s\n", errorCount, channel.ID)
		return
	}

	fmt.Printf("Message snapshots updated for channel %s\n", channel.ID)
}

func getChannelDbIdByPublicId(conn *pgx.Conn, publicId string) (int32, error) {
	var channelID int32

	query := "SELECT id FROM channel WHERE public_id = $1"
	err := conn.QueryRow(context.Background(), query, publicId).Scan(&channelID)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("channel not found with public_id: %s", publicId)
		}
		return 0, fmt.Errorf("failed to fetch channel: %w", err)
	}

	return channelID, nil
}
