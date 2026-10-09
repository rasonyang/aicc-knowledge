-- SPDX-License-Identifier: Apache-2.0

-- name: EnqueueJob :execrows
-- Idempotent per (kind, dedupe_key): a repeat returns 0 rows affected.
-- A job with no run_after is due immediately: it is stored as -infinity, not as
-- now(), so that it is claimable whatever clock the claiming backend reads. Two
-- backends of one server can disagree by hundreds of milliseconds (virtual
-- machines with per-CPU counter offsets), and a now() taken by a backend that
-- runs ahead would keep a job just committed invisible to the next claim.
INSERT INTO jobs (kind, payload, dedupe_key, max_attempts, run_after)
VALUES (sqlc.arg(kind), sqlc.arg(payload), sqlc.narg(dedupe_key), sqlc.arg(max_attempts), COALESCE(sqlc.narg(run_after)::timestamptz, '-infinity'::timestamptz))
ON CONFLICT (kind, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING;

-- name: ReclaimExpiredJobs :execrows
-- A RUNNING job whose lease ran out goes back to QUEUED, or to FAILED when it
-- has used all its attempts (the claim that expired was its last one).
UPDATE jobs
SET state = CASE WHEN attempts >= max_attempts THEN 'FAILED' ELSE 'QUEUED' END,
    finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END,
    last_error_code = 'LEASE_EXPIRED',
    last_error_message = 'lease expired before the worker finished',
    locked_until = NULL,
    locked_by = NULL
WHERE state = 'RUNNING' AND locked_until < now();

-- name: ClaimJob :one
-- SKIP LOCKED lets concurrent workers each take a different row.
UPDATE jobs
SET state = 'RUNNING',
    attempts = attempts + 1,
    locked_by = sqlc.arg(worker)::text,
    locked_until = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8)
WHERE id = (
    SELECT j.id FROM jobs j
    WHERE j.state = 'QUEUED' AND j.run_after <= now() AND j.kind = ANY(sqlc.arg(kinds)::text[])
    ORDER BY j.run_after, j.id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: ClaimJobByDedupeKey :one
-- Claims the one job of (kind, dedupe_key) when it is claimable; the operator's
-- `generate --version` uses it to work on a single file version.
UPDATE jobs
SET state = 'RUNNING',
    attempts = attempts + 1,
    locked_by = sqlc.arg(worker)::text,
    locked_until = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8)
WHERE id = (
    SELECT j.id FROM jobs j
    WHERE j.state = 'QUEUED' AND j.run_after <= now() AND j.kind = sqlc.arg(kind)::text AND j.dedupe_key = sqlc.arg(dedupe_key)::text
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: CompleteJob :execrows
UPDATE jobs
SET state = 'SUCCEEDED', finished_at = now(), locked_until = NULL, locked_by = NULL
WHERE id = sqlc.arg(id) AND state = 'RUNNING' AND locked_by = sqlc.arg(worker)::text;

-- name: FailJob :execrows
-- Retry with exponential backoff (base * 2^(attempts-1), capped), or FAILED
-- once attempts reached max_attempts.
UPDATE jobs
SET state = CASE WHEN attempts >= max_attempts THEN 'FAILED' ELSE 'QUEUED' END,
    finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END,
    run_after = CASE WHEN attempts >= max_attempts THEN run_after
                     ELSE now() + make_interval(secs => LEAST(sqlc.arg(cap_seconds)::float8, sqlc.arg(base_seconds)::float8 * power(2, attempts - 1))) END,
    last_error_code = sqlc.arg(error_code)::text,
    last_error_message = sqlc.arg(error_message)::text,
    locked_until = NULL,
    locked_by = NULL
WHERE id = sqlc.arg(id) AND state = 'RUNNING' AND locked_by = sqlc.arg(worker)::text;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = $1;

-- name: RearmJob :execrows
-- Operator retry: puts a finished job back in the queue with a fresh attempt
-- budget. A QUEUED or RUNNING job is left alone.
UPDATE jobs
SET state = 'QUEUED', attempts = 0, run_after = '-infinity'::timestamptz, finished_at = NULL,
    locked_until = NULL, locked_by = NULL, last_error_code = NULL, last_error_message = NULL
WHERE kind = sqlc.arg(kind) AND dedupe_key = sqlc.arg(dedupe_key)::text AND state IN ('SUCCEEDED', 'FAILED');

-- name: ExtendJobLease :execrows
-- A long job (generate calls the LLM once per section) keeps its lease alive.
UPDATE jobs
SET locked_until = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8)
WHERE id = sqlc.arg(id) AND state = 'RUNNING' AND locked_by = sqlc.arg(worker)::text;
