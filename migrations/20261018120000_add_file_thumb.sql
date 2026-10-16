ALTER TABLE file
    ADD COLUMN thumb_file_id   TEXT,
    ADD COLUMN thumb_width     INTEGER,
    ADD COLUMN thumb_height    INTEGER,
    ADD COLUMN thumb_failed_at TIMESTAMP,
    ADD CHECK (thumb_file_id IS NULL OR thumb_failed_at IS NULL),
    ADD CHECK ((thumb_file_id IS NULL) = (thumb_width IS NULL) AND (thumb_file_id IS NULL) = (thumb_height IS NULL));
