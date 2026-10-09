// SPDX-License-Identifier: Apache-2.0

package publish_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/parse/parsetest"
	"github.com/rasonyang/aicc-knowledge/internal/publish"
	"github.com/rasonyang/aicc-knowledge/internal/publish/publishtest"
)

const catalogV1 = `products:
  - id: zq-3
    names: ["ZQ 3", "ZQ三"]
  - id: zq-ultra
    names: ["ZQ Ultra"]
`

// catalogV2 renames ZQ 3 and adds a product, so the same questions are tagged
// differently.
const catalogV2 = `products:
  - id: zq-3
    names: ["ZQ Classic"]
  - id: zq-ultra
    names: ["ZQ Ultra", "ZQ 3"]
`

// catalogEnv is a publish Env over the store and S3 prefix of a parse Env, so
// the catalog reaches the publisher the way production does: S3, scan, parse.
func catalogEnv(t *testing.T) (*publishtest.Env, *parsetest.Env) {
	t.Helper()
	pe := parsetest.New(t)
	e := publishtest.NewOver(t, pe.Store)
	e.Publisher.S3Prefix = pe.S3.Prefix
	return e, pe
}

func productsOf(e *publishtest.Env, pub uuid.UUID, ids map[string]uuid.UUID) map[string][]string {
	got := e.ItemProducts(pub)
	out := map[string][]string{}
	for name, id := range ids {
		out[name] = got[id.String()]
	}
	return out
}

func TestPublishTagsEveryItemAndRecordsTheCatalog(t *testing.T) {
	e, pe := catalogEnv(t)
	pe.Put("products.yaml", []byte(catalogV1))
	pe.ScanParse()
	k := func(name string) string { return pe.ObjectKey(name) }
	ids := map[string]uuid.UUID{
		"question names a product":  e.AddCandidate(publishtest.Cand{Key: k("a.docx"), Language: EN, Question: "How long does the ZQ Ultra battery last?"}),
		"alternate names a product": e.AddCandidate(publishtest.Cand{Key: k("a.docx"), Language: EN, Question: "How do I reset it?", Alts: []string{"How do I reset the ZQ三?"}}),
		"two products":              e.AddCandidate(publishtest.Cand{Key: k("a.docx"), Language: EN, Question: "ZQ 3 or ZQ Ultra: which charges faster?"}),
		"file name names a product": e.AddCandidate(publishtest.Cand{Key: k("ZQ Ultra - manual.docx"), Language: EN, Question: "How do I update the firmware?"}),
		"question wins over file":   e.AddCandidate(publishtest.Cand{Key: k("ZQ Ultra - manual.docx"), Language: EN, Question: "Does the ZQ 3 support wireless charging?"}),
		"directory is not a name":   e.AddCandidate(publishtest.Cand{Key: k("zq-ultra/faq.docx"), Language: EN, Question: "How do I request an invoice?"}),
		"generic":                   e.AddCandidate(publishtest.Cand{Key: k("faq.docx"), Language: EN, Question: "What are your opening hours?"}),
	}
	r := e.Publish(EN)
	if r.Items != 7 {
		t.Fatalf("items = %d", r.Items)
	}
	want := map[string][]string{
		"question names a product":  {"zq-ultra"},
		"alternate names a product": {"zq-3"},
		"two products":              {"zq-3", "zq-ultra"},
		"file name names a product": {"zq-ultra"},
		"question wins over file":   {"zq-3"},
		"directory is not a name":   {},
		"generic":                   {},
	}
	got := productsOf(e, r.PublicationID, ids)
	for name, w := range want {
		if !slices.Equal(got[name], w) || len(got[name]) != len(w) {
			t.Errorf("%s: products = %v, want %v", name, got[name], w)
		}
	}
	var catalogID uuid.UUID
	if err := pe.Store.Pool.QueryRow(context.Background(), `SELECT id FROM product_catalogs`).Scan(&catalogID); err != nil {
		t.Fatal(err)
	}
	if got := e.PubCatalog(r.PublicationID); got != catalogID {
		t.Errorf("publication catalog = %s, want %s", got, catalogID)
	}

	// The tags are in the index documents too.
	resp, err := e.Search(EN, "How long does the ZQ Ultra battery last?", nil, 3)
	if err != nil || len(resp.Items) == 0 || resp.Items[0].ID != ids["question names a product"].String() ||
		!slices.Equal(resp.Items[0].Products, []string{"zq-ultra"}) {
		t.Fatalf("search = %+v (%v)", resp, err)
	}
}

func TestPublishWithoutACatalogObjectTagsNothing(t *testing.T) {
	e, pe := catalogEnv(t)
	id := e.AddCandidate(publishtest.Cand{Key: pe.ObjectKey("a.docx"), Language: EN, Question: "How long does the ZQ Ultra battery last?"})
	r := e.Publish(EN)
	if got := e.PubCatalog(r.PublicationID); got != uuid.Nil {
		t.Errorf("catalog = %s, want none", got)
	}
	if got := e.ItemProducts(r.PublicationID)[id.String()]; got == nil || len(got) != 0 {
		t.Errorf("products = %v, want an empty set", got)
	}
}

func TestCatalogChangeIsUsedByTheNextPublishAndRollbackRestoresTheOldSets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		retain  int
		rebuilt bool
	}{{"swap back", 3, false}, {"rebuild from the snapshot", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			e, pe := catalogEnv(t)
			e.Publisher.RetainIndexes = tc.retain
			pe.Put("products.yaml", []byte(catalogV1))
			pe.ScanParse()
			ids := map[string]uuid.UUID{
				"zq3": e.AddCandidate(publishtest.Cand{Key: pe.ObjectKey("a.docx"), Language: EN, Question: "How long does the ZQ 3 battery last?"}),
				"inv": e.AddCandidate(publishtest.Cand{Key: pe.ObjectKey("a.docx"), Language: EN, Question: "How do I request an invoice?"}),
			}
			p1 := e.Publish(EN)
			cat1 := e.PubCatalog(p1.PublicationID)

			pe.Put("products.yaml", []byte(catalogV2))
			pe.ScanParse()
			p2 := e.Publish(EN)
			cat2 := e.PubCatalog(p2.PublicationID)
			if cat1 == uuid.Nil || cat2 == uuid.Nil || cat1 == cat2 {
				t.Fatalf("catalogs = %s then %s, want two different rows", cat1, cat2)
			}
			s1, s2 := productsOf(e, p1.PublicationID, ids), productsOf(e, p2.PublicationID, ids)
			if !slices.Equal(s1["zq3"], []string{"zq-3"}) || !slices.Equal(s2["zq3"], []string{"zq-ultra"}) || len(s1["inv"])+len(s2["inv"]) != 0 {
				t.Fatalf("product sets: v1 %v, v2 %v", s1, s2)
			}

			rb, err := e.Publisher.Rollback(context.Background(), EN, nil)
			if err != nil || rb.To != p1.PublicationID || rb.Rebuilt != tc.rebuilt {
				t.Fatalf("rollback = %+v, %v", rb, err)
			}
			// The live documents carry the old product sets again, and the live
			// publication points at the old catalog.
			resp, err := e.Search(EN, "How long does the ZQ 3 battery last?", nil, 3)
			if err != nil || len(resp.Items) == 0 || resp.Items[0].ID != ids["zq3"].String() || !slices.Equal(resp.Items[0].Products, []string{"zq-3"}) {
				t.Fatalf("after rollback: %+v (%v)", resp, err)
			}
			if got := productsOf(e, p1.PublicationID, ids); !slices.Equal(got["zq3"], []string{"zq-3"}) {
				t.Errorf("snapshot changed: %v", got)
			}
			if got := e.PubCatalog(p1.PublicationID); got != cat1 {
				t.Errorf("catalog after rollback = %s, want %s", got, cat1)
			}
			cache := e.UseCatalogs()
			if c := cache.Catalog(EN); c == nil || len(c.Extract("ZQ 3").IDs) != 1 || c.Extract("ZQ 3").IDs[0] != "zq-3" {
				t.Errorf("the cache serves the wrong catalog after rollback")
			}
		})
	}
}

func TestPublishRefusesWhileTheCatalogFileIsBroken(t *testing.T) {
	e, pe := catalogEnv(t)
	pe.Put("products.yaml", []byte("products: ["))
	pe.ScanParse()
	e.AddCandidate(publishtest.Cand{Key: pe.ObjectKey("a.docx"), Language: EN, Question: "How long does the ZQ 3 battery last?"})
	_, err := e.Publisher.Publish(context.Background(), EN, publish.PublishOptions{})
	if publish.CodeOf(err) != publish.CodeCatalogUnavailable {
		t.Fatalf("err = %v, want CATALOG_UNAVAILABLE", err)
	}
	if n := len(e.Pubs(EN)); n != 0 {
		t.Errorf("publications = %d, want none (nothing is created)", n)
	}
	if uids := e.IndexUIDs(); len(uids) != 0 {
		t.Errorf("indexes = %v, want none", uids)
	}

	// Fixing the file lets the publish through; removing it publishes untagged.
	pe.Put("products.yaml", []byte(catalogV1))
	pe.ScanParse()
	if r := e.Publish(EN); e.PubCatalog(r.PublicationID) == uuid.Nil {
		t.Error("a fixed catalog was not used")
	}
	pe.S3.Delete("products.yaml")
	pe.ScanParse()
	r := e.Publish(EN)
	if got := e.PubCatalog(r.PublicationID); got != uuid.Nil {
		t.Errorf("catalog after removal = %s, want none", got)
	}
}

func TestAMisplacedCatalogDoesNotBlockPublishing(t *testing.T) {
	e, pe := catalogEnv(t)
	pe.Put("brand/products.yaml", []byte(catalogV1))
	pe.ScanParse()
	e.AddCandidate(publishtest.Cand{Key: pe.ObjectKey("a.docx"), Language: EN, Question: "How long does the ZQ 3 battery last?"})
	r := e.Publish(EN)
	if got := e.PubCatalog(r.PublicationID); got != uuid.Nil {
		t.Errorf("a catalog outside the root was used: %s", got)
	}
}
