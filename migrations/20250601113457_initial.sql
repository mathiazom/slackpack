CREATE TABLE channel
(
    id        SERIAL PRIMARY KEY,
    public_id VARCHAR(255) NOT NULL,
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    data      JSONB,
    UNIQUE(public_id, timestamp)
);

CREATE TABLE "user"
(
    id        SERIAL PRIMARY KEY,
    public_id VARCHAR(255) NOT NULL,
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    data      JSONB,
    UNIQUE(public_id, timestamp)
);

CREATE TABLE message
(
    id         SERIAL PRIMARY KEY,
    public_id  VARCHAR(255) UNIQUE NOT NULL,
    channel_id INTEGER NOT NULL,
    data       JSONB,
    FOREIGN KEY (channel_id) REFERENCES channel(id) ON DELETE RESTRICT
);

CREATE TABLE emoji
(
    id         SERIAL PRIMARY KEY,
    public_id  VARCHAR(255) NOT NULL,
    timestamp  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    slack_url  TEXT NOT NULL,
    file_id    TEXT NOT NULL,
    UNIQUE(public_id, timestamp)
);