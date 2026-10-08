// SPDX-License-Identifier: Apache-2.0

package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rasonyang/aicc-knowledge/internal/jobs"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/testdb"
)

func open(t *testing.T) (*store.Store, *jobs.Queue) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, testdb.ScratchDSN(t, "jobs"), 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	q := jobs.New(s.Queries)
	q.BackoffBase = time.Hour // a failed job must not become claimable by accident
	q.BackoffCap = 24 * time.Hour
	return s, q
}

func add(t *testing.T, q *jobs.Queue, e jobs.Enqueue) {
	t.Helper()
	created, err := q.Add(context.Background(), e)
	if err != nil || !created {
		t.Fatalf("Add = %v, %v", created, err)
	}
}

func TestEnqueueIsIdempotentPerKindAndDedupeKey(t *testing.T) {
	ctx := context.Background()
	s, q := open(t)
	e := jobs.Enqueue{Kind: jobs.KindParse, DedupeKey: "v1", Payload: map[string]string{"fileVersionId": "v1"}}
	add(t, q, e)
	if created, err := q.Add(ctx, e); err != nil || created {
		t.Fatalf("second Add = %v, %v; want false, nil", created, err)
	}
	// Another key and another kind with the same key are distinct jobs.
	add(t, q, jobs.Enqueue{Kind: jobs.KindParse, DedupeKey: "v2"})
	add(t, q, jobs.Enqueue{Kind: jobs.KindGenerate, DedupeKey: "v1"})
	// Without a key nothing is deduplicated.
	add(t, q, jobs.Enqueue{Kind: jobs.KindParse})
	add(t, q, jobs.Enqueue{Kind: jobs.KindParse})
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("jobs = %d, %v; want 5", n, err)
	}
}

func TestEnqueueRejectsUnknownKind(t *testing.T) {
	_, q := open(t)
	_, err := q.Add(context.Background(), jobs.Enqueue{Kind: "NOPE"})
	if !errors.Is(err, jobs.ErrUnknownJobKind) {
		t.Fatalf("err = %v, want UNKNOWN_JOB_KIND", err)
	}
	if _, err := q.Claim(context.Background(), "w", time.Minute, "NOPE"); !errors.Is(err, jobs.ErrUnknownJobKind) {
		t.Fatalf("Claim err = %v, want UNKNOWN_JOB_KIND", err)
	}
}

func TestConcurrentClaimersNeverGetTheSameJob(t *testing.T) {
	ctx := context.Background()
	_, q := open(t)
	const jobsN, workers = 60, 12
	for i := range jobsN {
		add(t, q, jobs.Enqueue{Kind: jobs.KindParse, DedupeKey: fmt.Sprint(i)})
	}
	var mu sync.Mutex
	claimed := map[uuid.UUID]string{}
	var dup []string
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("worker-%d", w)
			for {
				j, err := q.Claim(ctx, name, time.Minute, jobs.KindParse)
				if errors.Is(err, jobs.ErrNoJob) {
					return
				}
				if err != nil {
					errs <- err
					return
				}
				mu.Lock()
				if prev, ok := claimed[j.ID]; ok {
					dup = append(dup, fmt.Sprintf("%s claimed by %s and %s", j.ID, prev, name))
				}
				claimed[j.ID] = name
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(dup) != 0 {
		t.Fatalf("jobs claimed twice: %v", dup)
	}
	if len(claimed) != jobsN {
		t.Fatalf("claimed %d distinct jobs, want %d", len(claimed), jobsN)
	}
}

func TestClaimOnlyTakesRequestedKindsAndDueJobs(t *testing.T) {
	ctx := context.Background()
	_, q := open(t)
	add(t, q, jobs.Enqueue{Kind: jobs.KindGenerate})
	add(t, q, jobs.Enqueue{Kind: jobs.KindParse, RunAfter: time.Now().Add(time.Hour)})
	if _, err := q.Claim(ctx, "w", time.Minute, jobs.KindParse); !errors.Is(err, jobs.ErrNoJob) {
		t.Fatalf("Claim = %v, want ErrNoJob (wrong kind, or not due)", err)
	}
	j, err := q.Claim(ctx, "w", time.Minute, jobs.KindGenerate)
	if err != nil || j.Kind != jobs.KindGenerate || j.Attempts != 1 {
		t.Fatalf("Claim = %+v, %v", j, err)
	}
}

func TestExpiredLeaseIsReclaimedAndALateWorkerCannotComplete(t *testing.T) {
	ctx := context.Background()
	s, q := open(t)
	add(t, q, jobs.Enqueue{Kind: jobs.KindParse, DedupeKey: "a"})
	j1, err := q.Claim(ctx, "slow", time.Minute, jobs.KindParse)
	if err != nil {
		t.Fatal(err)
	}
	// While the lease is live nobody else can take it.
	if _, err := q.Claim(ctx, "other", time.Minute, jobs.KindParse); !errors.Is(err, jobs.ErrNoJob) {
		t.Fatalf("claim during live lease = %v, want ErrNoJob", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET locked_until = now() - interval '1 second' WHERE id = $1`, j1.ID); err != nil {
		t.Fatal(err)
	}
	j2, err := q.Claim(ctx, "fast", time.Minute, jobs.KindParse)
	if err != nil {
		t.Fatal(err)
	}
	if j2.ID != j1.ID || j2.Attempts != 2 {
		t.Fatalf("reclaimed job = %+v, want %s attempt 2", j2, j1.ID)
	}
	if err := q.Complete(ctx, j1.ID, "slow"); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("late Complete = %v, want LEASE_LOST", err)
	}
	if err := q.Complete(ctx, j2.ID, "fast"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.Pool.QueryRow(ctx, `SELECT state FROM jobs WHERE id = $1`, j1.ID).Scan(&state); err != nil || state != "SUCCEEDED" {
		t.Fatalf("state = %q, %v", state, err)
	}
}

func TestExpiredLeaseOnLastAttemptFails(t *testing.T) {
	ctx := context.Background()
	s, q := open(t)
	add(t, q, jobs.Enqueue{Kind: jobs.KindParse, MaxAttempts: 1})
	j, err := q.Claim(ctx, "w", time.Minute, jobs.KindParse)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET locked_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if n, err := q.Reclaim(ctx); err != nil || n != 1 {
		t.Fatalf("Reclaim = %d, %v", n, err)
	}
	var state, code string
	if err := s.Pool.QueryRow(ctx, `SELECT state, last_error_code FROM jobs WHERE id = $1`, j.ID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	if state != "FAILED" || code != string(jobs.CodeLeaseExpired) {
		t.Fatalf("state=%s code=%s, want FAILED LEASE_EXPIRED", state, code)
	}
}

func TestFailRetriesWithBackoffThenFailsAtMaxAttempts(t *testing.T) {
	ctx := context.Background()
	s, q := open(t)
	add(t, q, jobs.Enqueue{Kind: jobs.KindParse, MaxAttempts: 3})
	id := uuid.Nil
	for attempt := 1; attempt <= 3; attempt++ {
		// Make the previous failure's backoff elapse.
		if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET run_after = now()`); err != nil {
			t.Fatal(err)
		}
		j, err := q.Claim(ctx, "w", time.Minute, jobs.KindParse)
		if err != nil || j.Attempts != attempt {
			t.Fatalf("attempt %d: Claim = %+v, %v", attempt, j, err)
		}
		id = j.ID
		if err := q.Fail(ctx, j.ID, "w", "BOOM", "it broke"); err != nil {
			t.Fatal(err)
		}
		var state string
		var delay float64
		if err := s.Pool.QueryRow(ctx, `SELECT state, extract(epoch FROM run_after - now()) FROM jobs WHERE id = $1`, id).Scan(&state, &delay); err != nil {
			t.Fatal(err)
		}
		if attempt < 3 {
			// base is one hour: 1h after attempt 1, 2h after attempt 2.
			want := float64(3600 * (int(1) << (attempt - 1)))
			if state != "QUEUED" || delay < want-60 || delay > want+60 {
				t.Fatalf("attempt %d: state=%s delay=%.0fs, want QUEUED ~%.0fs", attempt, state, delay, want)
			}
			if _, err := q.Claim(ctx, "w", time.Minute, jobs.KindParse); !errors.Is(err, jobs.ErrNoJob) {
				t.Fatalf("job claimable during backoff: %v", err)
			}
		} else if state != "FAILED" {
			t.Fatalf("after max attempts state = %s, want FAILED", state)
		}
	}
	var code, msg string
	var finished bool
	if err := s.Pool.QueryRow(ctx, `SELECT last_error_code, last_error_message, finished_at IS NOT NULL FROM jobs WHERE id = $1`, id).Scan(&code, &msg, &finished); err != nil {
		t.Fatal(err)
	}
	if code != "BOOM" || msg != "it broke" || !finished {
		t.Fatalf("code=%q msg=%q finished=%v", code, msg, finished)
	}
	// A FAILED job is never claimed again.
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET run_after = now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Claim(ctx, "w", time.Minute, jobs.KindParse); !errors.Is(err, jobs.ErrNoJob) {
		t.Fatalf("FAILED job claimed: %v", err)
	}
}

func TestRearmResetsAFinishedJobInsteadOfInsertingAnother(t *testing.T) {
	ctx := context.Background()
	s, q := open(t)
	e := jobs.Enqueue{Kind: jobs.KindParse, DedupeKey: "v1", MaxAttempts: 1}
	add(t, q, e)
	j, err := q.Claim(ctx, "w", time.Minute, jobs.KindParse)
	if err != nil {
		t.Fatal(err)
	}
	// A RUNNING job is left alone: nothing is reset or inserted.
	if ok, err := q.Rearm(ctx, e); err != nil || ok {
		t.Fatalf("Rearm of a RUNNING job = %v, %v", ok, err)
	}
	if err := q.Fail(ctx, j.ID, "w", "BOOM", "broke"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.Pool.QueryRow(ctx, `SELECT state FROM jobs WHERE id = $1`, j.ID).Scan(&state); err != nil || state != "FAILED" {
		t.Fatalf("state = %q, %v", state, err)
	}

	if ok, err := q.Rearm(ctx, e); err != nil || !ok {
		t.Fatalf("Rearm = %v, %v", ok, err)
	}
	var attempts int
	var code *string
	var finished bool
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT state, attempts, last_error_code, finished_at IS NOT NULL FROM jobs WHERE id = $1`, j.ID).Scan(&state, &attempts, &code, &finished); err != nil {
		t.Fatal(err)
	}
	if state != "QUEUED" || attempts != 0 || code != nil || finished {
		t.Fatalf("job = %s attempts=%d code=%v finished=%v", state, attempts, code, finished)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs = %d, %v, want the same single row", n, err)
	}
	j2, err := q.Claim(ctx, "w", time.Minute, jobs.KindParse)
	if err != nil || j2.ID != j.ID || j2.Attempts != 1 {
		t.Fatalf("claim after rearm = %+v, %v", j2, err)
	}

	// With no row at all, Rearm enqueues one.
	if ok, err := q.Rearm(ctx, jobs.Enqueue{Kind: jobs.KindParse, DedupeKey: "v2"}); err != nil || !ok {
		t.Fatalf("Rearm without a row = %v, %v", ok, err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("jobs = %d, %v, want 2", n, err)
	}
	if _, err := q.Rearm(ctx, jobs.Enqueue{Kind: jobs.KindParse}); err == nil {
		t.Fatal("Rearm without a dedupe key must fail")
	}
}

func TestExtendKeepsALongJobFromBeingReclaimed(t *testing.T) {
	ctx := context.Background()
	s, q := open(t)
	add(t, q, jobs.Enqueue{Kind: jobs.KindGenerate, DedupeKey: "a"})
	j, err := q.Claim(ctx, "w", time.Minute, jobs.KindGenerate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET locked_until = now() + interval '1 second' WHERE id = $1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Extend(ctx, j.ID, "w", time.Hour); err != nil {
		t.Fatal(err)
	}
	var remaining float64
	if err := s.Pool.QueryRow(ctx, `SELECT extract(epoch FROM locked_until - now()) FROM jobs WHERE id = $1`, j.ID).Scan(&remaining); err != nil || remaining < 3000 {
		t.Fatalf("remaining lease = %v, %v", remaining, err)
	}
	if err := q.Extend(ctx, j.ID, "someone-else", time.Hour); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("Extend by a stranger = %v, want LEASE_LOST", err)
	}
}
