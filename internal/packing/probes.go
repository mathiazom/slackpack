package packing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/abema/go-mp4"
	. "github.com/mathiazom/slackpack/internal/seaweedfs"
)

// probedMimetypes are the video types whose archived original gets its video track
// probed, since Slack doesn't give video dimensions (original_w/original_h are 0).
// Slack labels iPhone .MOV files video/mp4.
var probedMimetypes = []string{"video/mp4", "video/quicktime"}

// Keys a probed video's display dimensions (rotation applied) and codec (its sample
// entry, e.g. avc1 or hvc1) are embedded under, next to archived_file_id in the
// file's JSON object.
const (
	videoWidthKey  = "archived_video_w"
	videoHeightKey = "archived_video_h"
	videoCodecKey  = "archived_video_codec"
)

type probedVideo struct {
	width  int
	height int
	codec  string
}

func (p probedVideo) fields() map[string]interface{} {
	return map[string]interface{}{
		videoWidthKey:  p.width,
		videoHeightKey: p.height,
		videoCodecKey:  p.codec,
	}
}

// probePass records the video track of every archived MP4/MOV message file.
var probePass = filePass{
	name:         "probe",
	mimetypes:    probedMimetypes,
	embeddedKey:  videoCodecKey,
	failedColumn: "probe_failed_at",
	process:      probeFile,
}

// probeFile resolves an archived video to its video track, probing it on first sight.
func probeFile(deps filePassDeps, file pendingMessageFile) (map[string]interface{}, error) {
	var width, height *int
	var codec *string
	err := deps.db.QueryRow(context.Background(), "SELECT probed_width, probed_height, probed_codec FROM file WHERE public_id = $1", file.slackFileId).Scan(&width, &height, &codec)
	if err != nil {
		return nil, fmt.Errorf("failed to check file probe: %w", err)
	}
	if codec != nil {
		return probedVideo{*width, *height, *codec}.fields(), nil
	}

	original, err := DownloadFromSeaweedFS(deps.seaweedMasterUrl, file.archivedFileId)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}

	video, err := probeVideoTrack(bytes.NewReader(original))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFileUnprocessable, err)
	}

	_, err = deps.db.Exec(context.Background(), "UPDATE file SET probed_width = $2, probed_height = $3, probed_codec = $4 WHERE public_id = $1", file.slackFileId, video.width, video.height, video.codec)
	if err != nil {
		return nil, fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Probed file '%s' (%s %dx%d)\n", file.slackFileId, video.codec, video.width, video.height)
	return video.fields(), nil
}

// probeVideoTrack reads the first video track of an MP4/MOV: its display dimensions,
// swapped when the track matrix rotates by 90° (as phones record portrait video), and
// its codec.
func probeVideoTrack(r io.ReadSeeker) (probedVideo, error) {
	traks, err := mp4.ExtractBox(r, nil, mp4.BoxPath{mp4.BoxTypeMoov(), mp4.BoxTypeTrak()})
	if err != nil {
		return probedVideo{}, err
	}
	for _, trak := range traks {
		boxes, err := mp4.ExtractBoxesWithPayload(r, trak, []mp4.BoxPath{
			{mp4.BoxTypeTkhd()},
			{mp4.BoxTypeMdia(), mp4.BoxTypeHdlr()},
			{mp4.BoxTypeMdia(), mp4.BoxTypeMinf(), mp4.BoxTypeStbl(), mp4.BoxTypeStsd(), mp4.BoxTypeAny()},
		})
		if err != nil {
			return probedVideo{}, err
		}
		var tkhd *mp4.Tkhd
		var isVideo bool
		var codec string
		for _, box := range boxes {
			switch payload := box.Payload.(type) {
			case *mp4.Tkhd:
				tkhd = payload
			case *mp4.Hdlr:
				isVideo = string(payload.HandlerType[:]) == "vide"
			default:
				if codec == "" && box.Info.Type != mp4.BoxTypeTkhd() && box.Info.Type != mp4.BoxTypeHdlr() {
					codec = box.Info.Type.String()
				}
			}
		}
		if !isVideo || tkhd == nil || codec == "" {
			continue
		}
		width, height := int(tkhd.GetWidthInt()), int(tkhd.GetHeightInt())
		if width <= 0 || height <= 0 {
			return probedVideo{}, fmt.Errorf("video track has no dimensions")
		}
		// A 90° or 270° rotation zeroes the matrix diagonal.
		if tkhd.Matrix[0] == 0 && tkhd.Matrix[4] == 0 {
			width, height = height, width
		}
		return probedVideo{width, height, codec}, nil
	}
	return probedVideo{}, errors.New("no video track")
}
