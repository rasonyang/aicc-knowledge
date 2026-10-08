// SPDX-License-Identifier: Apache-2.0

package search_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/rasonyang/aicc-knowledge/internal/meili"
	"github.com/rasonyang/aicc-knowledge/internal/publish/publishtest"
	"github.com/rasonyang/aicc-knowledge/internal/search"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

// recorder is a RoundTripper that remembers the JSON body of every search
// request and then lets the request go to the real Meilisearch.
type recorder struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil && strings.HasSuffix(req.URL.Path, "/search") {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(b))
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.bodies = append(r.bodies, m)
		r.mu.Unlock()
	}
	return http.DefaultTransport.RoundTrip(req)
}

// TestSearcherSendsNoRankingScoreThreshold: Meilisearch is slow when fewer than
// limit hits clear rankingScoreThreshold, so the searcher never sends one and
// applies the threshold to the hits itself.
func TestSearcherSendsNoRankingScoreThreshold(t *testing.T) {
	e := publishtest.New(t)
	f := publishEN(e)
	murl, mkey := testdb.MeiliURL(t)
	rec := &recorder{}
	mc, err := meili.New(meili.Config{BaseURL: murl, APIKey: mkey, HTTPClient: &http.Client{Transport: rec}})
	if err != nil {
		t.Fatal(err)
	}
	s := *e.Searcher
	s.Meili = mc

	for _, tc := range []struct {
		query   string
		status  search.Status
		wantTop string
	}{
		{"What is the capital of France?", search.StatusNoMatch, ""},
		{"I can't remember my login password", search.StatusHit, f.reset.String()},
	} {
		r, err := s.Search(context.Background(), search.Request{Query: tc.query, Language: EN, TopK: 3})
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != tc.status || (tc.wantTop != "" && (len(r.Items) == 0 || r.Items[0].ID != tc.wantTop)) {
			t.Errorf("%q: %+v", tc.query, r)
		}
		for _, it := range r.Items {
			if it.Score < s.ThresholdEN {
				t.Errorf("%q: item scored %.3f, below the threshold %.2f", tc.query, it.Score, s.ThresholdEN)
			}
		}
	}
	if len(rec.bodies) != 2 {
		t.Fatalf("recorded %d search requests, want 2", len(rec.bodies))
	}
	for i, b := range rec.bodies {
		if _, has := b["rankingScoreThreshold"]; has {
			t.Errorf("request %d carries rankingScoreThreshold: %v", i, b["rankingScoreThreshold"])
		}
		if lim, _ := b["limit"].(float64); lim < 10 {
			t.Errorf("request %d limit = %v, want at least the over-fetch of 10", i, b["limit"])
		}
	}
}
