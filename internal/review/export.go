// SPDX-License-Identifier: Apache-2.0

// Package review is the Excel round trip of candidate review.
//
// Export writes the candidates awaiting review to a protected workbook: the
// id and the hidden content hash identify what the reviewer saw, the action
// column (APPROVE, REJECT, EDIT) carries the decision, and only the cells a
// reviewer may change are unlocked. Import reads the workbook back and
// applies the decisions in one transaction, refusing every row whose source
// changed since the export (STALE) and every approval whose text was edited
// without choosing EDIT.
//
// EDIT means "approve with my edits": the candidate takes the edited text,
// has its flags and content hash recomputed, and ends APPROVED.
package review

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
)

// Sheet names.
const (
	SheetReview = "Review"
	SheetInfo   = "ExportInfo"
)

// Column headers of the Review sheet, in order.
const (
	ColID                 = "id"
	ColContentHash        = "contentHash"
	ColAction             = "action"
	ColLanguage           = "language"
	ColQuestion           = "question"
	ColAlternateQuestions = "alternateQuestions"
	ColAnswer             = "answer"
	ColFlags              = "flags"
	ColSourceRef          = "sourceRef"
	ColSourceExcerpt      = "sourceExcerpt"
	ColReviewNote         = "reviewNote"
)

// Columns lists the headers in sheet order.
var Columns = []string{
	ColID, ColContentHash, ColAction, ColLanguage, ColQuestion, ColAlternateQuestions, ColAnswer,
	ColFlags, ColSourceRef, ColSourceExcerpt, ColReviewNote,
}

// ExcerptChars is how much of the source section the workbook shows.
const ExcerptChars = 500

// FormatVersion is written to the info sheet.
const FormatVersion = "aicc-knowledge review export v1"

var columnWidths = map[string]float64{
	ColID: 38, ColContentHash: 12, ColAction: 12, ColLanguage: 10, ColQuestion: 42, ColAlternateQuestions: 42,
	ColAnswer: 60, ColFlags: 20, ColSourceRef: 38, ColSourceExcerpt: 70, ColReviewNote: 32,
}

// ExportOptions selects the candidates to export.
type ExportOptions struct {
	// States defaults to PENDING_REVIEW.
	States []domain.CandidateState
	// Language is EN, ZH or empty for both.
	Language domain.Language
	// Now stamps the info sheet; defaults to time.Now.
	Now func() time.Time
}

// Export builds the review workbook in memory and reports how many candidates
// it holds. The caller saves it with SaveAs and closes it.
func Export(ctx context.Context, st *store.Store, opts ExportOptions) (*excelize.File, int, error) {
	if len(opts.States) == 0 {
		opts.States = []domain.CandidateState{domain.CandidatePendingReview}
	}
	if opts.Language != "" && !opts.Language.Valid() {
		return nil, 0, fmt.Errorf("language %q is not EN or ZH", opts.Language)
	}
	states := make([]string, len(opts.States))
	for i, s := range opts.States {
		if !isCandidateState(s) {
			return nil, 0, fmt.Errorf("state %q is not a candidate state", s)
		}
		states[i] = string(s)
	}
	rows, err := st.Queries.ListCandidatesForExport(ctx, queries.ListCandidatesForExportParams{States: states, Language: string(opts.Language)})
	if err != nil {
		return nil, 0, fmt.Errorf("list candidates: %w", err)
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", SheetReview); err != nil {
		return nil, 0, err
	}
	fail := func(err error) (*excelize.File, int, error) {
		_ = f.Close()
		return nil, 0, err
	}
	locked, unlocked, header, err := styles(f)
	if err != nil {
		return fail(err)
	}
	for i, name := range Columns {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellStr(SheetReview, cell, name); err != nil {
			return fail(err)
		}
	}
	editable := map[string]bool{ColAction: true, ColQuestion: true, ColAlternateQuestions: true, ColAnswer: true, ColReviewNote: true}
	for r, c := range rows {
		values := map[string]string{
			ColID: c.ID.String(), ColContentHash: fmt.Sprintf("%x", c.ContentHash), ColAction: "", ColLanguage: c.Language,
			ColQuestion: c.Question, ColAlternateQuestions: strings.Join(c.AlternateQuestions, "\n"), ColAnswer: c.Answer,
			ColFlags: strings.Join(c.Flags, ", "), ColSourceRef: c.SourceRef, ColSourceExcerpt: excerpt(c.SectionBody),
			ColReviewNote: c.ReviewNote,
		}
		for i, name := range Columns {
			cell, _ := excelize.CoordinatesToCellName(i+1, r+2)
			if err := f.SetCellStr(SheetReview, cell, sanitize(values[name])); err != nil {
				return fail(err)
			}
			style := locked
			if editable[name] {
				style = unlocked
			}
			if err := f.SetCellStyle(SheetReview, cell, cell, style); err != nil {
				return fail(err)
			}
		}
	}
	last := len(Columns)
	lastCell, _ := excelize.CoordinatesToCellName(last, 1)
	if err := f.SetCellStyle(SheetReview, "A1", lastCell, header); err != nil {
		return fail(err)
	}
	// excelize leaves the used range at A1; readers that trust it see one cell.
	if err := f.SetSheetDimension(SheetReview, fmt.Sprintf("A1:%s%d", columnLetter(ColReviewNote), len(rows)+1)); err != nil {
		return fail(err)
	}
	for i, name := range Columns {
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth(SheetReview, col, col, columnWidths[name]); err != nil {
			return fail(err)
		}
	}
	if err := f.SetColVisible(SheetReview, columnLetter(ColContentHash), false); err != nil {
		return fail(err)
	}
	if err := f.SetPanes(SheetReview, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
		Selection: []excelize.Selection{{SQRef: "A2", ActiveCell: "A2", Pane: "bottomLeft"}}}); err != nil {
		return fail(err)
	}
	dv := excelize.NewDataValidation(true)
	dv.Sqref = fmt.Sprintf("%[1]s2:%[1]s%d", columnLetter(ColAction), max(len(rows)+1, 2))
	if err := dv.SetDropList(actionList()); err != nil {
		return fail(err)
	}
	dv.SetError(excelize.DataValidationErrorStyleStop, "Invalid action", "Choose APPROVE, REJECT or EDIT, or leave the cell empty.")
	dv.SetInput("Action", "APPROVE, REJECT, or EDIT (approve with the edits you made). Empty means no decision yet.")
	if err := f.AddDataValidation(SheetReview, dv); err != nil {
		return fail(err)
	}
	if err := f.ProtectSheet(SheetReview, &excelize.SheetProtectionOptions{
		SelectLockedCells: true, SelectUnlockedCells: true, FormatColumns: true, FormatRows: true,
	}); err != nil {
		return fail(err)
	}

	if _, err := f.NewSheet(SheetInfo); err != nil {
		return fail(err)
	}
	stateNames := make([]string, len(opts.States))
	for i, s := range opts.States {
		stateNames[i] = string(s)
	}
	lang := string(opts.Language)
	if lang == "" {
		lang = "ALL"
	}
	for i, kv := range [][2]string{
		{"format", FormatVersion}, {"exportedAt", now().UTC().Format(time.RFC3339)},
		{"states", strings.Join(stateNames, ",")}, {"language", lang}, {"rows", fmt.Sprint(len(rows))},
	} {
		a, _ := excelize.CoordinatesToCellName(1, i+1)
		b, _ := excelize.CoordinatesToCellName(2, i+1)
		if err := f.SetCellStr(SheetInfo, a, kv[0]); err != nil {
			return fail(err)
		}
		if err := f.SetCellStr(SheetInfo, b, kv[1]); err != nil {
			return fail(err)
		}
	}
	if err := f.SetSheetVisible(SheetInfo, false); err != nil {
		return fail(err)
	}
	f.SetActiveSheet(0)
	return f, len(rows), nil
}

func isCandidateState(s domain.CandidateState) bool {
	for _, v := range domain.CandidateStates() {
		if v == s {
			return true
		}
	}
	return false
}

func actionList() []string {
	var out []string
	for _, a := range domain.ReviewActions() {
		out = append(out, string(a))
	}
	return out
}

func columnLetter(name string) string {
	for i, c := range Columns {
		if c == name {
			l, _ := excelize.ColumnNumberToName(i + 1)
			return l
		}
	}
	panic("unknown column " + name)
}

func styles(f *excelize.File) (locked, unlocked, header int, err error) {
	align := &excelize.Alignment{Vertical: "top", WrapText: true}
	if locked, err = f.NewStyle(&excelize.Style{Alignment: align, Protection: &excelize.Protection{Locked: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#F2F2F2"}}}); err != nil {
		return
	}
	if unlocked, err = f.NewStyle(&excelize.Style{Alignment: align, Protection: &excelize.Protection{Locked: false}}); err != nil {
		return
	}
	header, err = f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true}, Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#D9E1F2"}}, Protection: &excelize.Protection{Locked: true},
	})
	return
}

// excerpt returns the start of a section body for the reviewer to check the
// answer against.
func excerpt(body string) string {
	body = strings.TrimSpace(body)
	if r := []rune(body); len(r) > ExcerptChars {
		return string(r[:ExcerptChars]) + "..."
	}
	return body
}

// sanitize drops the control characters XML cannot carry.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
}

// LanguageOf is for the CLI: it parses a --language flag.
func LanguageOf(s string) (domain.Language, error) {
	if s == "" {
		return "", nil
	}
	l := domain.Language(strings.ToUpper(s))
	if !l.Valid() {
		return "", fmt.Errorf("language %q is not EN or ZH", s)
	}
	return l, nil
}
