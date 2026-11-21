-- +goose Up
-- API token events are in core_activity now (office#200); the old rows are
-- not copied.
DROP TABLE IF EXISTS core_api_token_audit_logs;

-- +goose Down
CREATE TABLE core_api_token_audit_logs (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    token_id uuid NOT NULL REFERENCES core_api_tokens(id) ON DELETE CASCADE,
    action VARCHAR(50) NOT NULL,
    ip_address VARCHAR(45) NULL,
    user_agent TEXT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    additional_data JSONB NULL,
    CONSTRAINT api_token_audit_logs_pk PRIMARY KEY (id)
);
CREATE INDEX idx_api_token_audit_logs_token_id ON core_api_token_audit_logs (token_id);
CREATE INDEX idx_api_token_audit_logs_timestamp ON core_api_token_audit_logs (timestamp);
