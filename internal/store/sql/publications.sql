-- SPDX-License-Identifier: Apache-2.0

-- name: GetLivePublication :one
SELECT * FROM publications
WHERE language = $1 AND state = 'LIVE';

-- name: GetPublication :one
SELECT * FROM publications WHERE id = $1;

-- name: InsertPublication :exec
INSERT INTO publications (id, language, index_uid, catalog_id) VALUES ($1, $2, $3, $4);

-- name: InsertPublicationItem :exec
INSERT INTO publication_items (publication_id, candidate_id, content_hash, question, alternate_questions, answer, source_ref, scope, products)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: SetPublicationItemCount :exec
UPDATE publications SET item_count = $2 WHERE id = $1;

-- name: ListPublicationItems :many
SELECT * FROM publication_items WHERE publication_id = $1 ORDER BY candidate_id;

-- name: FailPublication :execrows
-- BUILDING -> FAILED. The caller has checked the edge with domain.
UPDATE publications SET state = 'FAILED', error_code = $2, content_uid = NULL
WHERE id = $1 AND state = 'BUILDING';

-- name: ListBuildingPublications :many
SELECT * FROM publications WHERE language = $1 AND state = 'BUILDING' ORDER BY created_at;

-- name: LockLivePublication :one
SELECT * FROM publications WHERE language = $1 AND state = 'LIVE' FOR UPDATE;

-- name: LockPublication :one
SELECT * FROM publications WHERE id = $1 FOR UPDATE;

-- name: SetPublicationContentUID :exec
UPDATE publications SET content_uid = sqlc.narg(content_uid) WHERE id = sqlc.arg(id);

-- name: SupersedePublication :execrows
-- LIVE -> SUPERSEDED. content_uid is where the content lives now (NULL when no
-- index holds it).
UPDATE publications SET state = 'SUPERSEDED', superseded_at = now(), content_uid = sqlc.narg(content_uid)
WHERE id = sqlc.arg(id) AND state = 'LIVE';

-- name: PromotePublication :execrows
-- BUILDING -> LIVE (publish) or SUPERSEDED -> LIVE (rollback). The caller has
-- checked the edge, and demoted the current LIVE row first.
UPDATE publications SET state = 'LIVE', live_at = now(), superseded_at = NULL, content_uid = sqlc.arg(content_uid), error_code = NULL
WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: ListSupersededPublications :many
-- Newest superseded first: the default rollback target is the first row.
SELECT * FROM publications WHERE language = $1 AND state = 'SUPERSEDED'
ORDER BY superseded_at DESC, created_at DESC, id DESC;

-- name: ListPublicationsOfLanguage :many
SELECT * FROM publications WHERE language = $1 ORDER BY created_at, id;
