-- SPDX-License-Identifier: Apache-2.0
--
-- API keys. The service has no users and no tenants: a bearer key identifies a
-- caller (AICC, an operator script) and that is all. The secret is 32 random
-- bytes shown once by `create-api-key`; only its SHA-256 digest is stored and
-- the digest is what a request is looked up by, so a database dump cannot be
-- replayed as credentials. key_prefix is the first eight characters of the
-- secret, kept in clear so an operator can tell keys apart in a list.

-- +goose Up
CREATE TABLE api_keys (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    name         varchar(128) NOT NULL,
    key_prefix   varchar(8)   NOT NULL,
    key_hash     bytea        NOT NULL CHECK (octet_length(key_hash) = 32),
    created_at   timestamptz  NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz,
    CONSTRAINT uq_api_keys_key_hash UNIQUE (key_hash)
);

-- +goose Down
DROP TABLE api_keys;
