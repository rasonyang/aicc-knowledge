-- SPDX-License-Identifier: Apache-2.0
--
-- Publications. Publishing builds a brand-new Meilisearch index named
-- index_uid from approved candidates, then swaps it atomically with the
-- language's live uid (faq_en, faq_zh). Every build is a row here, so every
-- publication is versioned and rollback is "swap the previous one back".
--
-- States: BUILDING -> LIVE | FAILED, LIVE -> SUPERSEDED, and SUPERSEDED -> LIVE
-- for rollback (see internal/domain). The partial unique index
-- uq_publications_live_language lets at most one publication per language be
-- LIVE; a rollback demotes the current LIVE row before it promotes the chosen
-- one inside a single transaction.
--
-- publication_items lists which candidates a publication contains and
-- snapshots what was published (question, alternate_questions, answer,
-- source_ref) next to the content_hash each had when it was built. Candidates
-- may later change state or be edited, so rollback to a publication whose
-- Meilisearch index was pruned rebuilds it from these snapshot columns, not
-- from the candidates table.

-- +goose Up
CREATE TABLE publications (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    language      varchar(2)   NOT NULL CHECK (language IN ('EN', 'ZH')),
    index_uid     varchar(128) NOT NULL,
    state         varchar(16)  NOT NULL DEFAULT 'BUILDING'
                  CHECK (state IN ('BUILDING', 'LIVE', 'SUPERSEDED', 'FAILED')),
    item_count    integer      NOT NULL DEFAULT 0 CHECK (item_count >= 0),
    error_code    varchar(64),
    created_at    timestamptz  NOT NULL DEFAULT now(),
    live_at       timestamptz,
    superseded_at timestamptz,
    CONSTRAINT uq_publications_index_uid UNIQUE (index_uid)
);

CREATE UNIQUE INDEX uq_publications_live_language ON publications (language) WHERE state = 'LIVE';
CREATE INDEX idx_publications_language_created_at ON publications (language, created_at DESC);

CREATE TABLE publication_items (
    publication_id uuid  NOT NULL,
    candidate_id   uuid  NOT NULL,
    content_hash   bytea NOT NULL CHECK (octet_length(content_hash) = 32),
    question            text   NOT NULL,
    alternate_questions text[] NOT NULL DEFAULT '{}',
    answer              text   NOT NULL,
    source_ref          text   NOT NULL,
    PRIMARY KEY (publication_id, candidate_id),
    CONSTRAINT fk_publication_items_publications FOREIGN KEY (publication_id) REFERENCES publications (id) ON DELETE CASCADE,
    CONSTRAINT fk_publication_items_candidates FOREIGN KEY (candidate_id) REFERENCES candidates (id)
);

CREATE INDEX idx_publication_items_candidate_id ON publication_items (candidate_id);

-- +goose Down
DROP TABLE publication_items;
DROP TABLE publications;
