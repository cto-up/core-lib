-- +goose Up
-- +goose StatementBegin
-- One activity log for every app (office#199): who did what, through which
-- door, recorded by each app in the change's own transaction. Append-only:
-- only erasure (core.activity_maintenance = on, set in its transaction) may
-- touch a row.
CREATE TABLE core_activity (
    id          UUID NOT NULL DEFAULT gen_random_uuid(),
    tenant_id   VARCHAR(64) NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    actor_id    VARCHAR,
    actor_name  VARCHAR(200),
    via         VARCHAR(16) NOT NULL,
    key_id      VARCHAR(64),
    action      VARCHAR(64) NOT NULL,
    target_kind VARCHAR(32) NOT NULL,
    target_id   VARCHAR(64) NOT NULL,
    scope_kind  VARCHAR(32),
    scope_id    VARCHAR(64),
    changes     JSONB,
    request_id  VARCHAR(64),
    PRIMARY KEY (id),
    CONSTRAINT core_activity_scope CHECK ((scope_kind IS NULL) = (scope_id IS NULL))
);

CREATE INDEX core_activity_target_idx ON core_activity (tenant_id, target_kind, target_id, occurred_at DESC, id DESC);
CREATE INDEX core_activity_scope_idx ON core_activity (tenant_id, scope_kind, scope_id, occurred_at DESC, id DESC) WHERE scope_kind IS NOT NULL;
CREATE INDEX core_activity_tenant_idx ON core_activity (tenant_id, occurred_at DESC, id DESC);
CREATE INDEX core_activity_actor_idx ON core_activity (actor_id) WHERE actor_id IS NOT NULL;

CREATE FUNCTION core_activity_append_only() RETURNS trigger AS $$
BEGIN
    IF current_setting('core.activity_maintenance', true) = 'on' THEN
        RETURN COALESCE(NEW, OLD);
    END IF;
    RAISE EXCEPTION 'core_activity is append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER core_activity_append_only
    BEFORE UPDATE OR DELETE ON core_activity
    FOR EACH ROW EXECUTE FUNCTION core_activity_append_only();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS core_activity;
DROP FUNCTION IF EXISTS core_activity_append_only();
-- +goose StatementEnd
