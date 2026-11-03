-- +goose Up
CREATE TABLE links (
    code VARCHAR(7) PRIMARY KEY,
    url TEXT NOT NULL,
    clicks BIGINT NOT NULL DEFAULT 0 CHECK (clicks >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE links;
