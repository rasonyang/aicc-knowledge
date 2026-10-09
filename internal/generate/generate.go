// SPDX-License-Identifier: Apache-2.0

// Package generate is the GENERATE job worker: it turns the sections of a
// PARSED file version into candidate Q&A with an offline LLM and stores them
// as PENDING_REVIEW rows.
//
// For each claimed job:
//
//   - the version must still be current and PARSED, and must not have
//     candidates yet, otherwise the job is completed as skipped;
//   - a section that is a stub (only a title or boilerplate lines, see
//     MinMeaningfulChars) is not sent; its text becomes context of the next
//     section that is sent. Every skipped section is logged and counted;
//   - a Q&A row (a sheet imported through a `.qa.yaml` mapping) needs no LLM:
//     its question, alternates and answer become a candidate verbatim
//     (qa-import-v1). Only an answer that is too long or has line breaks or
//     Markdown gets one LLM call that shortens it (qa-condense-v1);
//   - each other section (in ordinal order) goes to the LLM with the versioned
//     prompt. The answer is decoded and checked against the schema in Go, and
//     every candidate is validated by internal/candidate (length, markup,
//     URLs, script/language). If anything failed, the model gets one retry
//     with the problems appended; candidates that still fail are dropped with
//     a warning (log and metric), never stored. An answer that states a number
//     or model name the section does not contain is invalid (UNGROUNDED_FIGURE);
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
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/unicode/norm"

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

	// Sections that were not sent to the LLM, and why. Each is logged.
	SectionSkippedStub       = "SKIPPED_STUB"
	SectionSkippedNoLanguage = "SKIPPED_NO_LANGUAGE"
	// SectionTruncated counts sections sent with their text cut at
	// MaxSectionChars (in addition to their own outcome).
	SectionTruncated = "TRUNCATED"
	// Q&A rows.
	SectionQAVerbatim  = "QA_VERBATIM"
	SectionQACondensed = "QA_CONDENSED"
	SectionQADropped   = "QA_DROPPED"
	// SectionQANeedsShortening counts Q&A rows kept with their original answer
	// and the NEEDS_SHORTENING flag because condensing did not give a usable one.
	SectionQANeedsShortening = "QA_NEEDS_SHORTENING"
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

	SkippedStub       int // stub sections not sent (their text became context)
	SkippedNoLanguage int // sections without letters, not sent
	Truncated         int // sections whose text was cut at MaxSectionChars
	QAVerbatim        int // Q&A rows imported as they are
	QACondensed       int // Q&A rows whose answer the LLM shortened
	QADropped         int // Q&A rows that gave no candidate (invalid)
	QANeedsShortening int // Q&A rows kept with the original answer, flagged NEEDS_SHORTENING
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
	// Only, when set, restricts Run to the GENERATE job of that file version:
	// every other queued job is left alone.
	Only uuid.UUID
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
		var job jobs.Job
		var err error
		if w.Only != uuid.Nil {
			job, err = queue.ClaimByKey(ctx, w.name(), w.lease(), jobs.KindGenerate, w.Only.String())
		} else {
			job, err = queue.Claim(ctx, w.name(), w.lease(), jobs.KindGenerate)
		}
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
	secs, err := q.ListParsedSections(ctx, ver.ID)
	if err != nil {
		return 0, "", fmt.Errorf("list sections: %w", err)
	}
	secs, topUp, err := w.selectSections(ctx, ver.ID, secs)
	if err != nil {
		return 0, "", err
	}
	if topUp && len(secs) == 0 {
		return skip(SkipAlreadyGenerated)
	}

	var drafts []draft
	seen := newQuestionSet()
	var stubs []string // text of the stub sections since the last section that was sent
	for _, s := range secs {
		var got []draft
		switch {
		case s.Kind == string(domain.SectionKindXlsxQARow):
			d, err := w.qaRow(ctx, ver.ObjectKey, s, sum)
			if err != nil {
				return 0, "", err
			}
			got = d
		default:
			plan := classify(s)
			if plan.stub {
				sum.SkippedStub++
				w.skipped(ctx, SectionSkippedStub, ver.ObjectKey, s, "no text of its own; it becomes context of the next section")
				if plan.context != "" {
					stubs = append(stubs, plan.context)
				}
				continue
			}
			d, err := w.section(ctx, ver.ObjectKey, s, contextOf(stubs), sum)
			if err != nil {
				return 0, "", err
			}
			if !d.skipped {
				stubs = nil
			}
			got = d.drafts
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
	return w.persist(ctx, queue, job, ver.ID, drafts, topUp, sum)
}

// selectSections decides which sections a job works on. A version without
// candidates takes all of them. A version that has candidates was generated
// before: only the Q&A rows that have no live candidate (they arrived with a
// mapping after the first run, or their candidate went STALE because the row
// changed) are generated, and when there are none the job is skipped.
func (w *Worker) selectSections(ctx context.Context, versionID uuid.UUID, secs []queries.ParsedSection) (_ []queries.ParsedSection, topUp bool, _ error) {
	n, err := w.Store.Queries.CountCandidatesOfVersion(ctx, versionID)
	if err != nil {
		return nil, false, fmt.Errorf("count candidates: %w", err)
	}
	if n == 0 {
		return secs, false, nil
	}
	live, err := w.Store.Queries.ListLiveCandidateSections(ctx, versionID)
	if err != nil {
		return nil, false, fmt.Errorf("list candidate sections: %w", err)
	}
	have := map[string]bool{}
	for _, l := range live {
		if l.SectionOrdinal != nil {
			have[fmt.Sprint(*l.SectionOrdinal, "\x00", l.SourceRef)] = true
		}
	}
	var pending []queries.ParsedSection
	for _, s := range secs {
		if s.Kind == string(domain.SectionKindXlsxQARow) && !have[fmt.Sprint(s.Ordinal, "\x00", s.SourceRef)] {
			pending = append(pending, s)
		}
	}
	return pending, true, nil
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
	// promptVersion and model say what produced the text; model is empty for a
	// verbatim Q&A import.
	promptVersion string
	model         string
	// needsShortening marks a Q&A row whose original answer fails the answer
	// limits and was kept for the reviewer to shorten.
	needsShortening bool
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

// sectionResult is what section produced.
type sectionResult struct {
	drafts  []draft
	skipped bool // not sent to the LLM
}

// section runs the LLM over one non-stub section, with at most one retry, and
// returns the valid drafts. extra is the context from preceding stub sections.
// A non-nil error is an infrastructure error.
func (w *Worker) section(ctx context.Context, objectKey string, s queries.ParsedSection, extra string, sum *Summary) (sectionResult, error) {
	body := strings.TrimSpace(s.Body)
	head := strings.Join(s.HeadingPath, " ")
	lang, ok := candidate.DetectLanguage(head + " " + body)
	if !ok {
		sum.SkippedNoLanguage++
		w.skipped(ctx, SectionSkippedNoLanguage, objectKey, s, "no letters to write a question in")
		return sectionResult{skipped: true}, nil
	}
	if r := []rune(body); len(r) > MaxSectionChars {
		body = string(r[:MaxSectionChars])
		sum.Truncated++
		w.log().Warn("section text truncated before it was sent to the LLM",
			"sourceRef", s.SourceRef, "key", objectKey, "code", "SECTION_TRUNCATED", "chars", len(r), "kept", MaxSectionChars)
		if w.Metrics != nil {
			w.Metrics.ObserveGenerateSection(ctx, SectionTruncated)
		}
	}
	sum.Sections++
	source := head + "\n" + extra + "\n" + body // what an answer may take its figures from
	msgs := messages(lang, w.Limits, docTitle(objectKey), s.HeadingPath, extra, body)
	good, bad, raw, err := w.attempt(ctx, msgs, lang, source, sum)
	if err != nil {
		return sectionResult{}, err
	}
	retried := false
	if len(bad) > 0 {
		retried = true
		w.log().Info("section needs a retry", "sourceRef", s.SourceRef, "problems", len(bad))
		good2, bad2, _, err := w.attempt(ctx, append(msgs, feedback(raw, bad)...), lang, source, sum)
		if err != nil {
			return sectionResult{}, err
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
	return sectionResult{drafts: good}, nil
}

// skipped logs and counts a section that was not sent to the LLM. A skipped
// section is never silent.
func (w *Worker) skipped(ctx context.Context, outcome, objectKey string, s queries.ParsedSection, why string) {
	w.log().Warn("section skipped", "outcome", outcome, "sourceRef", s.SourceRef, "key", objectKey, "why", why)
	if w.Metrics != nil {
		w.Metrics.ObserveGenerateSection(ctx, outcome)
	}
}

// docTitle is the document's name for the prompt: the file name without its
// directory and extension.
func docTitle(objectKey string) string {
	base := path.Base(objectKey)
	return strings.TrimSuffix(base, path.Ext(base))
}

// plan is the verdict of classify.
type plan struct {
	stub bool
	// context is the text to attach to the next section when stub is set.
	context string
}

// classify decides whether a section carries text of its own. The test is on
// the body without its boilerplate lines (isBoilerplate), not on the heading:
// a section that only has a title, or a title and an "applicable products"
// line, would make the model invent an answer from the title. The exception
// is a heading that is itself a question (questionHeading): any body with
// text in it is that question's answer, however short.
func classify(s queries.ParsedSection) plan {
	body := strings.TrimSpace(s.Body)
	heading := ""
	if n := len(s.HeadingPath); n > 0 {
		heading = s.HeadingPath[n-1]
	}
	if questionHeading(heading) && hasText(body) {
		return plan{}
	}
	if utf8.RuneCountInString(removeBoilerplate(body)) >= MinMeaningfulChars {
		return plan{}
	}
	ctx := strings.Join(s.HeadingPath, " > ")
	if body != "" {
		if ctx != "" {
			ctx += ": "
		}
		ctx += strings.Join(strings.Fields(body), " ")
	}
	return plan{stub: true, context: ctx}
}

var questionStart = regexp.MustCompile(`^[Qq]\s*\d`)

// questionHeading reports whether a heading is a question: it ends with ？ or
// ?, or starts with Q and a digit (Q1, Q 12).
func questionHeading(h string) bool {
	h = strings.TrimSpace(norm.NFKC.String(h))
	return strings.HasSuffix(h, "?") || questionStart.MatchString(h)
}

// hasText reports whether s has at least two letters, digits or Han characters.
func hasText(s string) bool {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if n++; n == 2 {
				return true
			}
		}
	}
	return false
}

// Boilerplate lines are the short "label: value" lines that documents put
// under a title (applicable products, version, date): a label of at most
// maxLabelRunes characters, a colon (ASCII or full-width), and a line of at
// most maxBoilerplateRunes characters.
const (
	maxLabelRunes       = 24
	maxBoilerplateRunes = 50
)

func isBoilerplate(line string) bool {
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > maxBoilerplateRunes {
		return false
	}
	i := strings.IndexAny(line, ":：")
	if i < 0 {
		return false
	}
	label := utf8.RuneCountInString(strings.TrimSpace(line[:i]))
	return label >= 1 && label <= maxLabelRunes
}

// removeBoilerplate drops the boilerplate and blank lines and joins the rest.
func removeBoilerplate(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line != "" && !isBoilerplate(line) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// contextOf joins the stub texts that precede a section, newest last, and
// keeps the last MaxContextChars characters.
func contextOf(stubs []string) string {
	c := strings.Join(stubs, "\n")
	if r := []rune(c); len(r) > MaxContextChars {
		c = string(r[len(r)-MaxContextChars:])
	}
	return c
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
func (w *Worker) attempt(ctx context.Context, msgs []llm.Message, lang domain.Language, source string, sum *Summary) (good []draft, bad []problem, raw string, _ error) {
	content, err := w.complete(ctx, msgs, schemaName, schema, func(b []byte) error {
		_, err := decodeOutput(b)
		return err
	}, sum)
	var le *llm.Error
	errors.As(err, &le)
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
		if v, ungrounded := candidate.CheckGrounded(r.Answer, source); ungrounded {
			r.Violations = append(r.Violations, v)
		}
		if !r.OK() {
			bad = append(bad, problem{Index: i + 1, Question: r.Question, Violations: r.Violations})
			continue
		}
		good = append(good, draft{language: r.Language, question: r.Question, alternates: r.AlternateQuestions, answer: r.Answer,
			generated: time.Now(), promptVersion: PromptVersion, model: w.LLM.Model()})
	}
	return good, bad, content, nil
}

// complete makes one LLM request, timed and counted.
func (w *Worker) complete(ctx context.Context, msgs []llm.Message, name string, schema map[string]any, validate func([]byte) error, sum *Summary) (string, error) {
	start := time.Now()
	content, err := w.LLM.CompleteJSON(ctx, llm.Request{Messages: msgs, SchemaName: name, Schema: schema}, validate)
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
	return content, err
}

type skipError struct{ reason string }

func (e *skipError) Error() string { return "skipped: " + e.reason }

// persist stores the drafts of a version in one transaction.
func (w *Worker) persist(ctx context.Context, queue *jobs.Queue, job jobs.Job, versionID uuid.UUID, drafts []draft, topUp bool, sum *Summary) (int, string, error) {
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
		} else if n > 0 && !topUp {
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
				Flags: draftFlags(d), ContentHash: hash[:],
				PromptVersion: d.promptVersion, Model: d.model, GeneratedAt: tsz(d.generated),
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

// draftFlags is the flag set of a draft: the figures flag, plus
// NEEDS_SHORTENING for a Q&A row kept with its over-long original answer.
func draftFlags(d draft) []string {
	f := candidate.Flags(d.question, d.alternates, d.answer)
	if d.needsShortening {
		f = append(f, string(domain.FlagNeedsShortening))
	}
	return f
}

func ptr[T any](v T) *T { return &v }

func tsz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
