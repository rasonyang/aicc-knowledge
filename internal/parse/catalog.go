// SPDX-License-Identifier: Apache-2.0

package parse

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/jobs"
	"github.com/rasonyang/aicc-knowledge/internal/products"
	"github.com/rasonyang/aicc-knowledge/internal/scan"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// parseCatalog handles `products.yaml`. The one at the root of the S3 prefix is
// the product catalog: it is validated and stored (product_catalogs) under its
// file version, and the version becomes PARSED. It has no sections, so no
// GENERATE job. An invalid catalog is PARSE_FAILED with CATALOG_INVALID and
// every issue as a warning; publish refuses to run while the current catalog
// version is not PARSED, so a broken file never silently turns the guard off.
// A products.yaml anywhere else is PARSE_FAILED with CATALOG_MISPLACED and is
// otherwise ignored: there is exactly one catalog.
func (w *Worker) parseCatalog(ctx context.Context, job jobs.Job, t target, data []byte) (string, string, error) {
	if !scan.IsCatalogRoot(w.S3.Prefix, t.ver.ObjectKey) {
		return w.fail(ctx, job, t, CodeCatalogMisplaced, []Warning{{
			Code: CodeCatalogMisplaced, Location: t.ver.ObjectKey,
			Detail: "the catalog is the products.yaml at the root of the S3 prefix; there is exactly one, and this one is ignored",
		}})
	}
	cat, err := products.Parse(data)
	if err != nil {
		var pe *products.Error
		if !errors.As(err, &pe) {
			return t.kind, "", err
		}
		warns := make([]Warning, 0, len(pe.Issues))
		for _, iss := range pe.Issues {
			warns = append(warns, Warning{Code: products.CodeCatalogInvalid, Detail: iss})
		}
		return w.fail(ctx, job, t, products.CodeCatalogInvalid, warns)
	}
	raw, err := cat.MarshalJSON()
	if err != nil {
		return t.kind, "", fmt.Errorf("encode the catalog: %w", err)
	}
	txErr := w.inTx(ctx, job, t.ver.ID, false, func(q *queries.Queries, _ pgx.Tx) error {
		if _, err := q.InsertProductCatalog(ctx, queries.InsertProductCatalogParams{FileVersionID: t.ver.ID, Products: raw}); err != nil {
			return fmt.Errorf("store the catalog: %w", err)
		}
		return w.transition(ctx, q, t.ver.ID, domain.FileVersionParsed, nil, nil)
	})
	return w.finish(t, OutcomeParsed, txErr)
}
