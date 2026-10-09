// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// ErrNoLivePublication means the language has no LIVE publication.
var ErrNoLivePublication = errors.New("no live publication")

// LivePublication returns the LIVE publication of a language.
func (s *Store) LivePublication(ctx context.Context, lang domain.Language) (queries.Publication, error) {
	p, err := s.Queries.GetLivePublication(ctx, string(lang))
	if errors.Is(err, pgx.ErrNoRows) {
		return queries.Publication{}, ErrNoLivePublication
	}
	if err != nil {
		return queries.Publication{}, fmt.Errorf("get live publication: %w", err)
	}
	return p, nil
}
