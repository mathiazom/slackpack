package packing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	. "github.com/mathiazom/slackpack/internal/seaweedfs"
	"github.com/rusq/slackdump/v3"
)

// archiveMessageFiles resolves each file attached to a message to an archived copy in
// SeaweedFS, injecting the resulting pointer into the file's JSON object so slackback
// never needs to know about the `file` table. Returns the updated JSON even when some
// files failed to archive, so the message is still stored and the failed files can be
// retried on a later pack run.
func archiveMessageFiles(db *pgx.Conn, sd *slackdump.Session, seaweedMasterUrl string, jsonData []byte) ([]byte, error) {
	var message map[string]interface{}
	if err := json.Unmarshal(jsonData, &message); err != nil {
		return jsonData, fmt.Errorf("failed to parse message JSON: %w", err)
	}

	files, ok := message["files"].([]interface{})
	if !ok || len(files) == 0 {
		return jsonData, nil
	}

	var firstErr error
	for _, fileEntry := range files {
		file, ok := fileEntry.(map[string]interface{})
		if !ok {
			continue
		}
		slackFileId, _ := file["id"].(string)
		downloadUrl, _ := file["url_private_download"].(string)
		if slackFileId == "" || downloadUrl == "" {
			continue
		}
		name, _ := file["name"].(string)
		if name == "" {
			name = slackFileId
		}

		fileId, err := archiveMessageFile(db, sd, seaweedMasterUrl, slackFileId, name, downloadUrl)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		file["archived_file_id"] = fileId
	}

	updatedJson, err := json.Marshal(message)
	if err != nil {
		return jsonData, fmt.Errorf("failed to re-marshal message JSON: %w", err)
	}

	return updatedJson, firstErr
}

func archiveMessageFile(db *pgx.Conn, sd *slackdump.Session, seaweedMasterUrl, slackFileId, name, downloadUrl string) (string, error) {
	var fileId string
	err := db.QueryRow(context.Background(), "SELECT file_id FROM file WHERE public_id = $1", slackFileId).Scan(&fileId)
	if err == nil {
		return fileId, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("failed to check file existence '%s': %w", slackFileId, err)
	}

	var data bytes.Buffer
	if err := sd.Client().GetFile(downloadUrl, &data); err != nil {
		return "", fmt.Errorf("download failed for file '%s': %w", slackFileId, err)
	}

	fileId, err = UploadToSeaweedFS(seaweedMasterUrl, data.Bytes(), name)
	if err != nil {
		return "", fmt.Errorf("upload failed for file '%s': %w", slackFileId, err)
	}

	_, err = db.Exec(context.Background(), "INSERT INTO file (public_id, slack_url, file_id) VALUES ($1, $2, $3)", slackFileId, downloadUrl, fileId)
	if err != nil {
		return "", fmt.Errorf("insert failed for file '%s': %w", slackFileId, err)
	}

	fmt.Printf("Archived file '%s'\n", slackFileId)
	return fileId, nil
}
