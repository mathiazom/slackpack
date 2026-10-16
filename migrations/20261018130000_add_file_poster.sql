ALTER TABLE file
    ADD COLUMN poster_file_id   TEXT,
    ADD COLUMN poster_width     INTEGER,
    ADD COLUMN poster_height    INTEGER,
    ADD COLUMN poster_failed_at TIMESTAMP,
    ADD CHECK (poster_file_id IS NULL OR poster_failed_at IS NULL),
    ADD CHECK ((poster_file_id IS NULL) = (poster_width IS NULL) AND (poster_file_id IS NULL) = (poster_height IS NULL));
