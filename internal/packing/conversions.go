package packing

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"path"
	"strings"

	"github.com/gen2brain/heic"
	"github.com/jackc/pgx/v5"
	. "github.com/mathiazom/slackpack/internal/seaweedfs"
	"golang.org/x/image/tiff"
)

// imageDecoders maps the image types most browsers can't show (all but TIFF work in
// Safari) to a decoder, so a JPEG copy can be archived next to the original for
// display. A HEIC sequence (e.g. a burst) decodes to its first frame.
var imageDecoders = map[string]func(io.Reader) (image.Image, error){
	"image/heic":          heic.Decode,
	"image/heif":          heic.Decode,
	"image/heic-sequence": heic.Decode,
	"image/heif-sequence": heic.Decode,
	"image/tiff":          tiff.Decode,
}

// Keys a converted copy's pointer and dimensions are embedded under, next to
// archived_file_id in the file's JSON object. The dimensions are the decoded
// image's (orientation applied), as Slack's original_w/original_h may not be.
const (
	convertedFileIdKey = "archived_converted_file_id"
	convertedWidthKey  = "archived_converted_w"
	convertedHeightKey = "archived_converted_h"
)

const convertedJpegQuality = 90

type convertedFile struct {
	fileId string
	width  int
	height int
}

func (c convertedFile) fields() map[string]interface{} {
	return map[string]interface{}{
		convertedFileIdKey: c.fileId,
		convertedWidthKey:  c.width,
		convertedHeightKey: c.height,
	}
}

// conversionPass archives a JPEG copy of every archived message file in a format
// browsers can't show.
var conversionPass = filePass{
	name:         "convert",
	mimetypes:    mapKeys(imageDecoders),
	embeddedKey:  convertedFileIdKey,
	failedColumn: "conversion_failed_at",
	process:      convertFile,
}

// convertFile resolves an archived file to its archived JPEG copy, converting it on
// first sight.
func convertFile(db *pgx.Conn, seaweedMasterUrl string, file pendingMessageFile) (map[string]interface{}, error) {
	var convertedFileId *string
	var convertedWidth, convertedHeight *int
	err := db.QueryRow(context.Background(), "SELECT converted_file_id, converted_width, converted_height FROM file WHERE public_id = $1", file.slackFileId).Scan(&convertedFileId, &convertedWidth, &convertedHeight)
	if err != nil {
		return nil, fmt.Errorf("failed to check file conversion: %w", err)
	}
	if convertedFileId != nil {
		return convertedFile{*convertedFileId, *convertedWidth, *convertedHeight}.fields(), nil
	}

	original, err := DownloadFromSeaweedFS(seaweedMasterUrl, file.archivedFileId)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}

	data, width, height, err := encodeJpeg(original, imageDecoders[file.mimetype])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errFileUnprocessable, err)
	}

	uploadedId, err := UploadToSeaweedFS(seaweedMasterUrl, data, jpegName(file.name, file.slackFileId))
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	_, err = db.Exec(context.Background(), "UPDATE file SET converted_file_id = $2, converted_width = $3, converted_height = $4 WHERE public_id = $1", file.slackFileId, uploadedId, width, height)
	if err != nil {
		return nil, fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Converted file '%s' (%s)\n", file.slackFileId, file.mimetype)
	return convertedFile{uploadedId, width, height}.fields(), nil
}

// encodeJpeg decodes an image and re-encodes it as JPEG, flattening any transparency
// onto white (JPEG has no alpha, and would otherwise turn it black).
func encodeJpeg(original []byte, decode func(io.Reader) (image.Image, error)) ([]byte, int, int, error) {
	img, err := decode(bytes.NewReader(original))
	if err != nil {
		return nil, 0, 0, err
	}

	if opaque, ok := img.(interface{ Opaque() bool }); !ok || !opaque.Opaque() {
		flattened := image.NewRGBA(img.Bounds())
		draw.Draw(flattened, flattened.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(flattened, flattened.Bounds(), img, img.Bounds().Min, draw.Over)
		img = flattened
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: convertedJpegQuality}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), img.Bounds().Dx(), img.Bounds().Dy(), nil
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func jpegName(name, fallback string) string {
	if name == "" {
		name = fallback
	}
	return strings.TrimSuffix(name, path.Ext(name)) + ".jpg"
}
