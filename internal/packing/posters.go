package packing

import (
	"bytes"
	"context"
	"fmt"
	"image"

	. "github.com/mathiazom/slackpack/internal/seaweedfs"
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

// slackPosterKey is Slack's poster frame of a video, at the video's resolution.
const slackPosterKey = "thumb_video"

// posterPass archives Slack's poster frame of every archived video that has one. Like
// the thumbnail pass it reads from Slack, so it only backfills files still there; a
// poster that's gone is never retried.
var posterPass = filePass{
	name:         "poster",
	mimetypes:    posterMimetypes,
	where:        "coalesce(f.value->>'" + slackPosterKey + "', '') <> ''",
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

	posterUrl, _ := file.object[slackPosterKey].(string)
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
