// SPDX-License-Identifier: Apache-2.0

package publish_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/publish"
	"github.com/rasonyang/aicc-knowledge/internal/publish/publishtest"
)

const (
	EN = domain.LanguageEN
	ZH = domain.LanguageZH
)

func TestScopeFromObjectKey(t *testing.T) {
	keys := []string{"brand", "channel"}
	for _, tc := range []struct {
		name, prefix, key string
		keys              []string
		want              map[string]string
	}{
		{"two directories", "kb/", "kb/acme/web/faq.docx", keys, map[string]string{"brand": "acme", "channel": "web"}},
		{"one directory leaves the rest global", "kb/", "kb/acme/faq.docx", keys, map[string]string{"brand": "acme"}},
		{"no directory is global", "kb/", "kb/faq.docx", keys, map[string]string{}},
		{"deeper directories are ignored", "kb/", "kb/acme/web/2026/faq.docx", keys, map[string]string{"brand": "acme", "channel": "web"}},
		{"prefix without slash", "kb", "kb/acme/faq.docx", keys, map[string]string{"brand": "acme"}},
		{"empty prefix", "", "acme/web/faq.docx", keys, map[string]string{"brand": "acme", "channel": "web"}},
		{"outside the prefix has no scope", "kb/", "other/acme/faq.docx", keys, map[string]string{}},
		{"no template", "kb/", "kb/acme/faq.docx", nil, map[string]string{}},
		{"template order decides", "kb/", "kb/web/acme/faq.docx", []string{"channel", "brand"}, map[string]string{"channel": "web", "brand": "acme"}},
		{"slashes right after the prefix collapse", "kb/", "kb//web/faq.docx", keys, map[string]string{"brand": "web"}},
		{"an empty inner segment leaves its key unset", "kb/", "kb/acme//faq.docx", keys, map[string]string{"brand": "acme"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := publish.ScopeFromObjectKey(tc.keys, tc.prefix, tc.key)
			if !reflect.DeepEqual(got, tc.want) || len(got) != len(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// seed inserts the EN world of most tests and returns the ids by role.
type seed struct {
	reset, hours, cancel     uuid.UUID
	pending, rejected, stale uuid.UUID
	zhApproved               uuid.UUID
	approved                 []string
}

func seedWorld(e *publishtest.Env) seed {
	var s seed
	s.reset = e.AddCandidate(publishtest.Cand{Key: "kb/acme/web/faq.docx", Language: EN, Question: "How do I reset my password?",
		Alts: []string{"I forgot my password", "Password reset steps"}, Answer: "Use the Forgot password link on the sign-in page."})
	s.hours = e.AddCandidate(publishtest.Cand{Key: "kb/acme/hours.docx", Language: EN, Question: "What are your opening hours?",
		Answer: "We are open from nine to five on weekdays."})
	s.cancel = e.AddCandidate(publishtest.Cand{Language: EN, Question: "How can I cancel my subscription?",
		Answer: "Open Billing and choose Cancel subscription."})
	s.pending = e.AddCandidate(publishtest.Cand{Language: EN, Question: "Pending: do you ship overseas?", State: "PENDING_REVIEW"})
	s.rejected = e.AddCandidate(publishtest.Cand{Language: EN, Question: "Rejected: is the moon made of cheese?", State: "REJECTED"})
	s.stale = e.AddCandidate(publishtest.Cand{Key: "kb/old.docx", Language: EN, Question: "Stale: legacy product question", State: "APPROVED"})
	e.NewVersion("kb/old.docx") // the version it belongs to is superseded; the candidate is STALE
	s.zhApproved = e.AddCandidate(publishtest.Cand{Language: ZH, Question: "如何重置密码？", Answer: "请点击登录页面的忘记密码。"})
	s.approved = publishtest.IDs(s.reset, s.hours, s.cancel)
	return s
}

func TestPublishIndexesExactlyTheApprovedCandidates(t *testing.T) {
	e := publishtest.New(t)
	s := seedWorld(e)

	res := e.Publish(EN)
	if res.Outcome != publish.OutcomeLive || res.Items != 3 || res.Superseded != nil {
		t.Fatalf("result = %+v", res)
	}
	live := e.Live(EN)
	if got := e.DocIDs(live); !slices.Equal(got, s.approved) || len(got) != 3 {
		t.Fatalf("live index holds %v, want exactly the approved %v", got, s.approved)
	}
	for _, bad := range []uuid.UUID{s.pending, s.rejected, s.stale, s.zhApproved} {
		if slices.Contains(e.DocIDs(live), bad.String()) {
			t.Errorf("candidate %s must not be in the EN index", bad)
		}
	}
	// First publication: staging was renamed onto the live uid, nothing else exists.
	if got := e.IndexUIDs(); !slices.Equal(got, []string{live}) || len(got) != 1 {
		t.Errorf("indexes = %v, want only %s", got, live)
	}
	pubs := e.Pubs(EN)
	if len(pubs) != 1 || pubs[0].State != "LIVE" || pubs[0].ItemCount != 3 || pubs[0].ContentUID != live || pubs[0].ErrorCode != "" {
		t.Fatalf("publications = %+v", pubs)
	}
	if got := e.ItemIDs(pubs[0].ID); !slices.Equal(got, s.approved) {
		t.Errorf("snapshot items = %v", got)
	}
	// ZH was not touched.
	if len(e.Pubs(ZH)) != 0 {
		t.Errorf("ZH has publications: %+v", e.Pubs(ZH))
	}
	e.AssertContentUIDs(EN)

	// Documents carry the scope derived from the object path, global when absent.
	var scopes []string
	rows, err := e.Store.Pool.Query(context.Background(), `SELECT question || ' => ' || scope::text FROM publication_items ORDER BY question`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		scopes = append(scopes, s)
	}
	rows.Close()
	want := []string{
		`How can I cancel my subscription? => {}`,
		`How do I reset my password? => {"brand": "acme", "channel": "web"}`,
		`What are your opening hours? => {"brand": "acme"}`,
	}
	if !slices.Equal(scopes, want) || len(scopes) != 3 {
		t.Errorf("snapshot scopes = %q, want %q", scopes, want)
	}

	// An alternate question finds its document at ~1.0: one vector per phrasing.
	r, err := e.Search(EN, "I forgot my password", nil, 3)
	if err != nil || len(r.Items) == 0 || r.Items[0].ID != s.reset.String() || r.Items[0].Score < 0.99 {
		t.Fatalf("search by alternate = %+v, %v", r, err)
	}

	// ZH publishes on its own.
	if r := e.Publish(ZH); r.Items != 1 {
		t.Fatalf("zh result = %+v", r)
	}
	if got := e.DocIDs(e.Live(ZH)); !slices.Equal(got, publishtest.IDs(s.zhApproved)) {
		t.Errorf("zh index = %v", got)
	}
	e.AssertContentUIDs(EN)
	e.AssertContentUIDs(ZH)
}

func TestSecondPublishSwapsAndTheStagingUIDHoldsTheOldContent(t *testing.T) {
	e := publishtest.New(t)
	s := seedWorld(e)
	r1 := e.Publish(EN)
	extra := e.AddCandidate(publishtest.Cand{Language: EN, Question: "Do you have a mobile app?"})
	r2 := e.Publish(EN)

	if r2.Items != 4 || r2.Superseded == nil || *r2.Superseded != r1.PublicationID {
		t.Fatalf("r2 = %+v", r2)
	}
	p1, p2 := e.Pub(r1.PublicationID), e.Pub(r2.PublicationID)
	if p1.State != "SUPERSEDED" || p2.State != "LIVE" {
		t.Fatalf("states = %s, %s", p1.State, p2.State)
	}
	if p2.ContentUID != e.Live(EN) || p1.ContentUID != p2.IndexUID {
		t.Fatalf("content_uid: p1=%s p2=%s; want p2 in the live uid and p1 in p2's staging uid %s", p1.ContentUID, p2.ContentUID, p2.IndexUID)
	}
	if got := e.DocIDs(p1.ContentUID); !slices.Equal(got, s.approved) || len(got) != 3 {
		t.Errorf("old content = %v", got)
	}
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, publishtest.IDs(s.reset, s.hours, s.cancel, extra)) || len(got) != 4 {
		t.Errorf("new content = %v", got)
	}
	if got := e.IndexUIDs(); len(got) != 2 {
		t.Errorf("indexes = %v, want live + the retained one", got)
	}
	e.AssertContentUIDs(EN)
}

// stagingIndexes returns the namespace's indexes other than the live uid.
func stagingIndexes(e *publishtest.Env, lang domain.Language) []string {
	var out []string
	for _, u := range e.IndexUIDs() {
		if u != e.Live(lang) {
			out = append(out, u)
		}
	}
	return out
}

// truncatingEmbedder returns vectors one dimension short, so Meilisearch
// rejects the document batch (invalid_vector_dimensions) after the index was
// created: a real failure inside the build, not a stub of one.
type truncatingEmbedder struct{ publish.Embedder }

func (t truncatingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	v, err := t.Embedder.EmbedBatch(ctx, texts)
	for i := range v {
		v[i] = v[i][:len(v[i])-1]
	}
	return v, err
}

func TestPublishFailureLeavesTheLiveIndexAndRowUntouched(t *testing.T) {
	cases := []struct {
		name     string
		inject   func(e *publishtest.Env)
		wantCode publish.Code
	}{
		{"hook before the swap", func(e *publishtest.Env) {
			e.Publisher.Hooks.BeforeSwap = func(context.Context, string) error { return errors.New("injected") }
		}, publish.CodeHookFailed},
		{"meilisearch rejects the vectors", func(e *publishtest.Env) {
			e.Publisher.Embedder = truncatingEmbedder{e.Embed}
		}, publish.CodeIndexBuildFailed},
		{"the database commit fails after the swap", func(e *publishtest.Env) {
			e.Publisher.Hooks.AfterSwap = func(context.Context) error { return errors.New("injected") }
		}, publish.CodeStateCommitFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := publishtest.New(t)
			s := seedWorld(e)
			r1 := e.Publish(EN)
			e.AddCandidate(publishtest.Cand{Language: EN, Question: "Do you have a mobile app?"})
			beforeIndexes := e.IndexUIDs()
			beforeLive := e.DocIDs(e.Live(EN))
			beforePubs := e.Pubs(EN)

			tc.inject(e)
			res, err := e.Publisher.Publish(context.Background(), EN, publish.PublishOptions{})
			if publish.CodeOf(err) != tc.wantCode {
				t.Fatalf("err = %v (code %q), want %s", err, publish.CodeOf(err), tc.wantCode)
			}
			if res.Outcome == publish.OutcomeLive {
				t.Fatalf("result claims LIVE: %+v", res)
			}

			if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, beforeLive) || !slices.Equal(got, s.approved) || len(got) != 3 {
				t.Errorf("live index changed: %v, want %v", got, s.approved)
			}
			if got := e.IndexUIDs(); !slices.Equal(got, beforeIndexes) || len(got) != 1 {
				t.Errorf("indexes = %v, want %v (no stray staging index)", got, beforeIndexes)
			}
			pubs := e.Pubs(EN)
			if len(pubs) != len(beforePubs)+1 {
				t.Fatalf("publications = %+v", pubs)
			}
			if !reflect.DeepEqual(pubs[0], beforePubs[0]) || pubs[0].ID != r1.PublicationID || pubs[0].State != "LIVE" {
				t.Errorf("the LIVE row changed: %+v -> %+v", beforePubs[0], pubs[0])
			}
			failed := pubs[1]
			if failed.State != "FAILED" || failed.ErrorCode != string(tc.wantCode) || failed.ContentUID != "" || failed.ItemCount != 4 {
				t.Errorf("failed row = %+v", failed)
			}
			e.AssertContentUIDs(EN)

			// The injection is gone: the next publish works and supersedes P1.
			e.Publisher.Hooks = publish.Hooks{}
			e.Publisher.Embedder = e.Embed
			r3 := e.Publish(EN)
			if r3.Items != 4 || r3.Superseded == nil || *r3.Superseded != r1.PublicationID {
				t.Errorf("recovery publish = %+v", r3)
			}
			e.AssertContentUIDs(EN)
		})
	}
}

func TestPublishRefusesAnEmbedderOfAnotherSize(t *testing.T) {
	e := publishtest.New(t)
	seedWorld(e)
	e.Publisher.Dimensions = 768
	_, err := e.Publisher.Publish(context.Background(), EN, publish.PublishOptions{})
	if publish.CodeOf(err) != publish.CodeDimensionsMismatch {
		t.Fatalf("err = %v", err)
	}
	if len(e.Pubs(EN)) != 0 || len(e.IndexUIDs()) != 0 {
		t.Errorf("a refused publish left rows %v or indexes %v", e.Pubs(EN), e.IndexUIDs())
	}
}

func TestEmptyPublicationsNeedAllowEmpty(t *testing.T) {
	e := publishtest.New(t)
	ctx := context.Background()

	// Nothing approved and nothing live: nothing to do, no row, no index.
	res, err := e.Publisher.Publish(ctx, EN, publish.PublishOptions{})
	if err != nil || res.Outcome != publish.OutcomeSkippedEmpty {
		t.Fatalf("empty first publish = %+v, %v", res, err)
	}
	if len(e.Pubs(EN)) != 0 || len(e.IndexUIDs()) != 0 {
		t.Fatalf("a skipped publish left rows %v or indexes %v", e.Pubs(EN), e.IndexUIDs())
	}

	s := seedWorld(e)
	r1 := e.Publish(EN)
	// The source files are deleted: their candidates drop out at the next publish.
	e.RemoveSource("kb/acme/web/faq.docx")
	e.RemoveSource("kb/acme/hours.docx")
	e.RemoveSource("kb/faq.docx")

	// Nothing may be removed by accident.
	if _, err := e.Publisher.Publish(ctx, EN, publish.PublishOptions{}); publish.CodeOf(err) != publish.CodeEmptyRefused {
		t.Fatalf("emptying publish without --allow-empty: %v", err)
	}
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, s.approved) || len(e.Pubs(EN)) != 1 {
		t.Fatalf("refused publish changed things: live %v pubs %+v", got, e.Pubs(EN))
	}

	res, err = e.Publisher.Publish(ctx, EN, publish.PublishOptions{AllowEmpty: true})
	if err != nil || res.Outcome != publish.OutcomeLive || res.Items != 0 {
		t.Fatalf("allow-empty publish = %+v, %v", res, err)
	}
	if got := e.DocIDs(e.Live(EN)); len(got) != 0 {
		t.Errorf("live index = %v, want empty", got)
	}
	if p := e.Pub(res.PublicationID); p.State != "LIVE" || p.ItemCount != 0 {
		t.Errorf("empty publication = %+v", p)
	}
	if p := e.Pub(r1.PublicationID); p.State != "SUPERSEDED" || len(e.DocIDs(p.ContentUID)) != 3 {
		t.Errorf("previous publication = %+v", p)
	}
	// Search over the empty index is NO_MATCH, not an error.
	r, err := e.Search(EN, "How do I reset my password?", nil, 3)
	if err != nil || r.Status != "NO_MATCH" || r.Items == nil || len(r.Items) != 0 {
		t.Errorf("search over empty index = %+v, %v", r, err)
	}
	// Publishing an empty set over an empty live index is not an emptying.
	if res, err := e.Publisher.Publish(ctx, EN, publish.PublishOptions{}); err != nil || res.Outcome != publish.OutcomeSkippedEmpty {
		t.Errorf("empty over empty = %+v, %v", res, err)
	}
	e.AssertContentUIDs(EN)
}

func TestDeletedSourceDropsOutAtTheNextPublish(t *testing.T) {
	e := publishtest.New(t)
	s := seedWorld(e)
	e.Publish(EN)
	e.RemoveSource("kb/acme/hours.docx")
	// Until the next publish the live index still serves the old content.
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, s.approved) {
		t.Fatalf("live changed before publish: %v", got)
	}
	r := e.Publish(EN)
	if r.Items != 2 {
		t.Fatalf("items = %d", r.Items)
	}
	want := publishtest.IDs(s.reset, s.cancel)
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, want) || len(got) != 2 {
		t.Errorf("live = %v, want %v", got, want)
	}
	// The removed candidate is not in the new publication's snapshot either.
	if got := e.ItemIDs(r.PublicationID); !slices.Equal(got, want) {
		t.Errorf("items = %v", got)
	}
	e.AssertContentUIDs(EN)
}

func TestSearchNeverReturnsContentOfAnUnpublishedVersion(t *testing.T) {
	e := publishtest.New(t)
	const key = "kb/policy.docx"
	v1 := e.AddCandidate(publishtest.Cand{Key: key, Language: EN, Question: "How long is the warranty?", Answer: "The warranty lasts one year."})
	e.Publish(EN)
	q := "How long is the warranty?"
	if got := e.SearchIDs(EN, q, nil, 3); !slices.Equal(got, publishtest.IDs(v1)) {
		t.Fatalf("v1 published: %v", got)
	}

	// The source changes; v2 is parsed, generated and approved but not published.
	e.NewVersion(key)
	v2 := e.AddCandidate(publishtest.Cand{Key: key, Language: EN, Question: "How long is the warranty period?", Answer: "The warranty lasts two years."})
	for i := 0; i < 2; i++ {
		got := e.SearchIDs(EN, q, nil, 3)
		if !slices.Equal(got, publishtest.IDs(v1)) {
			t.Fatalf("before publishing v2 the search returned %v, want only v1 %s", got, v1)
		}
	}
	r, _ := e.Search(EN, q, nil, 3)
	if strings.Contains(r.Items[0].Answer, "two") {
		t.Fatalf("v2 answer leaked: %+v", r.Items)
	}

	e.Publish(EN)
	for _, query := range []string{q, "How long is the warranty period?"} {
		got := e.SearchIDs(EN, query, nil, 3)
		if !slices.Equal(got, publishtest.IDs(v2)) || slices.Contains(got, v1.String()) {
			t.Fatalf("after publishing v2, %q returned %v, want only v2 %s (v1 is STALE)", query, got, v2)
		}
	}
	e.AssertContentUIDs(EN)
}

func TestRollbackOneStep(t *testing.T) {
	e := publishtest.New(t)
	ctx := context.Background()
	a := e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I reset my password?", Answer: "Use the forgot password link."})
	r1 := e.Publish(EN)
	b := e.AddCandidate(publishtest.Cand{Language: EN, Question: "What are your opening hours?"})
	r2 := e.Publish(EN)
	e.AssertContentUIDs(EN)

	rb, err := e.Publisher.Rollback(ctx, EN, nil)
	if err != nil || rb.Rebuilt || rb.From != r2.PublicationID || rb.To != r1.PublicationID || rb.Items != 1 {
		t.Fatalf("rollback = %+v, %v", rb, err)
	}
	if p1, p2 := e.Pub(r1.PublicationID), e.Pub(r2.PublicationID); p1.State != "LIVE" || p2.State != "SUPERSEDED" || p1.ContentUID != e.Live(EN) || p2.ContentUID == "" {
		t.Fatalf("after rollback: %+v %+v", p1, p2)
	}
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, publishtest.IDs(a)) || len(got) != 1 {
		t.Errorf("live = %v, want only the first publication's content", got)
	}
	if got := e.SearchIDs(EN, "What are your opening hours?", nil, 3); slices.Contains(got, b.String()) {
		t.Errorf("rolled-back content still served: %v", got)
	}
	e.AssertContentUIDs(EN)

	// The default target is now the most recently superseded one: back to P2.
	rb, err = e.Publisher.Rollback(ctx, EN, nil)
	if err != nil || rb.To != r2.PublicationID || rb.Rebuilt {
		t.Fatalf("second rollback = %+v, %v", rb, err)
	}
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, publishtest.IDs(a, b)) {
		t.Errorf("live = %v", got)
	}
	e.AssertContentUIDs(EN)
}

func TestRollbackTwoStepsToAnOlderPublication(t *testing.T) {
	e := publishtest.New(t)
	ctx := context.Background()
	var ids []uuid.UUID
	var pubs []uuid.UUID
	for i, q := range []string{"How do I reset my password?", "What are your opening hours?", "How can I cancel my subscription?"} {
		_ = i
		ids = append(ids, e.AddCandidate(publishtest.Cand{Language: EN, Question: q}))
		pubs = append(pubs, e.Publish(EN).PublicationID)
	}
	e.AssertContentUIDs(EN)

	rb, err := e.Publisher.Rollback(ctx, EN, &pubs[0])
	if err != nil || rb.Rebuilt || rb.From != pubs[2] || rb.To != pubs[0] {
		t.Fatalf("rollback = %+v, %v", rb, err)
	}
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, publishtest.IDs(ids[0])) || len(got) != 1 {
		t.Errorf("live = %v, want the first publication's content only", got)
	}
	states := []string{}
	for _, p := range pubs {
		states = append(states, e.Pub(p).State)
	}
	if !slices.Equal(states, []string{"LIVE", "SUPERSEDED", "SUPERSEDED"}) {
		t.Errorf("states = %v", states)
	}
	e.AssertContentUIDs(EN)

	// And forward again to the newest.
	if _, err := e.Publisher.Rollback(ctx, EN, &pubs[2]); err != nil {
		t.Fatal(err)
	}
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, publishtest.IDs(ids...)) || len(got) != 3 {
		t.Errorf("live = %v", got)
	}
	e.AssertContentUIDs(EN)
}

func TestRollbackToAPrunedPublicationRebuildsItFromTheSnapshot(t *testing.T) {
	e := publishtest.New(t)
	e.Publisher.RetainIndexes = 1
	ctx := context.Background()
	c1 := e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I reset my password?", Alts: []string{"I forgot my password"}, Answer: "Use the forgot password link."})
	p1 := e.Publish(EN).PublicationID
	p1Before := e.SearchIDs(EN, "I forgot my password", nil, 3)
	hitBefore, _ := e.Search(EN, "How do I reset my password?", nil, 3)
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "What are your opening hours?"})
	p2 := e.Publish(EN).PublicationID
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How can I cancel my subscription?"})
	r3 := e.Publish(EN)
	p3 := r3.PublicationID

	if !slices.Equal(pruned(r3.Pruned), pruned([]uuid.UUID{p1})) || len(r3.Pruned) != 1 {
		t.Fatalf("pruned = %v, want [%s]", r3.Pruned, p1)
	}
	if pp := e.Pub(p1); pp.State != "SUPERSEDED" || pp.ContentUID != "" {
		t.Fatalf("pruned publication = %+v", pp)
	}
	if pp := e.Pub(p2); pp.ContentUID == "" {
		t.Fatalf("retained publication lost its index: %+v", pp)
	}
	if got := e.IndexUIDs(); len(got) != 2 {
		t.Fatalf("indexes = %v, want live + 1 retained", got)
	}
	e.AssertContentUIDs(EN)

	// The candidates change after the publication; the rebuild must use the snapshot.
	if _, err := e.Store.Pool.Exec(ctx, `UPDATE candidates SET state = 'STALE', question = 'EDITED AFTER PUBLISH' WHERE id = $1`, c1); err != nil {
		t.Fatal(err)
	}

	rb, err := e.Publisher.Rollback(ctx, EN, &p1)
	if err != nil || !rb.Rebuilt || rb.To != p1 || rb.From != p3 {
		t.Fatalf("rollback = %+v, %v", rb, err)
	}
	if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, publishtest.IDs(c1)) || len(got) != 1 {
		t.Errorf("rebuilt live = %v", got)
	}
	// Same content as when it was first published.
	if got := e.SearchIDs(EN, "I forgot my password", nil, 3); !slices.Equal(got, p1Before) {
		t.Errorf("rebuilt search = %v, want %v", got, p1Before)
	}
	hitAfter, _ := e.Search(EN, "How do I reset my password?", nil, 3)
	if len(hitAfter.Items) != 1 || hitAfter.Items[0].Question != hitBefore.Items[0].Question || hitAfter.Items[0].Answer != hitBefore.Items[0].Answer ||
		hitAfter.Items[0].SourceRef != hitBefore.Items[0].SourceRef || hitAfter.Items[0].Question != "How do I reset my password?" {
		t.Errorf("rebuilt item %+v differs from the original %+v", hitAfter.Items, hitBefore.Items)
	}
	if s := hitAfter.Items[0].Score - hitBefore.Items[0].Score; s > 1e-4 || s < -1e-4 {
		t.Errorf("score drifted by %f", s)
	}
	for _, id := range []uuid.UUID{p1, p2, p3} {
		_ = e.Pub(id)
	}
	if got := e.Pub(p3); got.State != "SUPERSEDED" || got.ContentUID == "" {
		t.Errorf("the demoted publication = %+v", got)
	}
	e.AssertContentUIDs(EN)
}

func pruned(ids []uuid.UUID) []string { return publishtest.IDs(ids...) }

func TestRetentionKeepsTheNewestAndKeepsTheRows(t *testing.T) {
	for _, retain := range []int{0, 2} {
		t.Run("retain="+string(rune('0'+retain)), func(t *testing.T) {
			e := publishtest.New(t)
			e.Publisher.RetainIndexes = retain
			var pubs []uuid.UUID
			for _, q := range []string{"one question about passwords", "two question about hours", "three question about billing", "four question about shipping"} {
				e.AddCandidate(publishtest.Cand{Language: EN, Question: q})
				pubs = append(pubs, e.Publish(EN).PublicationID)
			}
			// P4 live, P3..P1 superseded; the newest `retain` keep their index.
			if got := len(e.Pubs(EN)); got != 4 {
				t.Fatalf("rows = %d, want all 4 kept", got)
			}
			if got := e.IndexUIDs(); len(got) != 1+retain {
				t.Errorf("indexes = %v, want live + %d", got, retain)
			}
			for i, p := range pubs[:3] {
				pp := e.Pub(p)
				kept := i >= 3-retain
				if (pp.ContentUID != "") != kept || pp.State != "SUPERSEDED" {
					t.Errorf("P%d = %+v, kept=%v", i+1, pp, kept)
				}
				if n := len(e.ItemIDs(p)); n != i+1 {
					t.Errorf("P%d keeps %d items, want %d (items are kept forever)", i+1, n, i+1)
				}
			}
			e.AssertContentUIDs(EN)
		})
	}
}

func TestRollbackRefusals(t *testing.T) {
	e := publishtest.New(t)
	ctx := context.Background()
	if _, err := e.Publisher.Rollback(ctx, EN, nil); publish.CodeOf(err) != publish.CodeNoLivePublication {
		t.Errorf("no live: %v", err)
	}
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I reset my password?"})
	p1 := e.Publish(EN).PublicationID
	if _, err := e.Publisher.Rollback(ctx, EN, nil); publish.CodeOf(err) != publish.CodeNoRollbackTarget {
		t.Errorf("no target: %v", err)
	}
	if _, err := e.Publisher.Rollback(ctx, EN, &p1); publish.CodeOf(err) != publish.CodeRollbackTargetBad {
		t.Errorf("rollback to the LIVE publication: %v", err)
	}
	unknown := uuid.New()
	if _, err := e.Publisher.Rollback(ctx, EN, &unknown); publish.CodeOf(err) != publish.CodeRollbackTargetBad {
		t.Errorf("unknown target: %v", err)
	}
	e.AddCandidate(publishtest.Cand{Language: ZH, Question: "如何重置密码？"})
	zh := e.Publish(ZH).PublicationID
	e.AddCandidate(publishtest.Cand{Language: ZH, Question: "营业时间是什么？"})
	e.Publish(ZH)
	if _, err := e.Publisher.Rollback(ctx, EN, &zh); publish.CodeOf(err) != publish.CodeRollbackTargetBad {
		t.Errorf("target of another language: %v", err)
	}
	// A FAILED publication can never be rolled back to.
	e.Publisher.Hooks.BeforeSwap = func(context.Context, string) error { return errors.New("injected") }
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "Do you have a mobile app?"})
	_, _ = e.Publisher.Publish(ctx, EN, publish.PublishOptions{})
	e.Publisher.Hooks = publish.Hooks{}
	var failed uuid.UUID
	for _, p := range e.Pubs(EN) {
		if p.State == "FAILED" {
			failed = p.ID
		}
	}
	if _, err := e.Publisher.Rollback(ctx, EN, &failed); publish.CodeOf(err) != publish.CodeRollbackTargetBad {
		t.Errorf("rollback to FAILED: %v", err)
	}
	if got := e.Pub(p1).State; got != "LIVE" {
		t.Errorf("refusals changed state: %s", got)
	}
	e.AssertContentUIDs(EN)
}

func TestRollbackFailureLeavesEverythingAsItWas(t *testing.T) {
	e := publishtest.New(t)
	ctx := context.Background()
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I reset my password?"})
	p1 := e.Publish(EN).PublicationID
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "What are your opening hours?"})
	p2 := e.Publish(EN).PublicationID
	live := e.DocIDs(e.Live(EN))
	for name, hooks := range map[string]publish.Hooks{
		"before swap":    {BeforeSwap: func(context.Context, string) error { return errors.New("injected") }},
		"commit failure": {AfterSwap: func(context.Context) error { return errors.New("injected") }},
	} {
		e.Publisher.Hooks = hooks
		if _, err := e.Publisher.Rollback(ctx, EN, &p1); err == nil {
			t.Fatalf("%s: rollback succeeded", name)
		}
		if got := e.DocIDs(e.Live(EN)); !slices.Equal(got, live) || e.Pub(p2).State != "LIVE" || e.Pub(p1).State != "SUPERSEDED" {
			t.Errorf("%s: live %v states %s/%s", name, got, e.Pub(p1).State, e.Pub(p2).State)
		}
		e.AssertContentUIDs(EN)
	}
	// A rebuild that fails leaves no stray index either.
	e.Publisher.Hooks = publish.Hooks{}
	e.Publisher.RetainIndexes = 0
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How can I cancel my subscription?"})
	e.Publish(EN) // prunes P1's index (retain 0)
	e.Publisher.Hooks.BeforeSwap = func(context.Context, string) error { return errors.New("injected") }
	before := e.IndexUIDs()
	if _, err := e.Publisher.Rollback(ctx, EN, &p1); err == nil {
		t.Fatal("rebuild rollback succeeded")
	}
	if got := e.IndexUIDs(); !slices.Equal(got, before) {
		t.Errorf("indexes = %v, want %v", got, before)
	}
	e.AssertContentUIDs(EN)
}

func TestOnePublishAtATimePerLanguage(t *testing.T) {
	e := publishtest.New(t)
	ctx := context.Background()
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I reset my password?"})
	e.AddCandidate(publishtest.Cand{Language: ZH, Question: "如何重置密码？"})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.Publisher.Hooks.BeforeSwap = func(context.Context, string) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.Publisher.Publish(ctx, EN, publish.PublishOptions{})
		done <- err
	}()
	<-entered
	if _, err := e.Publisher.Publish(ctx, EN, publish.PublishOptions{}); publish.CodeOf(err) != publish.CodeInProgress {
		t.Errorf("concurrent publish: %v", err)
	}
	if _, err := e.Publisher.Rollback(ctx, EN, nil); publish.CodeOf(err) != publish.CodeInProgress {
		t.Errorf("concurrent rollback: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The other language was never blocked.
	if _, err := e.Publisher.Publish(ctx, ZH, publish.PublishOptions{}); err != nil {
		t.Errorf("zh publish: %v", err)
	}
	if len(e.Pubs(EN)) != 1 {
		t.Errorf("publications = %+v", e.Pubs(EN))
	}
}

func TestADeadRunsBuildingRowIsAbandonedAtTheNextPublish(t *testing.T) {
	e := publishtest.New(t)
	ctx := context.Background()
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I reset my password?"})
	stale := e.Live(EN) + "_deadbeef0000"
	var id uuid.UUID
	if err := e.Store.Pool.QueryRow(ctx, `INSERT INTO publications (language, index_uid) VALUES ('EN', $1) RETURNING id`, stale).Scan(&id); err != nil {
		t.Fatal(err)
	}
	cid, err := e.Meili.CreateIndex(ctx, stale, "id")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Meili.WaitTask(ctx, cid); err != nil {
		t.Fatal(err)
	}
	r := e.Publish(EN)
	if p := e.Pub(id); p.State != "FAILED" || p.ErrorCode != string(publish.CodeAbandoned) {
		t.Errorf("abandoned row = %+v", p)
	}
	if ok, _ := e.Meili.IndexExists(ctx, stale); ok {
		t.Error("the abandoned staging index was not deleted")
	}
	if r.Items != 1 || e.Pub(r.PublicationID).State != "LIVE" {
		t.Errorf("publish = %+v", r)
	}
	e.AssertContentUIDs(EN)
}

func TestAnIndexThatDoesNotHoldThePublicationIsRebuiltNotTrusted(t *testing.T) {
	e := publishtest.New(t)
	ctx := context.Background()
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "How do I reset my password?"})
	p1 := e.Publish(EN).PublicationID
	e.AddCandidate(publishtest.Cand{Language: EN, Question: "What are your opening hours?"})
	e.Publish(EN)
	// Corrupt the retained index: the claim in content_uid is now wrong.
	uid := e.Pub(p1).ContentUID
	id, err := e.Meili.DeleteIndex(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Meili.WaitTask(ctx, id); err != nil {
		t.Fatal(err)
	}
	rb, err := e.Publisher.Rollback(ctx, EN, &p1)
	if err != nil || !rb.Rebuilt {
		t.Fatalf("rollback = %+v, %v", rb, err)
	}
	if got := e.DocIDs(e.Live(EN)); len(got) != 1 {
		t.Errorf("live = %v", got)
	}
	e.AssertContentUIDs(EN)
}
