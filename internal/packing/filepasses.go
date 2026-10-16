package packing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

// errFileUnprocessable, wrapped by a pass's process, signals an archived original it
// can never handle (it can't be decoded or parsed), as opposed to a transient failure
// that should be retried on a later pack run. It's recorded in the pass's failedColumn.
var errFileUnprocessable = errors.New("file can't be processed")

// filePass derives something from archived message files of some types (a converted
// copy, media metadata), stores it on the `file` row and embeds it into the message
// JSON, so slackback never needs to know about the `file` table.
type filePass struct {
	name      string // for logs
	mimetypes []string
	// embeddedKey marks a file object the pass is done with.
	embeddedKey string
	// failedColumn on `file` marks an original that is errFileUnprocessable.
	failedColumn string
	// process returns the fields to embed into the file's JSON object, resolving
	// from the `file` row when the file was processed already.
	process func(db *pgx.Conn, seaweedMasterUrl string, file pendingMessageFile) (map[string]interface{}, error)
}

// pendingMessageFile is an archived file object in a message's JSON, by position.
type pendingMessageFile struct {
	messageId      int
	index          int
	slackFileId    string
	archivedFileId string
	name           string
	mimetype       string
}

// runFilePass runs a file pass over every archived message file it isn't done with.
// This backfills messages no longer in Slack too, since passes read the archived
// original. Files whose original was recorded as unprocessable are skipped.
func runFilePass(db *pgx.Conn, seaweedMasterUrl string, pass filePass) {
	query := `
		SELECT m.id, (f.idx - 1)::int, f.value->>'id', f.value->>'archived_file_id', coalesce(f.value->>'name', ''), lower(f.value->>'mimetype')
		FROM message m
		CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(m.data->'files') = 'array' THEN m.data->'files' ELSE '[]' END) WITH ORDINALITY AS f(value, idx)
		LEFT JOIN file failed ON failed.public_id = f.value->>'id' AND failed.` + pass.failedColumn + ` IS NOT NULL
		WHERE f.value ? 'archived_file_id'
		  AND NOT (f.value ? $2)
		  AND lower(f.value->>'mimetype') = ANY($1)
		  AND failed.id IS NULL
		ORDER BY m.id
	`
	rows, err := db.Query(context.Background(), query, pass.mimetypes, pass.embeddedKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to query message files to %s: %v\n", pass.name, err)
		return
	}
	files, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pendingMessageFile, error) {
		var f pendingMessageFile
		err := row.Scan(&f.messageId, &f.index, &f.slackFileId, &f.archivedFileId, &f.name, &f.mimetype)
		return f, err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read message files to %s: %v\n", pass.name, err)
		return
	}

	count := 0
	for _, f := range files {
		fields, err := pass.process(db, seaweedMasterUrl, f)
		if errors.Is(err, errFileUnprocessable) {
			_, dbErr := db.Exec(context.Background(), "UPDATE file SET "+pass.failedColumn+" = CURRENT_TIMESTAMP WHERE public_id = $1", f.slackFileId)
			if dbErr != nil {
				fmt.Fprintf(os.Stderr, "failed to record unprocessable file '%s': %v\n", f.slackFileId, dbErr)
				continue
			}
			fmt.Fprintf(os.Stderr, "can't %s file '%s' (%s), will not retry: %v\n", pass.name, f.slackFileId, f.mimetype, err)
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to %s file '%s': %v\n", pass.name, f.slackFileId, err)
			continue
		}
		if err := embedFileFields(db, f.messageId, f.index, fields); err != nil {
			fmt.Fprintf(os.Stderr, "failed to embed %s result in message %d: %v\n", pass.name, f.messageId, err)
			continue
		}
		count++
	}

	if count > 0 {
		fmt.Printf("Embedded %d %s results in messages\n", count, pass.name)
	}
}

// embedFileFields merges fields into one file object of a message's JSON.
func embedFileFields(db *pgx.Conn, messageId, index int, fields map[string]interface{}) error {
	fieldsJson, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = db.Exec(context.Background(),
		`UPDATE message SET data = jsonb_set(data, ARRAY['files', $2::int::text], data->'files'->$2::int || $3::jsonb) WHERE id = $1`,
		messageId, index, string(fieldsJson))
	return err
}
