package packing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"path"
	"strings"

	. "github.com/mathiazom/slackpack/internal/seaweedfs"
	"github.com/mathiazom/slackpack/internal/slackapi"
	_ "golang.org/x/image/webp"
)

// slackThumbKey is the Slack thumbnail archived for inline display: its longest side
// is thumbMaxSize, enough for slackopy's 480x360 box at 2x. Slack only makes it for
// images larger than that, which are shown from the original instead.
const (
	slackThumbKey = "thumb_960"
	thumbMaxSize  = 960
)

// thumbnailedMimetypes are the image types whose Slack thumbnail is archived: the
// raster types browsers show, plus those that get a converted copy. GIFs are left out,
// as their thumbnail is a still frame, and SVGs, as they're vector.
var thumbnailedMimetypes = append([]string{"image/jpeg", "image/png", "image/webp", "image/avif", "image/bmp"}, mapKeys(imageDecoders)...)

// Keys a thumbnail's pointer and dimensions are embedded under, next to
// archived_file_id in the file's JSON object.
const (
	thumbFileIdKey = "archived_thumb_file_id"
	thumbWidthKey  = "archived_thumb_w"
	thumbHeightKey = "archived_thumb_h"
)

type thumbFile struct {
	fileId string
	width  int
	height int
}

func (t thumbFile) fields() map[string]interface{} {
	return map[string]interface{}{
		thumbFileIdKey: t.fileId,
		thumbWidthKey:  t.width,
		thumbHeightKey: t.height,
	}
}

// thumbnailPass archives Slack's thumbnail of every archived message image large
// enough to have one. Unlike the other passes it reads from Slack, so it only
// backfills files still there; a thumbnail that's gone is never retried.
var thumbnailPass = filePass{
	name:         "thumbnail",
	mimetypes:    thumbnailedMimetypes,
	where:        "coalesce(f.value->>'" + slackThumbKey + "', '') <> ''",
	embeddedKey:  thumbFileIdKey,
	failedColumn: "thumb_failed_at",
	process:      thumbnailFile,
}

// thumbnailFile resolves an archived image to its archived Slack thumbnail, archiving
// it on first sight. Thumbnails of images that get a converted copy are re-encoded as
// JPEG, since Slack makes them PNG.
func thumbnailFile(deps filePassDeps, file pendingMessageFile) (map[string]interface{}, error) {
	var thumbFileId *string
	var thumbWidth, thumbHeight *int
	err := deps.db.QueryRow(context.Background(), "SELECT thumb_file_id, thumb_width, thumb_height FROM file WHERE public_id = $1", file.slackFileId).Scan(&thumbFileId, &thumbWidth, &thumbHeight)
	if err != nil {
		return nil, fmt.Errorf("failed to check file thumbnail: %w", err)
	}
	if thumbFileId != nil {
		return thumbFile{*thumbFileId, *thumbWidth, *thumbHeight}.fields(), nil
	}

	thumbUrl, _ := file.object[slackThumbKey].(string)
	data, err := downloadSlackThumb(deps, thumbUrl)
	if err != nil {
		return nil, err
	}

	// Decoding also catches a non-image body (e.g. a login page on a bad token),
	// which is left to retry, as it's likely fixed by then.
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("thumbnail isn't a known image: %w", err)
	}
	thumb, width, height := data, config.Width, config.Height
	if _, converted := imageDecoders[file.mimetype]; converted && format != "jpeg" {
		thumb, width, height, err = encodeJpeg(thumb, decodeImage)
		if err != nil {
			return nil, fmt.Errorf("failed to re-encode thumbnail: %w", err)
		}
		format = "jpeg"
	}

	uploadedId, err := UploadToSeaweedFS(deps.seaweedMasterUrl, thumb, thumbName(file.name, file.slackFileId, format))
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	_, err = deps.db.Exec(context.Background(), "UPDATE file SET thumb_file_id = $2, thumb_width = $3, thumb_height = $4 WHERE public_id = $1", file.slackFileId, uploadedId, width, height)
	if err != nil {
		return nil, fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Archived thumbnail of file '%s' (%s %dx%d)\n", file.slackFileId, format, width, height)
	return thumbFile{uploadedId, width, height}.fields(), nil
}

// downloadSlackThumb downloads a Slack thumbnail, which is errFileUnprocessable when
// it's gone.
func downloadSlackThumb(deps filePassDeps, thumbUrl string) ([]byte, error) {
	data, err := deps.slackClient.Download(context.Background(), thumbUrl)
	if errors.Is(err, slackapi.ErrGone) {
		return nil, fmt.Errorf("%w: thumbnail is %v", errFileUnprocessable, err)
	}
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	return data, nil
}

func decodeImage(r io.Reader) (image.Image, error) {
	img, _, err := image.Decode(r)
	return img, err
}

// thumbName names a thumbnail after its original, with the extension of its own
// format, so SeaweedFS serves it with the right content type.
func thumbName(name, fallback, format string) string {
	if name == "" {
		name = fallback
	}
	ext := "." + format
	if format == "jpeg" {
		ext = ".jpg"
	}
	return strings.TrimSuffix(name, path.Ext(name)) + "_960" + ext
}
