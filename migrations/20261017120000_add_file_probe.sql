ALTER TABLE file
    ADD COLUMN probed_width    INTEGER,
    ADD COLUMN probed_height   INTEGER,
    ADD COLUMN probed_codec    TEXT,
    ADD COLUMN probe_failed_at TIMESTAMP,
    ADD CHECK (probed_codec IS NULL OR probe_failed_at IS NULL),
    ADD CHECK ((probed_codec IS NULL) = (probed_width IS NULL) AND (probed_codec IS NULL) = (probed_height IS NULL));
