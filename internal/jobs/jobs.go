// SPDX-License-Identifier: Apache-2.0

// Package jobs is the work queue: one PostgreSQL table claimed with FOR UPDATE
// SKIP LOCKED. A job is QUEUED, is claimed with a lease (RUNNING), and ends
// SUCCEEDED or FAILED; a failed attempt goes back to QUEUED with exponential
// backoff until max_attempts is used up, and a RUNNING job whose lease expired
// is reclaimed the same way.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// Kind identifies the handler of a job. Kinds are registered here, in code,
// by the milestone that introduces them; the table has no CHECK on kind.
type Kind string

// Job kinds.
const (
	KindParse    Kind = "PARSE"    // payload {"fileVersionId": uuid}; dedupe key = the file version id
	KindGenerate Kind = "GENERATE" // payload {"fileVersionId": uuid}; dedupe key = the file version id
)

// Kinds returns every registered kind.
func Kinds() []Kind { return []Kind{KindParse, KindGenerate} }

// Valid reports whether k is registered.
func (k Kind) Valid() bool {
	for _, v := range Kinds() {
		if v == k {
			return true
		}
	}
	return false
}

// State is the state of a job row.
type State string

// Job states. QUEUED -> RUNNING -> SUCCEEDED | FAILED, and RUNNING -> QUEUED
// for a retry or an expired lease.
const (
	StateQueued    State = "QUEUED"
	StateRunning   State = "RUNNING"
	StateSucceeded State = "SUCCEEDED"
	StateFailed    State = "FAILED"
)

// ErrorCode is a machine-readable queue failure identifier.
type ErrorCode string

// Error codes.
const (
	CodeUnknownJobKind ErrorCode = "UNKNOWN_JOB_KIND"
	CodeLeaseLost      ErrorCode = "LEASE_LOST"
	// CodeLeaseExpired is stored in last_error_code when a lease ran out.
	CodeLeaseExpired ErrorCode = "LEASE_EXPIRED"
)

// Error is a coded queue error.
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Msg }

// Is matches any *Error with the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrUnknownJobKind = &Error{Code: CodeUnknownJobKind}
	ErrLeaseLost      = &Error{Code: CodeLeaseLost}
)

// ErrNoJob is returned by Claim when nothing is claimable right now.
var ErrNoJob = errors.New("no claimable job")

// Defaults.
const (
	DefaultMaxAttempts = 3
	DefaultLease       = 5 * time.Minute
	DefaultBackoffBase = 30 * time.Second
	DefaultBackoffCap  = 30 * time.Minute
)

// Job is a claimed job.
type Job struct {
	ID          uuid.UUID
	Kind        Kind
	Payload     json.RawMessage
	Attempts    int
	MaxAttempts int
}

// Enqueue describes a new job.
type Enqueue struct {
	Kind Kind
	// Payload is marshalled to JSON; nil means {}.
	Payload any
	// DedupeKey makes the enqueue idempotent per (Kind, DedupeKey), across all
	// states. Empty means the job is never deduplicated.
	DedupeKey string
	// MaxAttempts defaults to DefaultMaxAttempts.
	MaxAttempts int
	// RunAfter delays the first claim; zero means now.
	RunAfter time.Time
}

// Queue operates on the jobs table. Build it over a pool or a transaction.
type Queue struct {
	q           *queries.Queries
	BackoffBase time.Duration
	BackoffCap  time.Duration
}

// New returns a queue over q (use queries.WithTx to enqueue inside a
// transaction).
func New(q *queries.Queries) *Queue {
	return &Queue{q: q, BackoffBase: DefaultBackoffBase, BackoffCap: DefaultBackoffCap}
}

// Add inserts a job. It reports whether a row was created; false means the
// dedupe key already exists.
func (qu *Queue) Add(ctx context.Context, e Enqueue) (created bool, err error) {
	if !e.Kind.Valid() {
		return false, &Error{Code: CodeUnknownJobKind, Msg: fmt.Sprintf("kind %q is not registered", e.Kind)}
	}
	payload := []byte("{}")
	if e.Payload != nil {
		if payload, err = json.Marshal(e.Payload); err != nil {
			return false, fmt.Errorf("marshal job payload: %w", err)
		}
	}
	p := queries.EnqueueJobParams{Kind: string(e.Kind), Payload: payload, MaxAttempts: int32(DefaultMaxAttempts)}
	if e.MaxAttempts > 0 {
		p.MaxAttempts = int32(e.MaxAttempts)
	}
	if e.DedupeKey != "" {
		p.DedupeKey = &e.DedupeKey
	}
	if !e.RunAfter.IsZero() {
		p.RunAfter = pgtype.Timestamptz{Time: e.RunAfter, Valid: true}
	}
	n, err := qu.q.EnqueueJob(ctx, p)
	if err != nil {
		return false, fmt.Errorf("enqueue %s job: %w", e.Kind, err)
	}
	return n == 1, nil
}

// Reclaim returns jobs whose lease expired to QUEUED, or to FAILED when they
// have no attempts left. It reports how many rows it moved.
func (qu *Queue) Reclaim(ctx context.Context) (int64, error) {
	n, err := qu.q.ReclaimExpiredJobs(ctx)
	if err != nil {
		return 0, fmt.Errorf("reclaim expired jobs: %w", err)
	}
	return n, nil
}

// Claim reclaims expired leases, then takes one claimable job of the given
// kinds for worker, with a lease of the given length. It returns ErrNoJob when
// there is none. Concurrent claimers never receive the same job.
func (qu *Queue) Claim(ctx context.Context, worker string, lease time.Duration, kinds ...Kind) (Job, error) {
	if len(kinds) == 0 {
		return Job{}, &Error{Code: CodeUnknownJobKind, Msg: "Claim needs at least one kind"}
	}
	ks := make([]string, len(kinds))
	for i, k := range kinds {
		if !k.Valid() {
			return Job{}, &Error{Code: CodeUnknownJobKind, Msg: fmt.Sprintf("kind %q is not registered", k)}
		}
		ks[i] = string(k)
	}
	if _, err := qu.Reclaim(ctx); err != nil {
		return Job{}, err
	}
	row, err := qu.q.ClaimJob(ctx, queries.ClaimJobParams{Worker: worker, LeaseSeconds: lease.Seconds(), Kinds: ks})
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNoJob
	}
	if err != nil {
		return Job{}, fmt.Errorf("claim job: %w", err)
	}
	return Job{ID: row.ID, Kind: Kind(row.Kind), Payload: row.Payload, Attempts: int(row.Attempts), MaxAttempts: int(row.MaxAttempts)}, nil
}

// Complete marks a claimed job SUCCEEDED. It fails with ErrLeaseLost when the
// worker no longer holds the job.
func (qu *Queue) Complete(ctx context.Context, id uuid.UUID, worker string) error {
	n, err := qu.q.CompleteJob(ctx, queries.CompleteJobParams{ID: id, Worker: worker})
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	if n == 0 {
		return &Error{Code: CodeLeaseLost, Msg: "job " + id.String() + " is not running under " + worker}
	}
	return nil
}

// Extend pushes the lease of a claimed job to now plus lease. A long job calls it
// between units of work. It fails with ErrLeaseLost when the worker no longer
// holds the job.
func (qu *Queue) Extend(ctx context.Context, id uuid.UUID, worker string, lease time.Duration) error {
	n, err := qu.q.ExtendJobLease(ctx, queries.ExtendJobLeaseParams{ID: id, Worker: worker, LeaseSeconds: lease.Seconds()})
	if err != nil {
		return fmt.Errorf("extend job lease: %w", err)
	}
	if n == 0 {
		return &Error{Code: CodeLeaseLost, Msg: "job " + id.String() + " is not running under " + worker}
	}
	return nil
}

// Fail records a failed attempt: the job is re-queued with exponential
// backoff, or becomes FAILED once max_attempts is used up.
func (qu *Queue) Fail(ctx context.Context, id uuid.UUID, worker, code, message string) error {
	n, err := qu.q.FailJob(ctx, queries.FailJobParams{
		ID: id, Worker: worker, ErrorCode: code, ErrorMessage: message,
		BaseSeconds: qu.BackoffBase.Seconds(), CapSeconds: qu.BackoffCap.Seconds(),
	})
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	if n == 0 {
		return &Error{Code: CodeLeaseLost, Msg: "job " + id.String() + " is not running under " + worker}
	}
	return nil
}

// Rearm is the operator retry for a job with a dedupe key: a SUCCEEDED or
// FAILED row is put back to QUEUED with attempts reset to 0 and its error
// cleared, instead of inserting a second row (the dedupe index covers every
// state). A QUEUED or RUNNING row is left alone. When no row exists at all the
// job is enqueued from e. It reports whether the queue now holds a new
// opportunity to run (a row was reset or created).
func (qu *Queue) Rearm(ctx context.Context, e Enqueue) (bool, error) {
	if e.DedupeKey == "" {
		return false, &Error{Code: CodeUnknownJobKind, Msg: "Rearm needs a dedupe key"}
	}
	if !e.Kind.Valid() {
		return false, &Error{Code: CodeUnknownJobKind, Msg: fmt.Sprintf("kind %q is not registered", e.Kind)}
	}
	n, err := qu.q.RearmJob(ctx, queries.RearmJobParams{Kind: string(e.Kind), DedupeKey: e.DedupeKey})
	if err != nil {
		return false, fmt.Errorf("rearm %s job: %w", e.Kind, err)
	}
	if n == 1 {
		return true, nil
	}
	return qu.Add(ctx, e)
}
