// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

const (
	keySecretBytes = 32
	keyPrefixLen   = 8
)

// ErrInvalidAPIKey means the presented key matches no active key.
var ErrInvalidAPIKey = errors.New("invalid api key")

// HashAPIKey is the SHA-256 digest a key is stored and looked up by.
func HashAPIKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// IssueAPIKey creates a key named name and returns its secret. The secret is
// returned only here; the database keeps its digest.
func (s *Store) IssueAPIKey(ctx context.Context, name string) (secret string, err error) {
	raw := make([]byte, keySecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	secret = base64.RawURLEncoding.EncodeToString(raw)
	_, err = s.Queries.CreateAPIKey(ctx, queries.CreateAPIKeyParams{Name: name, KeyPrefix: secret[:keyPrefixLen], KeyHash: HashAPIKey(secret)})
	if err != nil {
		return "", fmt.Errorf("store key: %w", err)
	}
	return secret, nil
}

// AuthenticateAPIKey returns nil when secret is an active key.
func (s *Store) AuthenticateAPIKey(ctx context.Context, secret string) error {
	row, err := s.Queries.GetActiveAPIKeyByHash(ctx, HashAPIKey(secret))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidAPIKey
	}
	if err != nil {
		return fmt.Errorf("look up key: %w", err)
	}
	// Best effort: a failed touch must not fail the request.
	_ = s.Queries.TouchAPIKey(ctx, row.ID)
	return nil
}
