package packing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/mathiazom/slackpack/internal/slackapi"
	"os"
)

// personalMessageKeys are the token owner's own state on a message ("save for
// later"), not part of the conversation, so they aren't stored.
var personalMessageKeys = [][]string{{"saved"}}

func PackMessagesFromChannels(channelIds []string, slackClient *slackapi.Client, db *pgx.Conn, seaweedMasterUrl string) {
	for _, channelId := range channelIds {
		PackChannelMessages(channelId, slackClient, db, seaweedMasterUrl)
	}
	deps := filePassDeps{db, slackClient, seaweedMasterUrl}
	runFilePass(deps, conversionPass)
	runFilePass(deps, probePass)
	runFilePass(deps, thumbnailPass)
	runFilePass(deps, posterPass)
}

func PackChannelMessages(channelId string, slackClient *slackapi.Client, db *pgx.Conn, seaweedMasterUrl string) {
	messages, err := channelMessages(slackClient, channelId)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch messages for channel %s: %v\n", channelId, err)
		return
	}

	channelDbId, err := getChannelDbIdByPublicId(db, channelId)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to retrieve database id for channel %s: %v\n", channelId, err)
		return
	}

	errorCount := 0
	for _, message := range messages {
		messageId := message.ts
		jsonData, err := stripKeys(message.raw, personalMessageKeys...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to parse message %s: %v\n", messageId, err)
			errorCount++
			continue
		}

		jsonData, fileErr := archiveMessageFiles(db, slackClient, seaweedMasterUrl, jsonData)
		if fileErr != nil {
			fmt.Fprintf(os.Stderr, "file archiving failed for message %s: %v\n", messageId, fileErr)
		}

		var threadTs *string
		if message.threadTs != "" {
			threadTs = &message.threadTs
		}

		_, err = db.Exec(context.Background(), "INSERT INTO message (public_id, channel_id, data, thread_ts) VALUES ($1, $2, $3, $4) ON CONFLICT (public_id) DO UPDATE SET data = $3, thread_ts = $4", messageId, channelDbId, string(jsonData), threadTs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "upsert failed for message %s: %v\n", messageId, err)
			errorCount++
			continue
		}
	}

	if errorCount > 0 {
		if errorCount < len(messages) {
			fmt.Printf("Message snapshots partially updated (%d failures) for channel %s\n", errorCount, channelId)
			return
		}
		fmt.Printf("No message snapshots updated (%d failures) for channel %s\n", errorCount, channelId)
		return
	}

	fmt.Printf("Message snapshots updated for channel %s\n", channelId)
}

type channelMessage struct {
	raw      json.RawMessage
	ts       string
	threadTs string
}

// channelMessages fetches a channel's messages, each thread parent followed by its
// replies. Thread broadcasts are replies that also show in the channel, so their
// thread is fetched from its parent instead.
func channelMessages(slackClient *slackapi.Client, channelId string) ([]channelMessage, error) {
	history, err := slackClient.History(context.Background(), channelId)
	if err != nil {
		return nil, err
	}

	var messages []channelMessage
	for i := len(history) - 1; i >= 0; i-- {
		message, err := parseChannelMessage(history[i])
		if err != nil {
			return nil, err
		}
		messages = append(messages, message.channelMessage)
		if message.threadTs == "" || message.subtype == "thread_broadcast" {
			continue
		}
		replies, err := slackClient.Replies(context.Background(), channelId, message.threadTs)
		if err != nil {
			return nil, err
		}
		for _, raw := range replies {
			reply, err := parseChannelMessage(raw)
			if err != nil {
				return nil, err
			}
			messages = append(messages, reply.channelMessage)
		}
	}
	return messages, nil
}

type parsedMessage struct {
	channelMessage
	subtype string
}

func parseChannelMessage(raw json.RawMessage) (parsedMessage, error) {
	var fields struct {
		Ts       string `json:"ts"`
		ThreadTs string `json:"thread_ts"`
		Subtype  string `json:"subtype"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return parsedMessage{}, fmt.Errorf("failed to parse message: %w", err)
	}
	if fields.Ts == "" {
		return parsedMessage{}, errors.New("message has no ts")
	}
	return parsedMessage{channelMessage{raw, fields.Ts, fields.ThreadTs}, fields.Subtype}, nil
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
