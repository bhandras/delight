-- name: GetMachine :one
SELECT * FROM machines
WHERE account_id = ? AND id = ?
LIMIT 1;

-- name: ListMachines :many
SELECT * FROM machines
WHERE account_id = ?
ORDER BY last_active_at DESC;

-- name: CreateMachine :exec
INSERT INTO machines (
    id, account_id, metadata, metadata_version,
    daemon_state, daemon_state_version, data_encryption_key
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpdateMachineSeq :one
UPDATE machines
SET seq = seq + 1
WHERE account_id = ? AND id = ?
RETURNING seq;

-- name: UpdateMachineMetadata :execrows
UPDATE machines
SET metadata = ?, metadata_version = ?
WHERE account_id = ? AND id = ? AND metadata_version = ?;

-- name: UpdateMachineDaemonState :execrows
UPDATE machines
SET daemon_state = ?, daemon_state_version = ?
WHERE account_id = ? AND id = ? AND daemon_state_version = ?;

-- name: UpdateMachineActivity :exec
UPDATE machines
SET active = ?, last_active_at = ?
WHERE account_id = ? AND id = ?;

-- name: DeleteMachine :exec
DELETE FROM machines WHERE account_id = ? AND id = ?;
