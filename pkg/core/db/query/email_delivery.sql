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
