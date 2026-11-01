-- name: InsertEmailDelivery :exec
INSERT INTO core_email_deliveries (
    tenant_id, kind, origin_type, origin_id, account,
    recipient_hmac, recipient_masked, recipient_role,
    status, smtp_code, error_class, message_id
) VALUES (
    sqlc.narg(tenant_id), @kind, sqlc.narg(origin_type), sqlc.narg(origin_id), @account,
    @recipient_hmac, @recipient_masked, @recipient_role,
    @status, sqlc.narg(smtp_code), sqlc.narg(error_class), sqlc.narg(message_id)
);

-- name: PurgeEmailDeliveriesBefore :execrows
DELETE FROM core_email_deliveries
WHERE created_at < @cutoff;

-- name: ListEmailDeliveries :many
-- One tenant's rows, newest first. A NULL filter matches everything.
SELECT * FROM core_email_deliveries
WHERE tenant_id = @tenant_id
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind)::text)
  AND (sqlc.narg(sent_from)::timestamptz IS NULL OR created_at >= sqlc.narg(sent_from)::timestamptz)
  AND (sqlc.narg(sent_to)::timestamptz IS NULL OR created_at < sqlc.narg(sent_to)::timestamptz)
  AND (sqlc.narg(recipient_hmac)::bytea IS NULL OR recipient_hmac = sqlc.narg(recipient_hmac)::bytea)
ORDER BY created_at DESC, id
LIMIT @row_limit OFFSET @row_offset;

-- name: SummarizeEmailDeliveries :many
-- Every tenant, counts only.
SELECT kind, status, count(*)::bigint AS count
FROM core_email_deliveries
WHERE created_at >= @sent_from AND created_at < @sent_to
GROUP BY kind, status
ORDER BY kind, status;

-- name: ListEmailDeliveriesForRecipient :many
-- One person's rows in one tenant (a NULL tenant is platform mail), for an export.
SELECT * FROM core_email_deliveries
WHERE tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::text
  AND recipient_hmac = @recipient_hmac
ORDER BY created_at DESC;

-- name: DeleteEmailDeliveriesForRecipient :execrows
-- Erases one person's rows in one tenant (a NULL tenant is platform mail).
DELETE FROM core_email_deliveries
WHERE tenant_id IS NOT DISTINCT FROM sqlc.narg(tenant_id)::text
  AND recipient_hmac = @recipient_hmac;
