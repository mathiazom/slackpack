package packing

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	. "github.com/mathiazom/slackpack/internal/dbutils"
	. "github.com/mathiazom/slackpack/internal/seaweedfs"
	"github.com/rusq/slackdump/v3"
	"os"
	"strings"
)

func PackEmojis(sd *slackdump.Session, db *pgx.Conn, seaweedMasterUrl string) {
	emojis, err := sd.DumpEmojis(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "slackdump 'DumpEmojis' failed: %v\n", err)
		return
	}

	var originalEmojiIds []string
	var aliasEmojiIds = make(map[string]string)

	for key, value := range emojis {
		if strings.HasPrefix(value, "alias:") {
			aliasEmojiIds[key] = value
		} else {
			originalEmojiIds = append(originalEmojiIds, key)
		}
	}

	count := 0
	for _, emojiId := range originalEmojiIds {
		slackUrl := emojis[emojiId]

		var snapshotExists bool
		query := `
			WITH latest_snapshot AS (
				SELECT slack_url
				FROM emoji
				WHERE public_id = $1
				ORDER BY timestamp DESC
				LIMIT 1
			)
			SELECT EXISTS (
				SELECT 1 
				FROM latest_snapshot 
				WHERE slack_url = $2
			)
		`
		err := db.QueryRow(context.Background(), query, emojiId, slackUrl).Scan(&snapshotExists)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to check emoji existence '%s': %v\n", emojiId, err)
			continue
		}

		if snapshotExists {
			// since original has not changed, aliases have not changed either
			for key, value := range aliasEmojiIds {
				if strings.TrimPrefix(value, "alias:") == emojiId {
					delete(aliasEmojiIds, key)
				}
			}
			continue
		}

		fileId, err := UploadImageToSeaweedFS(seaweedMasterUrl, slackUrl)
		if err != nil {
			fmt.Fprintf(os.Stderr, "upload failed for emoji '%s': %v\n", emojiId, err)
			continue
		}

		for key, value := range aliasEmojiIds {
			if strings.TrimPrefix(value, "alias:") == emojiId {
				aliasEmojiIds[key] = fileId
			}
		}

		_, err = db.Exec(context.Background(), "INSERT INTO emoji (public_id, slack_url, file_id) VALUES ($1, $2, $3)", emojiId, slackUrl, fileId)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == ErrCodeUniqueConstraintViolation {
				fmt.Fprintf(os.Stderr, "insert failed for emoji '%s': %v\n", emojiId, err)
				// emoji is up-to-date
				continue
			}
			fmt.Fprintf(os.Stderr, "insert failed for emoji '%s': %v\n", emojiId, err)
			continue
		}

		fmt.Printf("Inserted new snapshot for emoji '%s'\n", emojiId)
		count++
	}
	for aliasEmojiId, fileId := range aliasEmojiIds {
		if strings.HasPrefix(fileId, "alias:") {
			fmt.Fprintf(os.Stderr, "missing fileId for alias emoji '%s'\n", aliasEmojiId)
			continue
		}
		slackUrl := emojis[aliasEmojiId]
		_, err = db.Exec(context.Background(), "INSERT INTO emoji (public_id, slack_url, file_id) VALUES ($1, $2, $3)", aliasEmojiId, slackUrl, fileId)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == ErrCodeUniqueConstraintViolation {
				// emoji is up-to-date
				continue
			}
			fmt.Fprintf(os.Stderr, "insert failed for alias emoji '%s': %v\n", aliasEmojiId, err)
			continue
		}

		fmt.Printf("Inserted new snapshot for alias emoji '%s'\n", aliasEmojiId)
		count++
	}

	if count == 0 {
		fmt.Printf("Emoji snapshots are up-to-date\n")
		return
	}

	fmt.Printf("Inserted %d emoji snapshots\n", count)
}
