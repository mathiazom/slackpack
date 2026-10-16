ALTER TABLE file
    ADD COLUMN converted_file_id    TEXT,
    ADD COLUMN converted_width      INTEGER,
    ADD COLUMN converted_height     INTEGER,
    ADD COLUMN conversion_failed_at TIMESTAMP,
    ADD CHECK (converted_file_id IS NULL OR conversion_failed_at IS NULL),
    ADD CHECK ((converted_file_id IS NULL) = (converted_width IS NULL) AND (converted_file_id IS NULL) = (converted_height IS NULL));
