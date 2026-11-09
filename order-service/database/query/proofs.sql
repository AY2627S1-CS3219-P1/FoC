-- name: ProofServerTime :one
SELECT clock_timestamp()::timestamptz;

-- name: FindProofUpload :one
SELECT * FROM order_proof_uploads WHERE order_id=$1 AND courier_id=$2 AND upload_id=$3;

-- name: GetProofUpload :one
SELECT * FROM order_proof_uploads WHERE id=$1;

-- name: ProofIdentityFenced :one
SELECT EXISTS(SELECT 1 FROM order_proof_fences WHERE order_id=$1 AND courier_id=$2 AND upload_id=$3);

-- name: CountStagedProofs :one
SELECT count(*) FROM order_proof_uploads WHERE order_id=$1 AND courier_id=$2 AND state='uploading';

-- name: SaveProofUpload :execrows
INSERT INTO order_proof_uploads(id,order_id,courier_id,upload_id,object_key,content_type,size_bytes,state,generation,signing_epoch,granted_at,expires_at,created_at,upload_scope)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT(id) DO UPDATE SET generation=EXCLUDED.generation,signing_epoch=EXCLUDED.signing_epoch,granted_at=EXCLUDED.granted_at,expires_at=EXCLUDED.expires_at
WHERE order_proof_uploads.state='uploading' AND EXCLUDED.state='uploading'
AND order_proof_uploads.order_id=EXCLUDED.order_id AND order_proof_uploads.courier_id=EXCLUDED.courier_id
AND order_proof_uploads.upload_scope=EXCLUDED.upload_scope AND order_proof_uploads.upload_id=EXCLUDED.upload_id AND order_proof_uploads.object_key=EXCLUDED.object_key
AND order_proof_uploads.content_type=EXCLUDED.content_type AND order_proof_uploads.size_bytes=EXCLUDED.size_bytes;

-- name: FenceProofIdentity :exec
INSERT INTO order_proof_fences(order_id,courier_id,upload_id,retired_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING;

-- name: RetireProofUpload :execrows
UPDATE order_proof_uploads SET state='deleting' WHERE order_id=$1 AND courier_id=$2 AND upload_id=$3 AND state='uploading';

-- name: VerifyProofUpload :execrows
UPDATE order_proof_uploads SET verified_at=COALESCE(verified_at,clock_timestamp()) WHERE id=$1 AND state='uploading';

-- name: AcceptProofUpload :execrows
UPDATE order_proof_uploads SET state='accepted' WHERE id=$1 AND state='uploading';

-- name: GetProofConfirmation :one
SELECT payload FROM order_proof_confirmations WHERE order_id=$1;

-- name: SaveProofConfirmation :exec
INSERT INTO order_proof_confirmations(order_id,payload) VALUES($1,$2);

-- name: GetPendingDeliveryReceipt :one
SELECT * FROM order_delivery_receipts WHERE order_id=$1 AND state='pending';

-- name: GetDeliveryReceipt :one
SELECT * FROM order_delivery_receipts WHERE id=$1;

-- name: InsertDeliveryReceipt :exec
INSERT INTO order_delivery_receipts(id,order_id,courier_id,proof_ids,unattended,without_proof,received_at,state,outage)
VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8);

-- name: FinishDeliveryReceipt :execrows
UPDATE order_delivery_receipts SET state=$2,processed_at=$3 WHERE id=$1 AND state='pending';

-- name: HasPendingDelivery :one
SELECT EXISTS(
 SELECT 1 FROM order_delivery_receipts r JOIN orders o ON o.id=r.order_id
 WHERE r.order_id=$1 AND r.state='pending' AND o.status='delivering' AND o.courier_id=r.courier_id
 AND o.picked_up_at IS NOT NULL AND r.received_at < o.picked_up_at + ((o.delivery_duration_ns * 2 + 999) / 1000) * interval '1 microsecond'
 AND ((r.without_proof AND r.outage IS NOT NULL) OR (NOT r.without_proof AND NOT EXISTS(
 SELECT 1 FROM unnest(r.proof_ids) p(id) LEFT JOIN order_proof_uploads u ON u.id=p.id
 WHERE u.id IS NULL OR u.order_id<>r.order_id OR u.courier_id<>r.courier_id OR u.state<>'uploading' OR u.verified_at IS NULL OR u.verified_at>r.received_at
 )))
);

-- name: ProofReservedByReceipt :one
SELECT EXISTS(SELECT 1 FROM order_delivery_receipts WHERE order_id=$1 AND state='pending' AND $2::uuid=ANY(proof_ids));

-- name: DueProofReceipts :many
SELECT id FROM order_delivery_receipts WHERE state='pending' AND next_attempt_at <= clock_timestamp() ORDER BY next_attempt_at,received_at,id LIMIT $1;

-- name: RetryProofReceipt :exec
UPDATE order_delivery_receipts SET next_attempt_at=clock_timestamp()+LEAST(attempt_count+1,30)*interval '2 seconds',attempt_count=attempt_count+1 WHERE id=$1 AND state='pending';

-- name: AlertProofReceipt :execrows
UPDATE order_delivery_receipts SET alerted_at=clock_timestamp() WHERE id=$1 AND state='pending' AND received_at<=clock_timestamp()-interval '5 minutes' AND (alerted_at IS NULL OR alerted_at<clock_timestamp()-interval '1 hour');

-- name: CurrentProofOutage :one
SELECT * FROM order_proof_outage_state WHERE scope=$1;

-- name: LockProofOutage :one
SELECT * FROM order_proof_outage_state WHERE scope=$1 FOR SHARE;

-- name: PublishProofOutage :exec
INSERT INTO order_proof_outage_state(scope,evidence_id,generation,observed_at,valid_until,reason,active)
VALUES($1,$2,1,$3,$4,$5,$6)
ON CONFLICT(scope) DO UPDATE SET evidence_id=EXCLUDED.evidence_id,generation=order_proof_outage_state.generation+1,observed_at=EXCLUDED.observed_at,valid_until=EXCLUDED.valid_until,reason=EXCLUDED.reason,active=EXCLUDED.active;

-- name: ProofCleanupCandidates :many
SELECT u.id,u.order_id FROM order_proof_uploads u JOIN orders o ON o.id=u.order_id
WHERE u.upload_scope=$2 AND u.state<>'accepted' AND u.expires_at <= $1 AND u.cleanup_after <= $1
AND (u.state='deleting' OR o.status<>'delivering' OR
 clock_timestamp() >= o.picked_up_at + ((o.delivery_duration_ns*2+999)/1000)*interval '1 microsecond')
AND NOT EXISTS(SELECT 1 FROM order_delivery_receipts r WHERE r.order_id=u.order_id AND r.state='pending')
ORDER BY u.cleanup_after,u.id LIMIT $3;

-- name: ScheduleProofCleanup :execrows
UPDATE order_proof_uploads SET cleanup_after=$2 WHERE id=$1 AND state='deleting' AND cleanup_after <= clock_timestamp();

-- name: CountRecentProofIdentities :one
SELECT count(*) FROM (
 SELECT u.upload_id FROM order_proof_uploads u WHERE u.order_id=$1 AND u.courier_id=$2 AND u.created_at>=sqlc.arg(since_at)
 UNION
 SELECT f.upload_id FROM order_proof_fences f WHERE f.order_id=$1 AND f.courier_id=$2 AND f.retired_at>=sqlc.arg(since_at)
) identities;
