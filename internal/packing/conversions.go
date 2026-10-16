package packing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
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

// errConversionFailed signals an original that can't be decoded, which will never
// succeed, as opposed to a transient failure that should be retried on a later run.
var errConversionFailed = errors.New("conversion failed")

type convertedFile struct {
	fileId string
	width  int
	height int
}

func (c convertedFile) embed(file map[string]interface{}) {
	file[convertedFileIdKey] = c.fileId
	file[convertedWidthKey] = c.width
	file[convertedHeightKey] = c.height
}

func convertibleMimetypes() []string {
	mimetypes := make([]string, 0, len(imageDecoders))
	for mimetype := range imageDecoders {
		mimetypes = append(mimetypes, mimetype)
	}
	return mimetypes
}

// pendingFilesQuery selects every archived message file of a convertible type that has
// no converted pointer yet, skipping files whose conversion failed permanently.
const pendingFilesQuery = `
	SELECT m.id, (f.idx - 1)::int, f.value->>'id', f.value->>'archived_file_id', coalesce(f.value->>'name', ''), lower(f.value->>'mimetype')
	FROM message m
	CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(m.data->'files') = 'array' THEN m.data->'files' ELSE '[]' END) WITH ORDINALITY AS f(value, idx)
	LEFT JOIN file failed ON failed.public_id = f.value->>'id' AND failed.conversion_failed_at IS NOT NULL
	WHERE f.value ? 'archived_file_id'
	  AND NOT (f.value ? '` + convertedFileIdKey + `')
	  AND lower(f.value->>'mimetype') = ANY($1)
	  AND failed.id IS NULL
	ORDER BY m.id
`

// embedConvertedFileQuery merges a converted copy's pointer and dimensions into one
// file object of a message's JSON.
const embedConvertedFileQuery = `UPDATE message SET data = jsonb_set(data, ARRAY['files', $2::int::text], data->'files'->$2::int || jsonb_build_object($3::text, $4::text, $5::text, $6::int, $7::text, $8::int)) WHERE id = $1`

// convertMessageFiles archives a JPEG copy of every archived message file in a format
// browsers can't show, embedding the pointer into the message JSON in place. This
// backfills messages no longer in Slack too, since conversion reads the archived
// original. Files already recorded as permanently failed are skipped.
func convertMessageFiles(db *pgx.Conn, seaweedMasterUrl string) {
	type pendingFile struct {
		messageId      int
		index          int
		slackFileId    string
		archivedFileId string
		name           string
		mimetype       string
	}
	rows, err := db.Query(context.Background(), pendingFilesQuery, convertibleMimetypes())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to query message files to convert: %v\n", err)
		return
	}
	files, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pendingFile, error) {
		var f pendingFile
		err := row.Scan(&f.messageId, &f.index, &f.slackFileId, &f.archivedFileId, &f.name, &f.mimetype)
		return f, err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read message files to convert: %v\n", err)
		return
	}

	count := 0
	for _, f := range files {
		converted, err := convertFile(db, seaweedMasterUrl, f.slackFileId, f.archivedFileId, f.name, f.mimetype)
		if err != nil {
			if !errors.Is(err, errConversionFailed) {
				fmt.Fprintf(os.Stderr, "failed to convert file '%s': %v\n", f.slackFileId, err)
			}
			continue
		}
		_, err = db.Exec(context.Background(), embedConvertedFileQuery,
			f.messageId, f.index, convertedFileIdKey, converted.fileId, convertedWidthKey, converted.width, convertedHeightKey, converted.height)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to embed converted file in message %d: %v\n", f.messageId, err)
			continue
		}
		count++
	}

	if count > 0 {
		fmt.Printf("Embedded %d converted files in messages\n", count)
	}
}

// convertFile resolves an archived file to its archived JPEG copy, converting it on
// first sight. Returns errConversionFailed when the original can't be decoded.
func convertFile(db *pgx.Conn, seaweedMasterUrl, slackFileId, archivedFileId, name, mimetype string) (convertedFile, error) {
	var converted convertedFile
	var convertedFileId *string
	var convertedWidth, convertedHeight *int
	err := db.QueryRow(context.Background(), "SELECT converted_file_id, converted_width, converted_height FROM file WHERE public_id = $1", slackFileId).Scan(&convertedFileId, &convertedWidth, &convertedHeight)
	if err != nil {
		return converted, fmt.Errorf("failed to check file conversion: %w", err)
	}
	if convertedFileId != nil {
		return convertedFile{*convertedFileId, *convertedWidth, *convertedHeight}, nil
	}

	original, err := DownloadFromSeaweedFS(seaweedMasterUrl, archivedFileId)
	if err != nil {
		return converted, fmt.Errorf("download failed: %w", err)
	}

	data, width, height, err := encodeJpeg(original, imageDecoders[mimetype])
	if err != nil {
		_, dbErr := db.Exec(context.Background(), "UPDATE file SET conversion_failed_at = CURRENT_TIMESTAMP WHERE public_id = $1", slackFileId)
		if dbErr != nil {
			return converted, fmt.Errorf("failed to record failed conversion: %w", dbErr)
		}
		fmt.Fprintf(os.Stderr, "file '%s' (%s) can't be converted, will not retry: %v\n", slackFileId, mimetype, err)
		return converted, fmt.Errorf("%w: %v", errConversionFailed, err)
	}

	uploadedId, err := UploadToSeaweedFS(seaweedMasterUrl, data, jpegName(name, slackFileId))
	if err != nil {
		return converted, fmt.Errorf("upload failed: %w", err)
	}

	_, err = db.Exec(context.Background(), "UPDATE file SET converted_file_id = $2, converted_width = $3, converted_height = $4 WHERE public_id = $1", slackFileId, uploadedId, width, height)
	if err != nil {
		return converted, fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Converted file '%s' (%s)\n", slackFileId, mimetype)
	return convertedFile{uploadedId, width, height}, nil
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

func jpegName(name, fallback string) string {
	if name == "" {
		name = fallback
	}
	return strings.TrimSuffix(name, path.Ext(name)) + ".jpg"
}
