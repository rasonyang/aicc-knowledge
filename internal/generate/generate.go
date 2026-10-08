// SPDX-License-Identifier: Apache-2.0

// Package generate is the GENERATE job worker: it turns the sections of a
// PARSED file version into candidate Q&A with an offline LLM and stores them
// as PENDING_REVIEW rows.
//
// For each claimed job:
//
//   - the version must still be current and PARSED, and must not have
//     candidates yet, otherwise the job is completed as skipped;
//   - each section (in ordinal order) goes to the LLM with the versioned
//     prompt. The answer is decoded and checked against the schema in Go, and
//     every candidate is validated by internal/candidate (length, markup,
//     URLs, script/language). If anything failed, the model gets one retry
//     with the problems appended; candidates that still fail are dropped with
//     a warning (log and metric), never stored;
//   - questions are deduplicated across the whole version (first wins);
//   - all candidates of the version are inserted in one transaction that
//     re-checks the version and the sections under a lock, so a re-run never
//     duplicates and a section re-derived in the meantime is not resurrected.
//
// An infrastructure error (LLM unreachable, database) writes nothing and the
// job goes back to the queue with backoff.
package generate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/jobs"
	"github.com/rasonyang/aicc-knowledge/internal/llm"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// Job failure codes (jobs.last_error_code) besides the llm package's.
const (
	JobCodeRuntime         = "GENERATE_RUNTIME_ERROR"
	JobCodeVersionNotFound = "FILE_VERSION_NOT_FOUND"
	JobCodeBadPayload      = "BAD_PAYLOAD"
)

// Reasons a job is skipped.
const (
	SkipSuperseded       = "SUPERSEDED"
	SkipRemoved          = "REMOVED"
	SkipNotParsed        = "NOT_PARSED"
	SkipAlreadyGenerated = "ALREADY_GENERATED"
)

// Metric outcome labels.
const (
	OutcomeCreated          = "CREATED"
	OutcomeDroppedInvalid   = "DROPPED_INVALID"
	OutcomeDroppedDuplicate = "DROPPED_DUPLICATE"

	SectionOK      = "OK"
	SectionRetried = "RETRIED"
	SectionPartial = "PARTIAL"
	SectionEmpty   = "EMPTY"
)

// Summary counts what one Run did.
type Summary struct {
	Claimed   int
	Generated int // jobs that ended with candidates stored
	Skipped   int
	Errors    int // jobs that hit an infrastructure error and went back to the queue

	Sections   int // sections sent to the LLM
	Candidates int // candidates stored
	Warnings   int // candidates dropped by validation after the retry
	Duplicates int // candidates dropped as duplicate questions
	LLMTime    time.Duration
}

// Completer is what the worker needs from the LLM client.
type Completer interface {
	CompleteJSON(ctx context.Context, req llm.Request, validate func(content []byte) error) (string, error)
	Model() string
}

// Worker runs GENERATE jobs.
type Worker struct {
	Store   *store.Store
	LLM     Completer
	Limits  candidate.Limits
	Metrics *obs.Metrics // optional
	Log     *slog.Logger // optional
	// Name identifies the worker in job leases; defaults to host and pid.
	Name string
	// Lease is the job lease, extended after every section; defaults to
	// jobs.DefaultLease.
	Lease time.Duration
}

type payload struct {
	FileVersionID uuid.UUID `json:"fileVersionId"`
}

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
	return fmt.Sprintf("generate-%s-%d", host, os.Getpid())
}

func (w *Worker) lease() time.Duration {
	if w.Lease > 0 {
		return w.Lease
	}
	return jobs.DefaultLease
}

// Enqueue re-arms (or creates) the GENERATE job of one version, for the
// operator's `generate --version`. A version that already has candidates is
// skipped when the job runs.
func (w *Worker) Enqueue(ctx context.Context, versionID uuid.UUID) (bool, error) {
	return jobs.New(w.Store.Queries).Rearm(ctx, jobs.Enqueue{
		Kind: jobs.KindGenerate, Payload: payload{FileVersionID: versionID}, DedupeKey: versionID.String(),
	})
}

// Run claims and processes GENERATE jobs until none is claimable.
func (w *Worker) Run(ctx context.Context) (Summary, error) {
	var sum Summary
	queue := jobs.New(w.Store.Queries)
	for {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		job, err := queue.Claim(ctx, w.name(), w.lease(), jobs.KindGenerate)
		if errors.Is(err, jobs.ErrNoJob) {
			return sum, nil
		}
		if err != nil {
			return sum, err
		}
		sum.Claimed++
		_, skipped, err := w.process(ctx, queue, job, &sum)
		if err != nil {
			if ctx.Err() != nil {
				return sum, ctx.Err()
			}
			code := JobCodeRuntime
			var je *jobError
			var le *llm.Error
			switch {
			case errors.As(err, &je):
				code = je.code
			case errors.As(err, &le):
				code = string(le.Code)
			}
			w.log().Error("generate job failed", "job", job.ID, "code", code, "error", err)
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			if ferr := queue.Fail(fctx, job.ID, w.name(), code, err.Error()); ferr != nil {
				w.log().Error("could not record job failure", "job", job.ID, "error", ferr)
			}
			cancel()
			sum.Errors++
			continue
		}
		if skipped != "" {
			sum.Skipped++
		} else {
			sum.Generated++ // the job ran to the end, even if the sections held nothing to ask
		}
	}
}

// process handles one claimed job: it reports how many candidates it stored
// and, when it skipped the job, why.
func (w *Worker) process(ctx context.Context, queue *jobs.Queue, job jobs.Job, sum *Summary) (stored int, skipped string, err error) {
	var p payload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.FileVersionID == uuid.Nil {
		return 0, "", &jobError{JobCodeBadPayload, fmt.Errorf("payload %s has no fileVersionId", job.Payload)}
	}
	skip := func(reason string) (int, string, error) {
		w.log().Info("generate job skipped", "job", job.ID, "fileVersionId", p.FileVersionID, "reason", reason)
		if err := queue.Complete(ctx, job.ID, w.name()); err != nil {
			return 0, "", err
		}
		return 0, reason, nil
	}

	q := w.Store.Queries
	ver, err := q.GetFileVersionWithSource(ctx, p.FileVersionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", &jobError{JobCodeVersionNotFound, fmt.Errorf("file version %s does not exist", p.FileVersionID)}
	}
	if err != nil {
		return 0, "", fmt.Errorf("load file version: %w", err)
	}
	if reason := notGeneratable(ver.State, ver.SupersededAt.Valid); reason != "" {
		return skip(reason)
	}
	if n, err := q.CountCandidatesOfVersion(ctx, ver.ID); err != nil {
		return 0, "", fmt.Errorf("count candidates: %w", err)
	} else if n > 0 {
		return skip(SkipAlreadyGenerated)
	}
	secs, err := q.ListParsedSections(ctx, ver.ID)
	if err != nil {
		return 0, "", fmt.Errorf("list sections: %w", err)
	}

	var drafts []draft
	seen := newQuestionSet()
	for _, s := range secs {
		got, err := w.section(ctx, ver.ObjectKey, s, sum)
		if err != nil {
			return 0, "", err
		}
		for _, d := range got {
			if !seen.add(d.question, d.alternates) {
				sum.Duplicates++
				w.count(ctx, string(d.language), OutcomeDroppedDuplicate, 1)
				w.log().Warn("candidate dropped: duplicate question", "sourceRef", s.SourceRef, "question", d.question)
				continue
			}
			d.ordinal, d.sourceRef = int(s.Ordinal), s.SourceRef
			drafts = append(drafts, d)
		}
		if err := queue.Extend(ctx, job.ID, w.name(), w.lease()); err != nil {
			return 0, "", err
		}
	}
	return w.persist(ctx, queue, job, ver.ID, drafts, sum)
}

func notGeneratable(state string, superseded bool) string {
	switch {
	case domain.FileVersionState(state) == domain.FileVersionRemoved:
		return SkipRemoved
	case superseded:
		return SkipSuperseded
	case domain.FileVersionState(state) != domain.FileVersionParsed:
		return SkipNotParsed
	}
	return ""
}

func (w *Worker) count(ctx context.Context, language, outcome string, n int64) {
	if w.Metrics != nil {
		w.Metrics.ObserveGenerateCandidates(ctx, language, outcome, n)
	}
}

// questionSet tracks the normalised questions and alternate questions of the
// candidates kept so far in a version.
type questionSet map[string]bool

func newQuestionSet() questionSet { return questionSet{} }

// add reports false, and adds nothing, when the question equals an earlier
// question or alternate question.
func (s questionSet) add(question string, alternates []string) bool {
	k := candidate.Normalize(question)
	if s[k] {
		return false
	}
	s[k] = true
	for _, a := range alternates {
		s[candidate.Normalize(a)] = true
	}
	return true
}

// draft is a validated candidate of one section.
type draft struct {
	ordinal    int
	sourceRef  string
	language   domain.Language
	question   string
	alternates []string
	answer     string
	generated  time.Time
}

// output is the schema the model answers with.
type output struct {
	Candidates []struct {
		Question           string   `json:"question"`
		AlternateQuestions []string `json:"alternateQuestions"`
		Answer             string   `json:"answer"`
		Language           string   `json:"language"`
	} `json:"candidates"`
}

// decodeOutput checks content against the schema in Go: a JSON object with
// exactly the key "candidates", an array of objects with exactly the four
// string/array fields (all required, none null), and nothing after the value.
func decodeOutput(content []byte) (*output, error) {
	var raw struct {
		Candidates *[]map[string]json.RawMessage `json:"candidates"`
	}
	dec := json.NewDecoder(strings.NewReader(string(content)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("not the expected JSON object: %w", err)
	}
	if dec.More() {
		return nil, errors.New("trailing data after the JSON value")
	}
	if raw.Candidates == nil {
		return nil, errors.New(`missing "candidates"`)
	}
	required := map[string]byte{"question": '"', "alternateQuestions": '[', "answer": '"', "language": '"'}
	for i, c := range *raw.Candidates {
		for k, first := range required {
			v, ok := c[k]
			if !ok || len(v) == 0 || v[0] != first {
				return nil, fmt.Errorf("candidates[%d].%s is missing or has the wrong type", i, k)
			}
		}
		for k := range c {
			if _, ok := required[k]; !ok {
				return nil, fmt.Errorf("candidates[%d] has unexpected field %q", i, k)
			}
		}
	}
	var out output
	if err := json.Unmarshal(content, &out); err != nil {
		return nil, fmt.Errorf("candidate fields have the wrong type: %w", err)
	}
	return &out, nil
}

// section runs the LLM over one section, with at most one retry, and returns
// the valid drafts. A non-nil error is an infrastructure error.
func (w *Worker) section(ctx context.Context, objectKey string, s queries.ParsedSection, sum *Summary) ([]draft, error) {
	body := strings.TrimSpace(s.Body)
	if len([]rune(body)) < MinSectionChars {
		return nil, nil
	}
	lang, ok := candidate.DetectLanguage(strings.Join(s.HeadingPath, " ") + " " + body)
	if !ok {
		return nil, nil
	}
	sum.Sections++
	msgs := messages(lang, w.Limits, s.HeadingPath, body)
	good, bad, raw, err := w.attempt(ctx, msgs, lang, sum)
	if err != nil {
		return nil, err
	}
	retried := false
	if len(bad) > 0 {
		retried = true
		w.log().Info("section needs a retry", "sourceRef", s.SourceRef, "problems", len(bad))
		good2, bad2, _, err := w.attempt(ctx, append(msgs, feedback(raw, bad)...), lang, sum)
		if err != nil {
			return nil, err
		}
		good, bad = mergeDrafts(good, good2), bad2
	}
	if len(good) > MaxCandidatesPerSection {
		good = good[:MaxCandidatesPerSection]
	}
	for _, p := range bad {
		w.count(ctx, string(lang), OutcomeDroppedInvalid, 1)
		sum.Warnings++
		w.log().Warn("candidate dropped: validation failed after the retry",
			"sourceRef", s.SourceRef, "key", objectKey, "problem", p.String())
	}
	w.count(ctx, string(lang), OutcomeCreated, 0) // keeps the series visible
	outcome := SectionOK
	switch {
	case len(good) == 0:
		outcome = SectionEmpty
	case len(bad) > 0:
		outcome = SectionPartial
	case retried:
		outcome = SectionRetried
	}
	if w.Metrics != nil {
		w.Metrics.ObserveGenerateSection(ctx, outcome)
	}
	return good, nil
}

func mergeDrafts(first, second []draft) []draft {
	seen := newQuestionSet()
	var out []draft
	for _, d := range append(append([]draft{}, first...), second...) {
		if seen.add(d.question, d.alternates) {
			out = append(out, d)
		}
	}
	return out
}

// attempt makes one request. It returns the valid drafts, the problems, and
// the model's raw content (for the feedback turn). Content that is not valid
// JSON of the schema is one problem with Index 0.
func (w *Worker) attempt(ctx context.Context, msgs []llm.Message, lang domain.Language, sum *Summary) (good []draft, bad []problem, raw string, _ error) {
	start := time.Now()
	content, err := w.LLM.CompleteJSON(ctx, llm.Request{Messages: msgs, SchemaName: schemaName, Schema: schema}, func(b []byte) error {
		_, err := decodeOutput(b)
		return err
	})
	elapsed := time.Since(start)
	sum.LLMTime += elapsed
	outcome := "OK"
	var le *llm.Error
	if err != nil {
		outcome = "ERROR"
		if errors.As(err, &le) {
			outcome = string(le.Code)
		}
	}
	if w.Metrics != nil && ctx.Err() == nil {
		w.Metrics.ObserveLLMRequest(ctx, outcome, elapsed)
	}
	if errors.Is(err, llm.ErrOutputInvalid) {
		return nil, []problem{{Violations: []candidate.Violation{{Code: string(llm.CodeOutputInvalid), Detail: le.Error()}}}}, "", nil
	}
	if err != nil {
		return nil, nil, "", err
	}
	out, _ := decodeOutput([]byte(content)) // validated by the client
	for i, c := range out.Candidates {
		r := candidate.Validate(candidate.Draft{Language: c.Language, Question: c.Question, AlternateQuestions: c.AlternateQuestions, Answer: c.Answer}, w.Limits)
		if r.Language.Valid() && r.Language != lang {
			r.Violations = append(r.Violations, candidate.Violation{
				Code: candidate.CodeLanguageMixed, Field: "language", Detail: fmt.Sprintf("the section is written in %s but the entry says %s", lang, r.Language),
			})
		}
		if !r.OK() {
			bad = append(bad, problem{Index: i + 1, Question: r.Question, Violations: r.Violations})
			continue
		}
		good = append(good, draft{language: r.Language, question: r.Question, alternates: r.AlternateQuestions, answer: r.Answer, generated: time.Now()})
	}
	return good, bad, content, nil
}

type skipError struct{ reason string }

func (e *skipError) Error() string { return "skipped: " + e.reason }

// persist stores the drafts of a version in one transaction.
func (w *Worker) persist(ctx context.Context, queue *jobs.Queue, job jobs.Job, versionID uuid.UUID, drafts []draft, sum *Summary) (int, string, error) {
	stored := 0
	err := pgx.BeginFunc(ctx, w.Store.Pool, func(tx pgx.Tx) error {
		stored = 0
		q := w.Store.Queries.WithTx(tx)
		cur, err := q.LockFileVersion(ctx, versionID)
		if err != nil {
			return fmt.Errorf("lock file version: %w", err)
		}
		if reason := notGeneratable(cur.State, cur.SupersededAt.Valid); reason != "" {
			return &skipError{reason}
		}
		if n, err := q.CountCandidatesOfVersion(ctx, versionID); err != nil {
			return fmt.Errorf("count candidates: %w", err)
		} else if n > 0 {
			return &skipError{SkipAlreadyGenerated}
		}
		secs, err := q.ListParsedSections(ctx, versionID)
		if err != nil {
			return fmt.Errorf("list sections: %w", err)
		}
		sourceRefs := map[int32]string{}
		for _, s := range secs {
			sourceRefs[s.Ordinal] = s.SourceRef
		}
		for _, d := range drafts {
			if ref, ok := sourceRefs[int32(d.ordinal)]; !ok || ref != d.sourceRef {
				w.log().Warn("candidate dropped: its section was re-derived while generating", "sourceRef", d.sourceRef, "question", d.question)
				continue
			}
			c := candidate.Content{
				Language: d.language, Question: d.question, AlternateQuestions: d.alternates, Answer: d.answer,
				SourceRef: d.sourceRef, FileVersionID: versionID,
			}
			hash := candidate.ContentHash(c)
			n, err := q.InsertCandidate(ctx, queries.InsertCandidateParams{
				FileVersionID: versionID, SectionOrdinal: ptr(int32(d.ordinal)), Language: string(d.language), Question: d.question,
				AlternateQuestions: d.alternates, Answer: d.answer, SourceRef: d.sourceRef,
				Flags: candidate.Flags(d.question, d.alternates, d.answer), ContentHash: hash[:],
				PromptVersion: PromptVersion, Model: w.LLM.Model(), GeneratedAt: tsz(d.generated),
			})
			if err != nil {
				return fmt.Errorf("insert candidate: %w", err)
			}
			if n == 1 {
				stored++
				w.count(ctx, string(d.language), OutcomeCreated, 1)
			}
		}
		return jobs.New(q).Complete(ctx, job.ID, w.name())
	})
	var se *skipError
	if errors.As(err, &se) {
		w.log().Info("generate job skipped", "job", job.ID, "fileVersionId", versionID, "reason", se.reason)
		if err := queue.Complete(ctx, job.ID, w.name()); err != nil {
			return 0, "", err
		}
		return 0, se.reason, nil
	}
	if err != nil {
		return 0, "", err
	}
	sum.Candidates += stored
	return stored, "", nil
}

func ptr[T any](v T) *T { return &v }

func tsz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
