CREATE TABLE channel
(
    id        SERIAL PRIMARY KEY,
    public_id VARCHAR(255) NOT NULL,
    timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    data      JSONB,
    data_hash BIGINT GENERATED ALWAYS AS (jsonb_hash_extended(data, 0)) STORED,
    UNIQUE(public_id, data_hash),
    UNIQUE(public_id, timestamp)
);