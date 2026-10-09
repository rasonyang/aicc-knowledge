-- SPDX-License-Identifier: Apache-2.0
--
-- "Newest publication" and "most recently superseded publication" decide the
-- default rollback target, which indexes retention keeps and which BUILDING
-- rows are abandoned first. They were derived from created_at, superseded_at
-- and the uuidv7 id, all read from the clock of whichever database backend ran
-- the statement. Backends of one server can disagree about the time (seen on
-- virtual machines with per-CPU counter offsets), so two operator actions made
-- in order could sort the other way round. Order must not depend on a clock:
--
--   seq             creation order of a publication, drawn from an identity
--                   sequence at INSERT. Unique.
--   superseded_seq  drawn from publication_superseded_seq each time a
--                   publication becomes SUPERSEDED (publish demotion and
--                   rollback demotion). Most recently superseded = highest.
--                   It is NULL for every other state: promoting a SUPERSEDED
--                   publication back to LIVE clears it, exactly as it clears
--                   superseded_at, and the next demotion draws a fresh number,
--                   so a rolled-back-from publication ranks as the newest
--                   superseded one.
--
-- Both are written while the language lock is held, so draws for one language
-- are serialized. Timestamps stay for display. Existing rows are numbered in
-- the order the old rule would have sorted them.

-- +goose Up
ALTER TABLE publications ADD COLUMN seq bigint;
UPDATE publications p SET seq = o.n
FROM (SELECT id, row_number() OVER (ORDER BY created_at, id) AS n FROM publications) o
WHERE p.id = o.id;
ALTER TABLE publications ALTER COLUMN seq SET NOT NULL;
ALTER TABLE publications ALTER COLUMN seq ADD GENERATED ALWAYS AS IDENTITY;
SELECT setval(pg_get_serial_sequence('publications', 'seq'), COALESCE(max(seq), 1), max(seq) IS NOT NULL) FROM publications;
ALTER TABLE publications ADD CONSTRAINT uq_publications_seq UNIQUE (seq);

CREATE SEQUENCE publication_superseded_seq;
ALTER TABLE publications ADD COLUMN superseded_seq bigint;
UPDATE publications p SET superseded_seq = o.n
FROM (SELECT id, row_number() OVER (ORDER BY superseded_at, created_at, id) AS n
      FROM publications WHERE state = 'SUPERSEDED') o
WHERE p.id = o.id;
SELECT setval('publication_superseded_seq', COALESCE(max(superseded_seq), 1), max(superseded_seq) IS NOT NULL) FROM publications;
CREATE INDEX idx_publications_language_seq ON publications (language, seq DESC);

-- +goose Down
DROP INDEX idx_publications_language_seq;
ALTER TABLE publications DROP CONSTRAINT uq_publications_seq;
ALTER TABLE publications DROP COLUMN superseded_seq;
DROP SEQUENCE publication_superseded_seq;
ALTER TABLE publications DROP COLUMN seq;
