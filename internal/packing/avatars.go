package packing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/mathiazom/slackpack/internal/seaweedfs"
)

// avatarSizes maps each archived Slack profile image key to the key its SeaweedFS
// pointer is embedded under, next to it in the snapshot's profile object.
var avatarSizes = []struct {
	imageKey    string
	archivedKey string
}{
	{"image_72", "archived_avatar_72_id"},
	{"image_512", "archived_avatar_512_id"},
}

// errAvatarGone signals a download that will never succeed (403/404/410), as opposed
// to a transient failure that should be retried on a later pack run.
var errAvatarGone = errors.New("avatar is gone")

var avatarHttpClient = &http.Client{Timeout: 30 * time.Second}

type snapshotAvatars struct {
	id   int
	urls []string // indexed like avatarSizes, empty when already archived or not wanted
}

// archiveSnapshotAvatars archives the profile images of every user snapshot that is
// missing a pointer, embedding the pointers into the snapshot JSON in place. This
// backfills historical snapshots too; change detection ignores the archived keys, so
// this never produces new snapshots. Images already recorded as permanently failed
// are skipped without any request.
func archiveSnapshotAvatars(db *pgx.Conn, seaweedMasterUrl string) {
	query := `
		SELECT id, url_72, url_512
		FROM (
			SELECT u.id,
			       CASE WHEN p ? 'archived_avatar_72_id' OR f72.slack_url IS NOT NULL THEN '' ELSE coalesce(p->>'image_72', '') END AS url_72,
			       CASE WHEN p ? 'archived_avatar_512_id' OR f512.slack_url IS NOT NULL THEN '' ELSE coalesce(p->>'image_512', '') END AS url_512
			FROM (SELECT id, data->'profile' AS p FROM "user") u
			LEFT JOIN avatar f72 ON f72.slack_url = p->>'image_72' AND f72.failed_at IS NOT NULL
			LEFT JOIN avatar f512 ON f512.slack_url = p->>'image_512' AND f512.failed_at IS NOT NULL
		) pending
		WHERE url_72 <> '' OR url_512 <> ''
		ORDER BY id
	`
	rows, err := db.Query(context.Background(), query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to query user snapshot avatars: %v\n", err)
		return
	}
	snapshots, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (snapshotAvatars, error) {
		var s snapshotAvatars
		var url72, url512 string
		err := row.Scan(&s.id, &url72, &url512)
		s.urls = []string{url72, url512}
		return s, err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read user snapshot avatars: %v\n", err)
		return
	}

	count := 0
	for _, snapshot := range snapshots {
		for i, size := range avatarSizes {
			slackUrl := snapshot.urls[i]
			if slackUrl == "" {
				continue
			}
			fileId, err := archiveAvatar(db, seaweedMasterUrl, slackUrl)
			if err != nil {
				if !errors.Is(err, errAvatarGone) {
					fmt.Fprintf(os.Stderr, "failed to archive avatar '%s': %v\n", slackUrl, err)
				}
				continue
			}
			_, err = db.Exec(context.Background(),
				`UPDATE "user" SET data = jsonb_set(data, ARRAY['profile', $2::text], to_jsonb($3::text)) WHERE id = $1`,
				snapshot.id, size.archivedKey, fileId)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to embed avatar in user snapshot %d: %v\n", snapshot.id, err)
				continue
			}
			count++
		}
	}

	if count > 0 {
		fmt.Printf("Embedded %d archived avatars in user snapshots\n", count)
	}
}

// archiveAvatar resolves a Slack avatar URL to an archived copy in SeaweedFS, archiving
// it on first sight. Returns errAvatarGone when the image is permanently unavailable.
func archiveAvatar(db *pgx.Conn, seaweedMasterUrl, slackUrl string) (string, error) {
	var fileId *string
	err := db.QueryRow(context.Background(), "SELECT file_id FROM avatar WHERE slack_url = $1", slackUrl).Scan(&fileId)
	if err == nil {
		if fileId == nil {
			return "", errAvatarGone
		}
		return *fileId, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("failed to check avatar existence: %w", err)
	}

	data, err := fetchAvatarImage(slackUrl)
	if errors.Is(err, errAvatarGone) {
		_, dbErr := db.Exec(context.Background(), "INSERT INTO avatar (slack_url, failed_at) VALUES ($1, CURRENT_TIMESTAMP)", slackUrl)
		if dbErr != nil {
			return "", fmt.Errorf("failed to record gone avatar: %w", dbErr)
		}
		fmt.Fprintf(os.Stderr, "avatar '%s' is gone, will not retry: %v\n", slackUrl, err)
		return "", err
	}
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	name := slackUrl
	if parsed, err := url.Parse(slackUrl); err == nil {
		name = path.Base(parsed.Path)
	}
	uploadedId, err := UploadToSeaweedFS(seaweedMasterUrl, data, name)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	_, err = db.Exec(context.Background(), "INSERT INTO avatar (slack_url, file_id) VALUES ($1, $2)", slackUrl, uploadedId)
	if err != nil {
		return "", fmt.Errorf("insert failed: %w", err)
	}

	fmt.Printf("Archived avatar '%s'\n", slackUrl)
	return uploadedId, nil
}

func fetchAvatarImage(slackUrl string) ([]byte, error) {
	resp, err := avatarHttpClient.Get(slackUrl)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return io.ReadAll(resp.Body)
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return nil, fmt.Errorf("%w: status %d", errAvatarGone, resp.StatusCode)
	default:
		return nil, fmt.Errorf("fetch failed with status: %d", resp.StatusCode)
	}
}
