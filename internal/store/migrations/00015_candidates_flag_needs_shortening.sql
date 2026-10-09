-- SPDX-License-Identifier: Apache-2.0
--
-- A curated Q&A row whose answer is too long is condensed by one LLM call. When
-- that call cannot produce a usable answer (still too long, a figure the
-- original does not state, cut-off or invalid output) the row used to be
-- dropped, so curated knowledge silently disappeared. It is now kept as a
-- PENDING_REVIEW candidate with the original answer and the flag
-- NEEDS_SHORTENING: the reviewer must EDIT the answer down (APPROVE is refused),
-- and the flag goes away with the edit.
--
-- The flags CHECK is replaced, not added to: the gate test in internal/store
-- requires exactly one CHECK per enum column and equality with the Go set.

-- +goose Up
ALTER TABLE candidates DROP CONSTRAINT candidates_flags_check;
ALTER TABLE candidates ADD CONSTRAINT candidates_flags_check
    CHECK (flags <@ ARRAY['CONTAINS_FIGURES', 'NEEDS_SHORTENING']::text[]);

-- +goose Down
UPDATE candidates SET flags = array_remove(flags, 'NEEDS_SHORTENING');
ALTER TABLE candidates DROP CONSTRAINT candidates_flags_check;
ALTER TABLE candidates ADD CONSTRAINT candidates_flags_check
    CHECK (flags <@ ARRAY['CONTAINS_FIGURES']::text[]);
