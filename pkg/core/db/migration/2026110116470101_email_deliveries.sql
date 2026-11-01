-- +goose Up
-- One row per recipient of every email the platform sends (ADR 090 §3): did
-- the relay accept it. Never the body, the subject or the plain address — the
-- recipient is an HMAC an admin can match by typing an exact address, plus a
-- masked form for display. Kept 90 days, then purged by the lifecycle sweeper.
CREATE TABLE core_email_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- NULL only for platform mail that belongs to no tenant.
    tenant_id TEXT,
    kind TEXT NOT NULL,
    origin_type TEXT,
    origin_id TEXT,
    account TEXT NOT NULL,
    recipient_hmac BYTEA NOT NULL,
    recipient_masked TEXT NOT NULL,
    recipient_role TEXT NOT NULL CHECK (recipient_role IN ('to', 'cc', 'bcc')),
    status TEXT NOT NULL CHECK (status IN ('accepted', 'rejected', 'failed')),
    smtp_code INTEGER,
    error_class TEXT,
    message_id TEXT
);

CREATE INDEX idx_core_email_deliveries_tenant_created
    ON core_email_deliveries (tenant_id, created_at DESC);
CREATE INDEX idx_core_email_deliveries_tenant_recipient
    ON core_email_deliveries (tenant_id, recipient_hmac);
CREATE INDEX idx_core_email_deliveries_created
    ON core_email_deliveries (created_at);

-- +goose Down
DROP TABLE IF EXISTS core_email_deliveries;
