CREATE TABLE message
(
    id         SERIAL PRIMARY KEY,
    public_id  VARCHAR(255) UNIQUE NOT NULL,
    channel_id INTEGER NOT NULL,
    data       JSONB,
    FOREIGN KEY (channel_id) REFERENCES channel(id) ON DELETE RESTRICT
)