package packing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"net/url"

	. "github.com/mathiazom/slackpack/internal/seaweedfs"
	"github.com/rusq/slack"
	"golang.org/x/image/draw"
)

// posterMimetypes are the video types slackback embeds, whose Slack poster frame
// (thumb_video) is archived so slackopy can show it without loading the video.
var posterMimetypes = []string{"video/mp4", "video/quicktime", "video/webm"}

// Keys a poster's pointer and dimensions are embedded under, next to
// archived_file_id in the file's JSON object.
const (
	posterFileIdKey = "archived_poster_file_id"
	posterWidthKey  = "archived_poster_w"
	posterHeightKey = "archived_poster_h"
)

type posterFile struct {
	fileId string
	width  int
	height int
}

func (p posterFile) fields() map[string]interface{} {
	return map[string]interface{}{
		posterFileIdKey: p.fileId,
		posterWidthKey:  p.width,
		posterHeightKey: p.height,
	}
}

// posterPass archives Slack's poster frame of every archived video. Like the
// thumbnail pass it reads from Slack, so it only backfills files still there; a
// poster that's gone, or that Slack never made, is never retried.
var posterPass = filePass{
	name:         "poster",
	mimetypes:    posterMimetypes,
	embeddedKey:  posterFileIdKey,
	failedColumn: "poster_failed_at",
	process:      posterFileOf,
}

// posterFileOf resolves an archived video to its archived poster, archiving it on
// first sight, scaled to fit thumbMaxSize like the image thumbnails.
func posterFileOf(deps filePassDeps, file pendingMessageFile) (map[string]interface{}, error) {
	var posterFileId *string
	var posterWidth, posterHeight *int
	err := deps.db.QueryRow(context.Background(), "SELECT poster_file_id, poster_width, poster_height FROM file WHERE public_id = $1", file.slackFileId).Scan(&posterFileId, &posterWidth, &posterHeight)
	if err != nil {
		return nil, fmt.Errorf("failed to check file poster: %w", err)
	}
	if posterFileId != nil {
		return posterFile{*posterFileId, *posterWidth, *posterHeight}.fields(), nil
	}

	posterUrl, err := fetchSlackThumbVideoUrl(deps, file.slackFileId)
	if err != nil {
		return nil, err
	}
	data, err := downloadSlackThumb(deps, posterUrl)
	if err != nil {
		return nil, err
	}

	// As for thumbnails, a non-image body is left to retry.
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("poster isn't a known image: %w", err)
	}
	poster, width, height, err := encodeImageJpeg(scaleToFit(img, thumbMaxSize))
	if err != nil {
		return nil, fmt.Errorf("failed to encode poster: %w", err)
	}

	uploadedId, err := UploadToSeaweedFS(deps.seaweedMasterUrl, poster, jpegName(file.name, file.slackFileId))
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	_, err = deps.db.Exec(context.Background(), "UPDATE file SET poster_file_id = $2, poster_width = $3, poster_height = $4 WHERE public_id = $1", file.slackFileId, uploadedId, width, height)
	if err != nil {
		return nil, fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Archived poster of file '%s' (%dx%d)\n", file.slackFileId, width, height)
	return posterFile{uploadedId, width, height}.fields(), nil
}

// fetchSlackThumbVideoUrl looks up a video's thumb_video with a raw files.info call,
// as slackdump's typed File has no such field and drops it from the message JSON.
// It's errFileUnprocessable when the file is gone from Slack or has no poster.
func fetchSlackThumbVideoUrl(deps filePassDeps, slackFileId string) (string, error) {
	client, err := deps.slackAuth.HTTPClient()
	if err != nil {
		return "", fmt.Errorf("failed to create Slack HTTP client: %w", err)
	}
	resp, err := client.PostForm(slack.APIURL+"files.info", url.Values{
		"token": {deps.slackAuth.SlackToken()},
		"file":  {slackFileId},
	})
	if err != nil {
		return "", fmt.Errorf("files.info failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("files.info failed with status: %d", resp.StatusCode)
	}

	var info struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
		File  struct {
			ThumbVideo string `json:"thumb_video"`
		} `json:"file"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("failed to parse files.info response: %w", err)
	}
	switch {
	// hidden_by_limit is a free plan's history limit, lifted only by upgrading.
	case info.Error == "file_not_found" || info.Error == "file_deleted" || info.Error == "hidden_by_limit":
		return "", fmt.Errorf("%w: file is gone: %s", errFileUnprocessable, info.Error)
	case !info.Ok:
		return "", fmt.Errorf("files.info failed: %s", info.Error)
	case info.File.ThumbVideo == "":
		return "", fmt.Errorf("%w: Slack has no poster for it", errFileUnprocessable)
	}
	return info.File.ThumbVideo, nil
}

// scaleToFit scales an image down so its longest side is at most maxSize.
func scaleToFit(img image.Image, maxSize int) image.Image {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxSize && height <= maxSize {
		return img
	}
	if width >= height {
		width, height = maxSize, max(1, height*maxSize/width)
	} else {
		width, height = max(1, width*maxSize/height), maxSize
	}
	scaled := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), img, bounds, draw.Src, nil)
	return scaled
}
