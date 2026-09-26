CREATE TABLE file
(
    id        SERIAL PRIMARY KEY,
    public_id VARCHAR(255) UNIQUE NOT NULL,
    slack_url TEXT NOT NULL,
    file_id   TEXT NOT NULL
);
