CREATE TABLE emoji
(
    id         SERIAL PRIMARY KEY,
    public_id  VARCHAR(255) NOT NULL,
    timestamp  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    slack_url  TEXT NOT NULL,
    file_id    TEXT NOT NULL,
    UNIQUE(public_id, slack_url),
    UNIQUE(public_id, timestamp)
)