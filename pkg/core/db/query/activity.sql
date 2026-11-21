-- name: InsertActivity :exec
-- The actor's name is kept as it was when they acted.
INSERT INTO core_activity (tenant_id, actor_id, actor_name, via, key_id, action,
                           target_kind, target_id, scope_kind, scope_id, changes, request_id)
VALUES (sqlc.narg(tenant_id), sqlc.narg(actor_id)::varchar,
        (SELECT COALESCE(NULLIF(u.profile ->> 'name', ''), u.email) FROM core_users u WHERE u.id = sqlc.narg(actor_id)::varchar),
        @via, sqlc.narg(key_id), @action, @target_kind, @target_id,
        sqlc.narg(scope_kind), sqlc.narg(scope_id), sqlc.narg(changes), sqlc.narg(request_id));

-- name: ListActivity :many
-- One target's or one scope's activity, or the tenant's (admin), newest first;
-- before is the last row of the previous page.
SELECT a.id, a.occurred_at, a.actor_id, a.actor_name, a.via, a.key_id, a.action,
       a.target_kind, a.target_id, a.scope_kind, a.scope_id, a.changes
FROM core_activity a
WHERE a.tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)
  AND (sqlc.narg(target_kind)::text IS NULL OR (a.target_kind = sqlc.narg(target_kind) AND a.target_id = sqlc.narg(target_id)))
  AND (sqlc.narg(scope_kind)::text IS NULL OR (a.scope_kind = sqlc.narg(scope_kind) AND a.scope_id = sqlc.narg(scope_id)))
  AND (sqlc.narg(kind)::text IS NULL OR a.target_kind = sqlc.narg(kind))
  AND (sqlc.narg(action)::text IS NULL OR a.action = sqlc.narg(action))
  AND (sqlc.narg(actor_id)::text IS NULL OR a.actor_id = sqlc.narg(actor_id))
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR a.occurred_at >= sqlc.narg(from_at))
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR a.occurred_at < sqlc.narg(to_at))
  AND (sqlc.narg(before)::uuid IS NULL OR (a.occurred_at, a.id) <
       (SELECT b.occurred_at, b.id FROM core_activity b WHERE b.tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id) AND b.id = sqlc.narg(before)))
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT @row_limit OFFSET @row_offset;

-- name: ListActivityByActor :many
-- What one person did, in every tenant: their data export.
SELECT COALESCE(tenant_id, '')::text AS tenant_id, occurred_at, via, action, target_kind, target_id
FROM core_activity
WHERE actor_id = @actor_id
ORDER BY occurred_at DESC;

-- name: AllowActivityMaintenance :exec
-- Lets this transaction touch core_activity rows (erasure only).
SELECT set_config('core.activity_maintenance', 'on', true);

-- name: ForgetActivityActor :execrows
-- Erasure: the rows stay, who acted goes.
UPDATE core_activity SET actor_id = NULL, actor_name = NULL
WHERE actor_id = @actor_id;
