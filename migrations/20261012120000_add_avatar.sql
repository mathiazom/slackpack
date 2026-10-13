CREATE TABLE avatar
(
    id        SERIAL PRIMARY KEY,
    slack_url TEXT UNIQUE NOT NULL,
    file_id   TEXT,
    failed_at TIMESTAMP,
    CHECK ((file_id IS NULL) <> (failed_at IS NULL))
);
