-- SPDX-License-Identifier: Apache-2.0

-- name: CountCandidatesOfVersion :one
SELECT count(*) FROM candidates WHERE file_version_id = $1;

-- name: InsertCandidate :execrows
-- Idempotent per (file_version_id, content_hash): a repeat inserts nothing.
INSERT INTO candidates (file_version_id, section_ordinal, language, question, alternate_questions, answer,
                        source_ref, flags, content_hash, prompt_version, model, generated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (file_version_id, content_hash) DO NOTHING;

-- name: MarkCandidatesOfWithdrawnSectionsStale :execrows
-- Called where a version's sections are rewritten. A candidate is tied to its
-- section by (ordinal, source_ref); one whose section no longer exists can no
-- longer be checked against its source. The reason is appended to any note.
UPDATE candidates c
SET state = 'STALE',
    review_note = COALESCE(NULLIF(c.review_note, '') || E'\n', '') || 'SECTION_WITHDRAWN'
WHERE c.file_version_id = $1
  AND c.state <> 'STALE'
  AND c.section_ordinal IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM parsed_sections s
      WHERE s.file_version_id = c.file_version_id AND s.ordinal = c.section_ordinal AND s.source_ref = c.source_ref);

-- name: MarkQACandidatesOfRowStale :execrows
-- A Q&A row changed under the same (ordinal, source_ref): the candidates
-- imported from it (prompt_version qa-*) no longer match their source.
UPDATE candidates
SET state = 'STALE',
    review_note = COALESCE(NULLIF(review_note, '') || E'\n', '') || 'QA_ROW_CHANGED'
WHERE file_version_id = $1 AND section_ordinal = $2 AND source_ref = $3
  AND state <> 'STALE' AND prompt_version LIKE 'qa-%';

-- name: ListCandidatesForExport :many
-- Candidates in deterministic order for the review workbook, with the text of
-- the section they came from (empty when the section is gone).
SELECT c.id, c.language, c.question, c.alternate_questions, c.answer, c.flags, c.source_ref, c.content_hash,
       c.state, COALESCE(c.review_note, '')::text AS review_note, COALESCE(s.body, '')::text AS section_body
FROM candidates c
JOIN file_versions v ON v.id = c.file_version_id
JOIN source_files sf ON sf.id = v.source_file_id
LEFT JOIN parsed_sections s ON s.file_version_id = c.file_version_id AND s.ordinal = c.section_ordinal AND s.source_ref = c.source_ref
WHERE c.state = ANY(sqlc.arg(states)::text[])
  AND (sqlc.arg(language)::text = '' OR c.language = sqlc.arg(language)::text)
ORDER BY sf.object_key, v.version_no, c.section_ordinal NULLS FIRST, c.created_at, c.id;

-- name: LockCandidateForReview :one
-- The candidate the review import needs, locked for the rest of the
-- transaction. current_ok says whether its file version is still the current,
-- PARSED one.
SELECT c.id, c.file_version_id, c.language, c.question, c.alternate_questions, c.answer, c.source_ref, c.flags,
       c.state, c.content_hash, c.review_note,
       (v.superseded_at IS NULL AND v.state = 'PARSED')::bool AS version_is_current
FROM candidates c
JOIN file_versions v ON v.id = c.file_version_id
WHERE c.id = $1
FOR UPDATE OF c;

-- name: ReviewCandidate :execrows
-- Applies a decision to a PENDING_REVIEW candidate. The caller has checked the
-- edge with domain.CandidateState.Transition.
UPDATE candidates
SET state = sqlc.arg(to_state), question = sqlc.arg(question), alternate_questions = sqlc.arg(alternate_questions),
    answer = sqlc.arg(answer), flags = sqlc.arg(flags), content_hash = sqlc.arg(content_hash),
    review_note = sqlc.narg(review_note), reviewed_at = now()
WHERE id = sqlc.arg(id) AND state = 'PENDING_REVIEW';

-- name: InsertCandidateReview :exec
INSERT INTO candidate_reviews (candidate_id, action, reviewer, before, after, source_file_name)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListLiveCandidateSections :many
-- The (ordinal, source_ref) of every section of a version that has a
-- candidate in any state but STALE. Generation uses it to find Q&A rows that
-- still need one.
SELECT DISTINCT section_ordinal, source_ref FROM candidates
WHERE file_version_id = $1 AND state <> 'STALE' AND section_ordinal IS NOT NULL;
