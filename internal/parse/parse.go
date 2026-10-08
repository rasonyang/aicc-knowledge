// SPDX-License-Identifier: Apache-2.0

// Package parse is the PARSE job worker: it turns a DISCOVERED file version
// into parsed sections (and fact rows) and moves it to PARSED, or to
// PARSE_FAILED with a coded reason.
//
// For each claimed job:
//
//   - the version must still be current and DISCOVERED, otherwise the job is
//     completed as skipped and nothing changes;
//   - the object is downloaded and hashed; if its SHA-256 is not the version's
//     (the object changed after the scan) the job is skipped, because bytes are
//     never parsed under a version they do not belong to. The next scan makes
//     the new version;
//   - .docx goes to internal/docx, .xlsx to internal/xlsx, and `*.facts.yaml`
//     is validated as a facts mapping;
//   - everything one version needs (sections, state, warnings, fact import,
//     job completion) is written in one transaction.
//
// Facts. The mapping of `<path>/<name>.xlsx` is `<path>/<name>.facts.yaml`. A
// workbook is imported with the current version of its mapping when that
// version is PARSED, and a mapping, once it parses, imports against the
// current workbook version when that is already PARSED. Whichever of the two is
// parsed last therefore performs the import, and the final transaction
// re-checks the counterpart under a lock (and retries the job's work if it
// moved), so the two can never both decide "the other is not ready". Sheets
// that a mapping covers are facts-only: they are not emitted as sections. An
// import problem (any xlsx.Issue, an invalid mapping, a table-name conflict)
// fails the version that triggered it and leaves the table UNAVAILABLE.
//
// Infrastructure errors (S3, database) do not change the version: the job goes
// back to the queue with backoff.
package parse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rasonyang/aicc-knowledge/internal/docx"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/facts"
	"github.com/rasonyang/aicc-knowledge/internal/jobs"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/s3store"
	"github.com/rasonyang/aicc-knowledge/internal/scan"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// Parse error codes stored in file_versions.parse_error_code. The parsers
// contribute DOCX_CORRUPT, XLSX_CORRUPT, UNSUPPORTED_FORMAT and MAPPING_INVALID
// (their own constants); these are the ones this package adds. The facts
// package adds FACT_TABLE_NAME_CONFLICT.
const (
	CodeFactsInvalid   = "FACTS_INVALID"
	CodeObjectTooLarge = scan.ParseErrorObjectTooLarge
)

// Job failure codes (jobs.last_error_code): an infrastructure problem, the
// version is untouched.
const (
	JobCodeRuntime         = "PARSE_RUNTIME_ERROR"
	JobCodeVersionNotFound = "FILE_VERSION_NOT_FOUND"
	JobCodeBadPayload      = "BAD_PAYLOAD"
)

// Metric label values: the kind of file and the outcome of a job.
const (
	KindDocx    = "DOCX"
	KindXlsx    = "XLSX"
	KindMapping = "FACTS_MAPPING"

	OutcomeParsed  = "PARSED"
	OutcomeFailed  = "PARSE_FAILED"
	OutcomeSkipped = "SKIPPED"
	OutcomeError   = "ERROR"
)

// Reasons a job is skipped.
const (
	SkipSuperseded  = "SUPERSEDED"
	SkipRemoved     = "REMOVED"
	SkipNotPending  = "NOT_DISCOVERED"
	SkipSHAMismatch = "SHA_MISMATCH"
	SkipNoObject    = "OBJECT_MISSING"
)

// Warning is one entry of file_versions.parse_warnings.
type Warning struct {
	Code     string `json:"code"`
	Detail   string `json:"detail"`
	Location string `json:"location"`
}

// Summary counts what one Run did. Claimed = Parsed + Failed + Skipped +
// Errors.
type Summary struct {
	Claimed int
	Parsed  int
	// Failed counts versions that ended PARSE_FAILED.
	Failed  int
	Skipped int
	// Errors counts jobs that hit an infrastructure error and went back to
	// the queue.
	Errors int
}

// RetrySummary counts what RetryFailed did.
type RetrySummary struct {
	Versions int
	// JobsQueued counts jobs put back in the queue (re-armed, or inserted
	// when the version had no job row).
	JobsQueued int
}

// Worker runs PARSE jobs.
type Worker struct {
	Store   *store.Store
	S3      *s3store.Client
	Metrics *obs.Metrics // optional
	Log     *slog.Logger // optional
	// MaxObjectBytes is the largest object the worker will hold in memory.
	MaxObjectBytes int64
	// Name identifies the worker in job leases; defaults to host and pid.
	Name string
	// Lease is the job lease; defaults to jobs.DefaultLease.
	Lease time.Duration
}

var (
	errSuperseded = errors.New("file version is no longer current or not DISCOVERED")
	errStale      = errors.New("the facts counterpart changed while parsing")
)

type jobError struct {
	code string
	err  error
}

func (e *jobError) Error() string { return e.code + ": " + e.err.Error() }
func (e *jobError) Unwrap() error { return e.err }

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

func (w *Worker) name() string {
	if w.Name != "" {
		return w.Name
	}
	host, _ := os.Hostname()
	return fmt.Sprintf("parse-%s-%d", host, os.Getpid())
}

func (w *Worker) lease() time.Duration {
	if w.Lease > 0 {
		return w.Lease
	}
	return jobs.DefaultLease
}

// Run claims and processes PARSE jobs until none is claimable. A job that
// fails for an infrastructure reason is re-queued with backoff and counted in
// Summary.Errors; Run itself fails only when the queue or the context does.
func (w *Worker) Run(ctx context.Context) (Summary, error) {
	var sum Summary
	queue := jobs.New(w.Store.Queries)
	for {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		job, err := queue.Claim(ctx, w.name(), w.lease(), jobs.KindParse)
		if errors.Is(err, jobs.ErrNoJob) {
			return sum, nil
		}
		if err != nil {
			return sum, err
		}
		sum.Claimed++
		start := time.Now()
		kind, outcome, err := w.process(ctx, queue, job)
		if err != nil {
			if ctx.Err() != nil {
				return sum, ctx.Err()
			}
			outcome = OutcomeError
			code := JobCodeRuntime
			var je *jobError
			if errors.As(err, &je) {
				code = je.code
			}
			w.log().Error("parse job failed", "job", job.ID, "code", code, "error", err)
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			if ferr := queue.Fail(fctx, job.ID, w.name(), code, err.Error()); ferr != nil {
				w.log().Error("could not record job failure", "job", job.ID, "error", ferr)
			}
			cancel()
		}
		switch outcome {
		case OutcomeParsed:
			sum.Parsed++
		case OutcomeFailed:
			sum.Failed++
		case OutcomeSkipped:
			sum.Skipped++
		default:
			sum.Errors++
		}
		if w.Metrics != nil {
			if kind == "" {
				kind = "UNKNOWN"
			}
			w.Metrics.ObserveParse(ctx, kind, outcome, time.Since(start))
		}
	}
}

type payload struct {
	FileVersionID uuid.UUID `json:"fileVersionId"`
}

// process handles one claimed job. It returns the kind and outcome; a non-nil
// error is an infrastructure error and the caller fails the job.
func (w *Worker) process(ctx context.Context, queue *jobs.Queue, job jobs.Job) (kind, outcome string, err error) {
	var p payload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.FileVersionID == uuid.Nil {
		return "", "", &jobError{JobCodeBadPayload, fmt.Errorf("payload %s has no fileVersionId", job.Payload)}
	}
	for range 3 {
		kind, outcome, err = w.attempt(ctx, queue, job, p.FileVersionID)
		if !errors.Is(err, errStale) {
			return kind, outcome, err
		}
		w.log().Info("facts counterpart moved, re-reading", "job", job.ID, "fileVersionId", p.FileVersionID)
	}
	return kind, outcome, err
}

func kindOf(f scan.Format) string {
	switch f {
	case scan.FormatDocx:
		return KindDocx
	case scan.FormatXlsx:
		return KindXlsx
	case scan.FormatFacts:
		return KindMapping
	}
	return "UNKNOWN"
}

// target is the version being parsed.
type target struct {
	ver  queries.GetFileVersionWithSourceRow
	kind string
}

func (w *Worker) attempt(ctx context.Context, queue *jobs.Queue, job jobs.Job, id uuid.UUID) (kind, outcome string, err error) {
	q := w.Store.Queries
	ver, err := q.GetFileVersionWithSource(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", &jobError{JobCodeVersionNotFound, fmt.Errorf("file version %s does not exist", id)}
	}
	if err != nil {
		return "", "", fmt.Errorf("load file version: %w", err)
	}
	format := scan.Classify(ver.ObjectKey)
	t := target{ver: ver, kind: kindOf(format)}
	skip := func(reason string) (string, string, error) {
		w.log().Info("parse job skipped", "job", job.ID, "fileVersionId", id, "key", ver.ObjectKey, "reason", reason)
		if err := queue.Complete(ctx, job.ID, w.name()); err != nil {
			return t.kind, "", err
		}
		return t.kind, OutcomeSkipped, nil
	}
	switch state := domain.FileVersionState(ver.State); {
	case state == domain.FileVersionRemoved:
		return skip(SkipRemoved)
	case ver.SupersededAt.Valid:
		return skip(SkipSuperseded)
	case state != domain.FileVersionDiscovered:
		return skip(SkipNotPending)
	}

	if format == scan.FormatUnsupported {
		return w.fail(ctx, job, t, xlsx.CodeUnsupportedFormat, []Warning{{Code: xlsx.CodeUnsupportedFormat, Detail: "no parser for this file name"}})
	}
	if ver.SizeBytes > w.MaxObjectBytes {
		return w.fail(ctx, job, t, CodeObjectTooLarge, []Warning{{Code: CodeObjectTooLarge, Detail: fmt.Sprintf("object is %d bytes, the cap is %d", ver.SizeBytes, w.MaxObjectBytes)}})
	}
	data, reason, err := w.download(ctx, t)
	if err != nil {
		return t.kind, "", err
	}
	if reason != "" {
		return skip(reason)
	}

	switch format {
	case scan.FormatDocx:
		return w.parseDocx(ctx, job, t, data)
	case scan.FormatXlsx:
		return w.parseWorkbook(ctx, job, t, data)
	default:
		return w.parseMapping(ctx, job, t, data)
	}
}

// download reads the object and checks it is the version's bytes. A non-empty
// reason means "skip the job".
func (w *Worker) download(ctx context.Context, t target) (data []byte, skipReason string, err error) {
	return w.fetch(ctx, t.ver.ObjectKey, t.ver.SHA256)
}

func (w *Worker) fetch(ctx context.Context, key string, wantSHA []byte) ([]byte, string, error) {
	body, err := w.S3.Open(ctx, key)
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return nil, SkipNoObject, nil
	}
	if err != nil {
		return nil, "", err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, w.MaxObjectBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w", key, err)
	}
	if int64(len(data)) > w.MaxObjectBytes {
		return nil, SkipSHAMismatch, nil // grew past the cap: not the scanned bytes
	}
	if sum := sha256.Sum256(data); !bytes.Equal(sum[:], wantSHA) {
		return nil, SkipSHAMismatch, nil
	}
	return data, "", nil
}

// section is a parsed section before it is bound to a version.
type section struct {
	ordinal     int
	kind        domain.SectionKind
	headingPath []string
	level       int
	body        string
	sourceRef   string
}

func (w *Worker) parseDocx(ctx context.Context, job jobs.Job, t target, data []byte) (string, string, error) {
	doc, err := docx.ParseBytes(data)
	if err != nil {
		code := docx.CodeOf(err)
		if code == "" {
			code = docx.CodeDOCXCorrupt
		}
		return w.fail(ctx, job, t, code, []Warning{{Code: code, Detail: err.Error()}})
	}
	secs := make([]section, 0, len(doc.Sections))
	for _, s := range doc.Sections {
		secs = append(secs, section{
			ordinal: s.Ordinal, kind: domain.SectionKindDocxSection, headingPath: s.HeadingPath, level: s.Level,
			body: s.Text(), sourceRef: t.ver.ObjectKey + "#" + s.SourceRef,
		})
	}
	warns := make([]Warning, 0, len(doc.Warnings))
	for _, x := range doc.Warnings {
		warns = append(warns, Warning{Code: x.Code, Detail: x.Detail, Location: x.Location})
	}
	return w.succeed(ctx, job, t, secs, warns, nil)
}

func (w *Worker) succeed(ctx context.Context, job jobs.Job, t target, secs []section, warns []Warning, extra func(q *queries.Queries, tx pgx.Tx) error) (string, string, error) {
	lockFacts := extra != nil
	err := w.inTx(ctx, job, t.ver.ID, lockFacts, func(q *queries.Queries, tx pgx.Tx) error {
		if err := w.writeSections(ctx, q, t.ver.ID, secs); err != nil {
			return err
		}
		if len(secs) > 0 {
			// Candidates are generated from the sections; the job is queued in
			// the transaction that makes them visible, once per version.
			if _, err := jobs.New(q).Add(ctx, jobs.Enqueue{
				Kind: jobs.KindGenerate, Payload: payload{FileVersionID: t.ver.ID}, DedupeKey: t.ver.ID.String(),
			}); err != nil {
				return err
			}
		}
		if extra != nil {
			if err := extra(q, tx); err != nil {
				return err
			}
		}
		return w.transition(ctx, q, t.ver.ID, domain.FileVersionParsed, nil, warns)
	})
	var ce *facts.ConflictError
	if errors.As(err, &ce) {
		return w.fail(ctx, job, t, facts.CodeNameConflict, []Warning{{Code: facts.CodeNameConflict, Detail: ce.Detail, Location: ce.Table}})
	}
	return w.finish(t, OutcomeParsed, err)
}

// fail moves the version to PARSE_FAILED with a code and details.
func (w *Worker) fail(ctx context.Context, job jobs.Job, t target, code string, warns []Warning) (string, string, error) {
	factsKind := t.kind == KindXlsx || t.kind == KindMapping
	err := w.inTx(ctx, job, t.ver.ID, factsKind, func(q *queries.Queries, tx pgx.Tx) error {
		if err := w.transition(ctx, q, t.ver.ID, domain.FileVersionParseFailed, &code, warns); err != nil {
			return err
		}
		if factsKind {
			// The tables this source feeds cannot be served from it.
			if _, err := q.MarkFactTablesUnavailableBySource(ctx, queries.MarkFactTablesUnavailableBySourceParams{
				SourceFileID: &t.ver.SourceFileID, ErrorCode: code,
			}); err != nil {
				return fmt.Errorf("mark fact tables unavailable: %w", err)
			}
		}
		return nil
	})
	w.log().Warn("file version failed to parse", "fileVersionId", t.ver.ID, "key", t.ver.ObjectKey, "code", code, "details", len(warns))
	return w.finish(t, OutcomeFailed, err)
}

// finish maps a transaction result to the (kind, outcome, error) of a job.
// errSuperseded means the version changed under us: the job is completed as
// skipped.
func (w *Worker) finish(t target, outcome string, err error) (string, string, error) {
	if errors.Is(err, errSuperseded) {
		w.log().Info("parse job skipped", "fileVersionId", t.ver.ID, "key", t.ver.ObjectKey, "reason", SkipSuperseded)
		return t.kind, OutcomeSkipped, nil
	}
	if err != nil {
		return t.kind, "", err
	}
	return t.kind, outcome, nil
}

// inTx runs body in one transaction that first re-checks that the version is
// still current and DISCOVERED (errSuperseded otherwise), and completes the
// job when body succeeds. With lockFacts the transaction takes the facts
// import lock before any version lock.
func (w *Worker) inTx(ctx context.Context, job jobs.Job, versionID uuid.UUID, lockFacts bool, body func(q *queries.Queries, tx pgx.Tx) error) error {
	err := pgx.BeginFunc(ctx, w.Store.Pool, func(tx pgx.Tx) error {
		q := w.Store.Queries.WithTx(tx)
		if lockFacts {
			if err := facts.LockImports(ctx, tx); err != nil {
				return err
			}
		}
		cur, err := q.LockFileVersion(ctx, versionID)
		if err != nil {
			return fmt.Errorf("lock file version: %w", err)
		}
		if cur.SupersededAt.Valid || domain.FileVersionState(cur.State) != domain.FileVersionDiscovered {
			return errSuperseded
		}
		if err := body(q, tx); err != nil {
			return err
		}
		return jobs.New(q).Complete(ctx, job.ID, w.name())
	})
	if errors.Is(err, errSuperseded) {
		// Rolled back; the job is completed on its own below by the caller.
		cerr := jobs.New(w.Store.Queries).Complete(ctx, job.ID, w.name())
		if cerr != nil {
			return cerr
		}
	}
	return err
}

func (w *Worker) transition(ctx context.Context, q *queries.Queries, id uuid.UUID, to domain.FileVersionState, code *string, warns []Warning) error {
	if _, err := domain.FileVersionDiscovered.Transition(to); err != nil {
		return err
	}
	raw, err := encodeWarnings(warns)
	if err != nil {
		return err
	}
	n, err := q.TransitionFileVersion(ctx, queries.TransitionFileVersionParams{
		ID: id, FromState: string(domain.FileVersionDiscovered), ToState: string(to), ParseErrorCode: code, ParseWarnings: raw,
	})
	if err != nil {
		return fmt.Errorf("transition file version: %w", err)
	}
	if n != 1 {
		return errSuperseded
	}
	return nil
}

// maxWarningRunes bounds one warning field so a hostile or binary file cannot
// bloat parse_warnings through an error message that quotes it.
const maxWarningRunes = 1000

// cleanText makes s safe to store in jsonb: valid UTF-8, no control
// characters (jsonb rejects \u0000), bounded length.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxWarningRunes {
		s = string(r[:maxWarningRunes]) + "..."
	}
	return s
}

func encodeWarnings(warns []Warning) ([]byte, error) {
	out := make([]Warning, len(warns))
	for i, w := range warns {
		out[i] = Warning{Code: cleanText(w.Code), Detail: cleanText(w.Detail), Location: cleanText(w.Location)}
	}
	return json.Marshal(out)
}

func (w *Worker) writeSections(ctx context.Context, q *queries.Queries, id uuid.UUID, secs []section) error {
	if _, err := q.DeleteParsedSections(ctx, id); err != nil {
		return fmt.Errorf("delete old sections: %w", err)
	}
	rows := make([]queries.InsertParsedSectionsParams, 0, len(secs))
	for _, s := range secs {
		hp := s.headingPath
		if hp == nil {
			hp = []string{}
		}
		rows = append(rows, queries.InsertParsedSectionsParams{
			FileVersionID: id, Ordinal: int32(s.ordinal), Kind: string(s.kind), HeadingPath: hp,
			Level: int32(s.level), Body: s.body, SourceRef: s.sourceRef,
		})
	}
	if n, err := q.InsertParsedSections(ctx, rows); err != nil || n != int64(len(rows)) {
		return fmt.Errorf("insert sections: inserted %d of %d: %v", n, len(rows), err)
	}
	// A candidate belongs to a section by (ordinal, source_ref). Sections that
	// were derived again may have dropped some (a facts mapping now covers the
	// sheet), and their candidates can no longer be checked against the source.
	n, err := q.MarkCandidatesOfWithdrawnSectionsStale(ctx, id)
	if err != nil {
		return fmt.Errorf("mark candidates of withdrawn sections stale: %w", err)
	}
	if n > 0 {
		w.log().Info("candidates became STALE: their section was withdrawn", "fileVersionId", id, "candidates", n, "reason", "SECTION_WITHDRAWN")
	}
	return nil
}

// RetryFailed is the operator retry: every current PARSE_FAILED version goes
// back to DISCOVERED (its content is untouched; only processing state is
// reset) and its existing PARSE job is re-armed with a fresh attempt budget
// instead of inserting a second job. PARSED, UNSUPPORTED and REMOVED versions
// are never touched.
func (w *Worker) RetryFailed(ctx context.Context) (RetrySummary, error) {
	var sum RetrySummary
	err := pgx.BeginFunc(ctx, w.Store.Pool, func(tx pgx.Tx) error {
		sum = RetrySummary{}
		q := w.Store.Queries.WithTx(tx)
		failed, err := q.ListCurrentFailedFileVersions(ctx)
		if err != nil {
			return fmt.Errorf("list failed versions: %w", err)
		}
		queue := jobs.New(q)
		for _, v := range failed {
			if _, err := domain.FileVersionState(v.State).Transition(domain.FileVersionDiscovered); err != nil {
				return err
			}
			n, err := q.TransitionFileVersion(ctx, queries.TransitionFileVersionParams{
				ID: v.ID, FromState: v.State, ToState: string(domain.FileVersionDiscovered), ParseWarnings: []byte("[]"),
			})
			if err != nil {
				return fmt.Errorf("reset version %s: %w", v.ID, err)
			}
			if n != 1 {
				return fmt.Errorf("reset version %s: changed %d rows", v.ID, n)
			}
			sum.Versions++
			queued, err := queue.Rearm(ctx, jobs.Enqueue{
				Kind: jobs.KindParse, Payload: payload{FileVersionID: v.ID}, DedupeKey: v.ID.String(),
			})
			if err != nil {
				return err
			}
			if queued {
				sum.JobsQueued++
			}
		}
		return nil
	})
	return sum, err
}

// mappingKeyOf and workbookKeyOf map a workbook key to its mapping key and back.
func mappingKeyOf(workbookKey string) string {
	return workbookKey[:len(workbookKey)-len(".xlsx")] + ".facts.yaml"
}

func workbookKeyOf(mappingKey string) string {
	return mappingKey[:len(mappingKey)-len(".facts.yaml")] + ".xlsx"
}
