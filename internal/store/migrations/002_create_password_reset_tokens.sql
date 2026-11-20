CREATE TABLE IF NOT EXISTS password_reset_tokens (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS password_reset_tokens_expiry_idx
    ON password_reset_tokens (expires_at);
