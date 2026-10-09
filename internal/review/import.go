// SPDX-License-Identifier: Apache-2.0

package review

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/obs"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// Row outcomes. The first four are applied (or deliberately skipped); the
// rest are errors: the row changed nothing.
const (
	OutcomeApproved = "APPROVED"
	OutcomeRejected = "REJECTED"
	OutcomeEdited   = "EDITED" // action EDIT: approved with the reviewer's text
	OutcomeSkipped  = "SKIPPED"

	CodeUnknownCandidate       = "UNKNOWN_CANDIDATE"
	CodeNotPending             = "NOT_PENDING"
	CodeStale                  = "STALE"
	CodeEditRequiresEditAction = "EDIT_REQUIRES_EDIT_ACTION"
	CodeInvalidEdit            = "INVALID_EDIT"
	CodeShorteningRequired     = "SHORTENING_REQUIRED"
	CodeInvalidAction          = "INVALID_ACTION"
	CodeDuplicateContent       = "DUPLICATE_CONTENT"
	CodeReviewFileInvalid      = "REVIEW_FILE_INVALID"
	maxDetailChars             = 300
)

// FileError means the workbook is not a review export at all.
type FileError struct{ Msg string }

func (e *FileError) Error() string { return CodeReviewFileInvalid + ": " + e.Msg }

// RowResult is the outcome of one row.
type RowResult struct {
	Row       int // spreadsheet row number, 1-based (the header is row 1)
	ID        string
	Outcome   string // OutcomeApproved, OutcomeRejected, OutcomeEdited, OutcomeSkipped or an error code
	Detail    string
	Candidate uuid.UUID
}

// IsError reports whether the row was refused.
func (r RowResult) IsError() bool {
	switch r.Outcome {
	case OutcomeApproved, OutcomeRejected, OutcomeEdited, OutcomeSkipped:
		return false
	}
	return true
}

// Summary is the result of an import.
type Summary struct {
	Rows    int            // rows that carried data
	Counts  map[string]int // by outcome
	Results []RowResult    // every row, in sheet order
}

// Errors returns the refused rows.
func (s Summary) Errors() []RowResult {
	var out []RowResult
	for _, r := range s.Results {
		if r.IsError() {
			out = append(out, r)
		}
	}
	return out
}

// Clean reports whether no row was refused (SKIPPED rows are fine).
func (s Summary) Clean() bool { return len(s.Errors()) == 0 }

// ImportOptions configures Import.
type ImportOptions struct {
	// Reviewer is recorded in the audit trail; required.
	Reviewer string
	// Limits are the validation limits applied to EDIT rows.
	Limits  candidate.Limits
	Metrics *obs.Metrics // optional
}

// Import applies the decisions of a review workbook in one transaction. Rows
// are independent: a refused row changes nothing and does not stop the others.
// An infrastructure error rolls everything back.
func Import(ctx context.Context, st *store.Store, path string, opts ImportOptions) (Summary, error) {
	reviewer := strings.TrimSpace(opts.Reviewer)
	if reviewer == "" {
		return Summary{}, errors.New("a reviewer name is required")
	}
	rows, err := readSheet(path)
	if err != nil {
		return Summary{}, err
	}
	fileName := filepath.Base(path)

	sum := Summary{Counts: map[string]int{}}
	err = pgx.BeginFunc(ctx, st.Pool, func(tx pgx.Tx) error {
		sum = Summary{Counts: map[string]int{}}
		q := st.Queries.WithTx(tx)
		for _, row := range rows {
			if row.blank() {
				continue
			}
			sum.Rows++
			sp, err := tx.Begin(ctx) // a savepoint: a refused row must not poison the transaction
			if err != nil {
				return err
			}
			res, err := applyRow(ctx, q.WithTx(sp), row, reviewer, fileName, opts.Limits)
			if err != nil {
				_ = sp.Rollback(ctx)
				return fmt.Errorf("row %d: %w", row.number, err)
			}
			if res.IsError() {
				_ = sp.Rollback(ctx)
			} else if err := sp.Commit(ctx); err != nil {
				return err
			}
			sum.Counts[res.Outcome]++
			sum.Results = append(sum.Results, res)
		}
		return nil
	})
	if err != nil {
		return Summary{}, err
	}
	if opts.Metrics != nil {
		for _, r := range sum.Results {
			opts.Metrics.ObserveReviewRow(ctx, r.Outcome)
		}
	}
	return sum, nil
}

type sheetRow struct {
	number                                   int
	id, hash, action, question, alts, answer string
	note                                     string
}

func (r sheetRow) blank() bool {
	return r.id == "" && r.hash == "" && r.action == "" && r.question == "" && r.alts == "" && r.answer == "" && r.note == ""
}

func readSheet(path string) ([]sheetRow, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, &FileError{Msg: "cannot open the workbook: " + err.Error()}
	}
	defer f.Close()
	grid, err := f.GetRows(SheetReview)
	if err != nil {
		return nil, &FileError{Msg: fmt.Sprintf("no %q sheet: %v", SheetReview, err)}
	}
	if len(grid) == 0 {
		return nil, &FileError{Msg: "the Review sheet is empty"}
	}
	idx := map[string]int{}
	for i, h := range grid[0] {
		idx[strings.TrimSpace(h)] = i
	}
	for _, need := range []string{ColID, ColContentHash, ColAction, ColQuestion, ColAlternateQuestions, ColAnswer} {
		if _, ok := idx[need]; !ok {
			return nil, &FileError{Msg: fmt.Sprintf("the Review sheet has no %q column", need)}
		}
	}
	cell := func(row []string, name string) string {
		i, ok := idx[name]
		if !ok || i >= len(row) {
			return ""
		}
		return row[i]
	}
	out := make([]sheetRow, 0, len(grid)-1)
	for n, row := range grid[1:] {
		out = append(out, sheetRow{
			number: n + 2,
			id:     strings.TrimSpace(cell(row, ColID)), hash: strings.ToLower(strings.TrimSpace(cell(row, ColContentHash))),
			action:   strings.ToUpper(strings.TrimSpace(cell(row, ColAction))),
			question: cell(row, ColQuestion), alts: cell(row, ColAlternateQuestions), answer: cell(row, ColAnswer),
			note: strings.TrimSpace(cell(row, ColReviewNote)),
		})
	}
	return out, nil
}

// splitAlternates turns the cell back into the list: one per line, cleaned,
// empty lines dropped. It never dedupes or caps, so an unedited cell
// reproduces the stored list exactly.
func splitAlternates(cell string) []string {
	out := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(cell, "\r\n", "\n"), "\n") {
		if l := candidate.Clean(line); l != "" {
			out = append(out, l)
		}
	}
	return out
}

type auditContent struct {
	State              string   `json:"state"`
	Question           string   `json:"question"`
	AlternateQuestions []string `json:"alternateQuestions"`
	Answer             string   `json:"answer"`
	Flags              []string `json:"flags"`
	ContentHash        string   `json:"contentHash"`
	ReviewNote         string   `json:"reviewNote"`
}

func refuse(row sheetRow, code, detail string) RowResult {
	if r := []rune(detail); len(r) > maxDetailChars {
		detail = string(r[:maxDetailChars]) + "..."
	}
	return RowResult{Row: row.number, ID: row.id, Outcome: code, Detail: detail}
}

// applyRow decides one row. A returned error is an infrastructure error; a
// refusal is a RowResult whose Outcome is a code.
func applyRow(ctx context.Context, q *queries.Queries, row sheetRow, reviewer, fileName string, lim candidate.Limits) (RowResult, error) {
	if row.action == "" {
		return RowResult{Row: row.number, ID: row.id, Outcome: OutcomeSkipped}, nil
	}
	id, err := uuid.Parse(row.id)
	if err != nil {
		return refuse(row, CodeUnknownCandidate, "the id is not a UUID"), nil
	}
	c, err := q.LockCandidateForReview(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return refuse(row, CodeUnknownCandidate, "no candidate has this id"), nil
	}
	if err != nil {
		return RowResult{}, fmt.Errorf("lock candidate: %w", err)
	}
	res := RowResult{Row: row.number, ID: row.id, Candidate: id}
	switch domain.CandidateState(c.State) {
	case domain.CandidateStale:
		return refuse(row, CodeStale, "the candidate is STALE: its source changed"), nil
	case domain.CandidatePendingReview:
	default:
		return refuse(row, CodeNotPending, "the candidate is already "+c.State), nil
	}
	if want := hex.EncodeToString(c.ContentHash); row.hash != want {
		return refuse(row, CodeStale, "the candidate changed since the export (content hash differs)"), nil
	}
	if !c.VersionIsCurrent {
		return refuse(row, CodeStale, "the source file version is no longer current"), nil
	}

	action := domain.ReviewAction(row.action)
	if !slices.Contains(domain.ReviewActions(), action) {
		return refuse(row, CodeInvalidAction, fmt.Sprintf("%q is not APPROVE, REJECT or EDIT", row.action)), nil
	}
	note := row.note
	if note == "" && c.ReviewNote != nil {
		note = *c.ReviewNote
	}
	before := auditContent{
		State: c.State, Question: c.Question, AlternateQuestions: c.AlternateQuestions, Answer: c.Answer,
		Flags: c.Flags, ContentHash: hex.EncodeToString(c.ContentHash), ReviewNote: deref(c.ReviewNote),
	}
	after := before
	after.ReviewNote = note
	question, alternates, answer := c.Question, c.AlternateQuestions, c.Answer
	flags, hash := c.Flags, c.ContentHash

	switch action {
	case domain.ReviewApprove:
		if slices.Contains(c.Flags, string(domain.FlagNeedsShortening)) {
			return refuse(row, CodeShorteningRequired, "the answer is too long to publish as it is; choose EDIT and shorten it, or REJECT"), nil
		}
		cells := contentOf(c, candidate.Clean(row.question), splitAlternates(row.alts), candidate.Clean(row.answer))
		if h := candidate.ContentHash(cells); !bytes.Equal(h[:], c.ContentHash) {
			return refuse(row, CodeEditRequiresEditAction, "the text differs from the export; choose EDIT to approve it with your changes"), nil
		}
		after.State = string(domain.CandidateApproved)
		res.Outcome = OutcomeApproved
	case domain.ReviewReject:
		after.State = string(domain.CandidateRejected)
		res.Outcome = OutcomeRejected
	case domain.ReviewEdit:
		v := candidate.Validate(candidate.Draft{
			Language: c.Language, Question: row.question, AlternateQuestions: splitAlternates(row.alts), Answer: row.answer,
		}, lim)
		if !v.OK() {
			var parts []string
			for _, x := range v.Violations {
				parts = append(parts, x.Field+": "+x.String())
			}
			return refuse(row, CodeInvalidEdit, strings.Join(parts, "; ")), nil
		}
		edited := contentOf(c, v.Question, v.AlternateQuestions, v.Answer)
		h := candidate.ContentHash(edited)
		hash = h[:]
		question, alternates, answer = v.Question, v.AlternateQuestions, v.Answer
		flags = candidate.Flags(question, alternates, answer)
		after.State = string(domain.CandidateApproved)
		after.Question, after.AlternateQuestions, after.Answer, after.Flags = question, alternates, answer, flags
		after.ContentHash = hex.EncodeToString(hash)
		res.Outcome = OutcomeEdited
	}
	to := domain.CandidateState(after.State)
	if _, err := domain.CandidatePendingReview.Transition(to); err != nil {
		return RowResult{}, err
	}
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	n, err := q.ReviewCandidate(ctx, queries.ReviewCandidateParams{
		ID: id, ToState: after.State, Question: question, AlternateQuestions: alternates, Answer: answer,
		Flags: flags, ContentHash: hash, ReviewNote: notePtr,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return refuse(row, CodeDuplicateContent, "another candidate of this version already has this text"), nil
		}
		return RowResult{}, fmt.Errorf("update candidate: %w", err)
	}
	if n != 1 {
		return RowResult{}, fmt.Errorf("update candidate %s changed %d rows", id, n)
	}
	bj, _ := json.Marshal(before)
	aj, _ := json.Marshal(after)
	if err := q.InsertCandidateReview(ctx, queries.InsertCandidateReviewParams{
		CandidateID: id, Action: string(action), Reviewer: reviewer, Before: bj, After: aj, SourceFileName: fileName,
	}); err != nil {
		return RowResult{}, fmt.Errorf("record review: %w", err)
	}
	return res, nil
}

func contentOf(c queries.LockCandidateForReviewRow, question string, alternates []string, answer string) candidate.Content {
	return candidate.Content{
		Language: domain.Language(c.Language), Question: question, AlternateQuestions: alternates, Answer: answer,
		SourceRef: c.SourceRef, FileVersionID: c.FileVersionID,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}
