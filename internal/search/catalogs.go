// SPDX-License-Identifier: Apache-2.0

package search

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/products"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// Catalogs tells the searcher which product catalog applies to a language. nil
// means no catalog: the guard is off and search behaves as it did without one.
type Catalogs interface {
	Catalog(lang domain.Language) *products.Catalog
}

// StaticCatalogs is a fixed catalog per language (eval, tests).
type StaticCatalogs map[domain.Language]*products.Catalog

// Catalog implements Catalogs.
func (s StaticCatalogs) Catalog(lang domain.Language) *products.Catalog { return s[lang] }

type cached struct {
	id  uuid.UUID
	cat *products.Catalog
}

// CatalogCache keeps the catalog of each language's LIVE publication in memory,
// so a search never queries PostgreSQL. Refresh reads the catalog the live
// publication was built with (publications.catalog_id); Run repeats it every
// interval.
//
// Lag: a publish or rollback is visible to the searcher at once (the index
// swap), but the cache learns the new catalog at its next refresh, up to one
// interval later (KB_PRODUCTS_REFRESH_SEC). The documents carry their own
// product ids, so the only effect of the lag is that, for up to one interval
// after a publication that changed the catalog, a query is read with the old
// catalog's names: a new alias is not recognised yet, and an id that the old
// catalog does not know is not matched. A failed refresh keeps the previous
// catalog and logs.
type CatalogCache struct {
	Queries *queries.Queries
	Log     *slog.Logger
	en, zh  atomic.Pointer[cached]
}

func (c *CatalogCache) slot(lang domain.Language) *atomic.Pointer[cached] {
	if lang == domain.LanguageZH {
		return &c.zh
	}
	return &c.en
}

// Catalog implements Catalogs.
func (c *CatalogCache) Catalog(lang domain.Language) *products.Catalog {
	if e := c.slot(lang).Load(); e != nil {
		return e.cat
	}
	return nil
}

// Refresh reloads both languages. It returns the joined errors of the
// languages that failed; those keep their previous catalog.
func (c *CatalogCache) Refresh(ctx context.Context) error {
	var errs []error
	for _, lang := range []domain.Language{domain.LanguageEN, domain.LanguageZH} {
		if err := c.refresh(ctx, lang); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *CatalogCache) refresh(ctx context.Context, lang domain.Language) error {
	slot := c.slot(lang)
	row, err := c.Queries.GetLiveCatalog(ctx, string(lang))
	if errors.Is(err, pgx.ErrNoRows) {
		if slot.Swap(nil) != nil {
			c.log().Info("product catalog unloaded: the live publication has none", "language", lang)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if cur := slot.Load(); cur != nil && cur.id == row.ID {
		return nil
	}
	cat, err := products.FromJSON(row.Products)
	if err != nil {
		return err
	}
	slot.Store(&cached{id: row.ID, cat: cat})
	c.log().Info("product catalog loaded", "language", lang, "catalogId", row.ID, "products", len(cat.Products()))
	return nil
}

// Run refreshes every interval until ctx ends. Call Refresh first for the
// startup load.
func (c *CatalogCache) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.Refresh(ctx); err != nil && ctx.Err() == nil {
				c.log().Warn("product catalog refresh failed; keeping the previous catalog", "error", err)
			}
		}
	}
}

func (c *CatalogCache) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}
