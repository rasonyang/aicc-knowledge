// SPDX-License-Identifier: Apache-2.0

package parse

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rasonyang/aicc-knowledge/internal/candidate"
	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/facts"
	"github.com/rasonyang/aicc-knowledge/internal/jobs"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// analysis is the pure result of reading a workbook under an optional mapping.
type analysis struct {
	sections []section
	warnings []Warning
	// tables holds the imported tables; empty when there is no mapping or
	// when issues is not.
	tables []facts.TableImport
	// issues are the facts import problems; any issue means FACTS_INVALID.
	issues []Warning
	// qaIssues are the Q&A import problems (QA_INVALID); conflicts name sheets
	// claimed by both a facts and a Q&A mapping (QA_SHEET_CONFLICT).
	qaIssues  []Warning
	conflicts []Warning
}

// failure returns the code and details of the problems that stop a workbook
// from being parsed under its mappings, or "" when there are none.
func (a *analysis) failure() (string, []Warning) {
	var code string
	var details []Warning
	switch {
	case len(a.conflicts) > 0:
		code, details = CodeQASheetConflict, a.conflicts
	case len(a.issues) > 0:
		code, details = CodeFactsInvalid, a.issues
	case len(a.qaIssues) > 0:
		code, details = CodeQAInvalid, a.qaIssues
	default:
		return "", nil
	}
	return code, append(append([]Warning{}, details...), a.warnings...)
}

func addWarning(list *[]Warning, seen map[string]bool, w Warning) {
	k := w.Code + "\x00" + w.Location
	if seen[k] {
		return
	}
	seen[k] = true
	*list = append(*list, w)
}

// analyzeWorkbook extracts the content sections of the sheets no mapping
// covers, one section per row of the sheets a Q&A mapping covers, and imports
// every table of a facts mapping. Either mapping may be nil. A non-nil error
// is a file-level failure (*xlsx.Error).
func analyzeWorkbook(data []byte, objectKey string, mf *xlsx.MappingFile, qf *xlsx.QAMappingFile) (*analysis, error) {
	an := &analysis{warnings: []Warning{}}
	if shared := xlsx.SharedSheets(qf, mf); len(shared) > 0 {
		for _, sheet := range shared {
			an.conflicts = append(an.conflicts, Warning{
				Code: CodeQASheetConflict, Location: sheet,
				Detail: fmt.Sprintf("sheet %q is claimed by both a facts mapping and a Q&A mapping", sheet),
			})
		}
		return an, nil
	}
	var opts xlsx.ExtractOptions
	if mf != nil {
		opts.ExcludeSheets = append(opts.ExcludeSheets, mf.Sheets()...)
	}
	opts.ExcludeSheets = append(opts.ExcludeSheets, qf.SheetNames()...)
	content, err := xlsx.ExtractBytes(data, opts)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, s := range content.Sections {
		an.sections = append(an.sections, section{
			ordinal: s.Ordinal, kind: domain.SectionKindXlsxChunk, headingPath: s.HeadingPath, level: len(s.HeadingPath),
			body: s.Text, sourceRef: objectKey + "#" + s.SourceRef,
		})
	}
	for _, x := range content.Warnings {
		addWarning(&an.warnings, seen, Warning{Code: x.Code, Detail: x.Detail, Location: x.Location})
	}
	if err := addQARows(an, data, objectKey, qf, seen); err != nil {
		return nil, err
	}
	if mf == nil {
		return an, nil
	}
	var tables []facts.TableImport
	for _, m := range mf.Tables {
		res, err := xlsx.ImportFactsBytes(data, &m)
		if err != nil {
			return nil, err
		}
		for _, x := range res.Warnings {
			addWarning(&an.warnings, seen, Warning{Code: x.Code, Detail: x.Detail, Location: x.Location})
		}
		for _, i := range res.Issues {
			detail := i.Detail
			if i.Column != "" {
				detail = fmt.Sprintf("table %s column %s: %s", m.Table, i.Column, i.Detail)
			} else {
				detail = fmt.Sprintf("table %s: %s", m.Table, i.Detail)
			}
			an.issues = append(an.issues, Warning{Code: i.Code, Detail: detail, Location: i.Location})
		}
		if res.OK() {
			tables = append(tables, facts.TableImport{Mapping: m, Rows: res.Rows})
		}
	}
	if len(an.issues) == 0 {
		an.tables = tables
	}
	return an, nil
}

// addQARows imports the Q&A sheets. Their sections come after the content
// sections, so the ordinals of the content sections do not depend on them.
func addQARows(an *analysis, data []byte, objectKey string, qf *xlsx.QAMappingFile, seen map[string]bool) error {
	if qf == nil {
		return nil
	}
	res, err := xlsx.ImportQABytes(data, qf)
	if err != nil {
		return err
	}
	for _, x := range res.Warnings {
		addWarning(&an.warnings, seen, Warning{Code: x.Code, Detail: x.Detail, Location: x.Location})
	}
	for _, i := range res.Issues {
		detail := i.Detail
		if i.Column != "" {
			detail = fmt.Sprintf("Q&A %s: %s", i.Column, i.Detail)
		}
		an.qaIssues = append(an.qaIssues, Warning{Code: i.Code, Detail: detail, Location: i.Location})
	}
	ordinal := len(an.sections)
	for _, r := range res.Rows {
		lang := r.Language
		if lang == xlsx.QALanguageAuto {
			d, ok := candidate.DetectLanguage(r.Question + " " + r.Answer)
			if !ok {
				addWarning(&an.warnings, seen, Warning{Code: CodeQALanguageUnknown, Detail: fmt.Sprintf("row %d has no letters to detect a language from and is skipped", r.Row), Location: r.Sheet + "!A" + fmt.Sprint(r.Row)})
				continue
			}
			lang = string(d)
		}
		ordinal++
		an.sections = append(an.sections, section{
			ordinal: ordinal, kind: domain.SectionKindXlsxQARow, headingPath: []string{r.Sheet}, level: 1,
			body: r.Answer, sourceRef: objectKey + "#" + r.SourceRef,
			question: r.Question, alternates: r.Alternates, language: lang,
		})
	}
	return nil
}

// counterpart is the current version of the sibling file of a workbook or
// mapping, as seen at one moment.
type counterpart struct {
	Valid        bool
	ID           uuid.UUID
	SourceFileID uuid.UUID
	State        string
	SHA          []byte
}

func (c counterpart) same(o counterpart) bool {
	return c.Valid == o.Valid && c.ID == o.ID && c.State == o.State
}

func (w *Worker) peek(ctx context.Context, bucket, key string) (counterpart, error) {
	row, err := w.Store.Queries.GetCurrentVersionByObjectKey(ctx, queries.GetCurrentVersionByObjectKeyParams{Bucket: bucket, ObjectKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return counterpart{}, nil
	}
	if err != nil {
		return counterpart{}, fmt.Errorf("load %s: %w", key, err)
	}
	return counterpart{Valid: true, ID: row.ID, SourceFileID: row.SourceFileID, State: row.State, SHA: row.SHA256}, nil
}

// lockCounterpart reads and locks the counterpart's current version inside the
// transaction, so a scan cannot supersede it before the import commits.
func (w *Worker) lockCounterpart(ctx context.Context, q *queries.Queries, bucket, key string) (counterpart, error) {
	row, err := q.GetCurrentVersionByObjectKey(ctx, queries.GetCurrentVersionByObjectKeyParams{Bucket: bucket, ObjectKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return counterpart{}, nil
	}
	if err != nil {
		return counterpart{}, fmt.Errorf("load %s: %w", key, err)
	}
	locked, err := q.LockFileVersion(ctx, row.ID)
	if err != nil {
		return counterpart{}, fmt.Errorf("lock %s: %w", key, err)
	}
	if locked.SupersededAt.Valid {
		return counterpart{}, errStale
	}
	return counterpart{Valid: true, ID: row.ID, SourceFileID: row.SourceFileID, State: locked.State, SHA: row.SHA256}, nil
}

// factsSibling returns the facts mapping next to the workbook when its
// current version is PARSED, with the snapshot it was read under. A mapping
// that no longer parses, or whose object changed after its scan, counts as
// absent.
func (w *Worker) factsSibling(ctx context.Context, bucket, wkey string) (*xlsx.MappingFile, counterpart, error) {
	key := mappingKeyOf(wkey)
	snap, err := w.peek(ctx, bucket, key)
	if err != nil {
		return nil, snap, err
	}
	if !snap.Valid || snap.State != string(domain.FileVersionParsed) {
		return nil, snap, nil
	}
	data, reason, err := w.fetch(ctx, key, snap.SHA)
	if err != nil {
		return nil, snap, err
	}
	if reason != "" {
		w.log().Warn("mapping object changed after its scan; importing without it", "key", key, "reason", reason)
		return nil, snap, nil
	}
	mf, err := xlsx.ParseMappingFile(data)
	if err != nil {
		w.log().Error("a PARSED mapping no longer parses; importing without it", "key", key, "error", err)
		return nil, snap, nil
	}
	return mf, snap, nil
}

// qaSibling is factsSibling for the Q&A mapping.
func (w *Worker) qaSibling(ctx context.Context, bucket, wkey string) (*xlsx.QAMappingFile, counterpart, error) {
	key := qaKeyOf(wkey)
	snap, err := w.peek(ctx, bucket, key)
	if err != nil {
		return nil, snap, err
	}
	if !snap.Valid || snap.State != string(domain.FileVersionParsed) {
		return nil, snap, nil
	}
	data, reason, err := w.fetch(ctx, key, snap.SHA)
	if err != nil {
		return nil, snap, err
	}
	if reason != "" {
		w.log().Warn("Q&A mapping object changed after its scan; importing without it", "key", key, "reason", reason)
		return nil, snap, nil
	}
	qf, err := xlsx.ParseQAMappingFile(data)
	if err != nil {
		w.log().Error("a PARSED Q&A mapping no longer parses; importing without it", "key", key, "error", err)
		return nil, snap, nil
	}
	return qf, snap, nil
}

// checkCounterpart locks the counterpart's current version and reports errStale
// when it is not the one that was read.
func (w *Worker) checkCounterpart(ctx context.Context, q *queries.Queries, bucket, key string, snap counterpart) (counterpart, error) {
	cp, err := w.lockCounterpart(ctx, q, bucket, key)
	if err != nil {
		return cp, err
	}
	if !cp.same(snap) {
		return cp, errStale
	}
	return cp, nil
}

func (w *Worker) parseWorkbook(ctx context.Context, job jobs.Job, t target, data []byte) (string, string, error) {
	bucket, mkey, qkey := t.ver.Bucket, mappingKeyOf(t.ver.ObjectKey), qaKeyOf(t.ver.ObjectKey)
	mf, snap, err := w.factsSibling(ctx, bucket, t.ver.ObjectKey)
	if err != nil {
		return t.kind, "", err
	}
	qf, qsnap, err := w.qaSibling(ctx, bucket, t.ver.ObjectKey)
	if err != nil {
		return t.kind, "", err
	}

	an, err := analyzeWorkbook(data, t.ver.ObjectKey, mf, qf)
	if err != nil {
		code := xlsx.CodeOf(err)
		if code == "" {
			code = xlsx.CodeXLSXCorrupt
		}
		return w.fail(ctx, job, t, code, []Warning{{Code: code, Detail: err.Error()}})
	}
	if code, details := an.failure(); code != "" {
		return w.fail(ctx, job, t, code, details)
	}
	return w.succeed(ctx, job, t, an.sections, an.warnings, func(q *queries.Queries, tx pgx.Tx) error {
		cp, err := w.checkCounterpart(ctx, q, bucket, mkey, snap)
		if err != nil {
			return err
		}
		if _, err := w.checkCounterpart(ctx, q, bucket, qkey, qsnap); err != nil {
			return err
		}
		if mf == nil {
			return nil
		}
		wid := t.ver.ID
		return facts.Apply(ctx, tx, facts.ApplyInput{
			MappingSourceFileID: cp.SourceFileID, MappingVersionID: cp.ID,
			WorkbookSourceFileID: &t.ver.SourceFileID, WorkbookVersionID: &wid,
			WorkbookObjectKey: t.ver.ObjectKey, Tables: an.tables,
		}, q)
	})
}

func mappingWarnings(err error) []Warning {
	var me *xlsx.MappingError
	if errors.As(err, &me) {
		out := make([]Warning, 0, len(me.Issues))
		for _, i := range me.Issues {
			out = append(out, Warning{Code: xlsx.CodeMappingInvalid, Detail: i})
		}
		return out
	}
	return []Warning{{Code: xlsx.CodeMappingInvalid, Detail: err.Error()}}
}

func (w *Worker) parseMapping(ctx context.Context, job jobs.Job, t target, data []byte) (string, string, error) {
	mf, err := xlsx.ParseMappingFile(data)
	if err != nil {
		return w.fail(ctx, job, t, xlsx.CodeMappingInvalid, mappingWarnings(err))
	}
	bucket, wkey := t.ver.Bucket, workbookKeyOf(t.ver.ObjectKey)
	snap, err := w.peek(ctx, bucket, wkey)
	if err != nil {
		return t.kind, "", err
	}
	var wbSource *uuid.UUID
	switch sf, err := w.Store.Queries.GetSourceFileByObjectKey(ctx, queries.GetSourceFileByObjectKeyParams{Bucket: bucket, ObjectKey: wkey}); {
	case err == nil:
		wbSource = &sf.ID
	case !errors.Is(err, pgx.ErrNoRows):
		return t.kind, "", fmt.Errorf("load source file %s: %w", wkey, err)
	}

	qf, qsnap, err := w.qaSibling(ctx, bucket, wkey)
	if err != nil {
		return t.kind, "", err
	}

	var an *analysis
	var issues []Warning // workbook-side import problems; the mapping still parses
	issueCode := CodeFactsInvalid
	if snap.Valid && snap.State == string(domain.FileVersionParsed) {
		wdata, reason, err := w.fetch(ctx, wkey, snap.SHA)
		if err != nil {
			return t.kind, "", err
		}
		if reason == "" {
			if an, err = analyzeWorkbook(wdata, wkey, mf, qf); err != nil {
				return t.kind, "", fmt.Errorf("re-read workbook %s: %w", wkey, err)
			}
			if code, details := an.failure(); code != "" {
				issueCode = code
				// Blame rule: import problems in the data are the workbook's,
				// not the mapping's. The mapping stays PARSED so a fixed
				// workbook imports without operator action. The workbook is
				// already PARSED (there is no PARSED -> PARSE_FAILED edge), so
				// its issues are recorded in its parse_warnings and the tables
				// stay UNAVAILABLE/FACTS_INVALID.
				issues = details
				an = nil
			}
		} else {
			w.log().Warn("workbook object changed after its scan; registering the tables without rows", "key", wkey, "reason", reason)
		}
	}

	return w.succeed(ctx, job, t, nil, nil, func(q *queries.Queries, tx pgx.Tx) error {
		cp, err := w.lockCounterpart(ctx, q, bucket, wkey)
		if err != nil {
			return err
		}
		if !cp.same(snap) {
			return errStale // the workbook moved (for example became PARSED) since it was read
		}
		if _, err := w.checkCounterpart(ctx, q, bucket, qaKeyOf(wkey), qsnap); err != nil {
			return err
		}
		in := facts.ApplyInput{MappingSourceFileID: t.ver.SourceFileID, MappingVersionID: t.ver.ID, WorkbookSourceFileID: wbSource}
		if an == nil {
			in.UnavailableCode = facts.ReasonWorkbookNotParsed
			if issues != nil {
				in.UnavailableCode = issueCode
				raw, err := encodeWarnings(issues)
				if err != nil {
					return err
				}
				if err := q.SetParseWarnings(ctx, queries.SetParseWarningsParams{ID: cp.ID, ParseWarnings: raw}); err != nil {
					return fmt.Errorf("record import issues on the workbook: %w", err)
				}
			}
			for _, m := range mf.Tables {
				in.Tables = append(in.Tables, facts.TableImport{Mapping: m})
			}
			return facts.Apply(ctx, tx, in, q)
		}
		in.WorkbookVersionID, in.WorkbookObjectKey, in.Tables = &cp.ID, wkey, an.tables
		// The sheets this mapping covers stop being Q&A content, so the
		// workbook's sections and warnings are derived again under it.
		if err := w.writeSections(ctx, q, cp.ID, an.sections); err != nil {
			return err
		}
		raw, err := encodeWarnings(an.warnings)
		if err != nil {
			return err
		}
		if err := q.SetParseWarnings(ctx, queries.SetParseWarningsParams{ID: cp.ID, ParseWarnings: raw}); err != nil {
			return fmt.Errorf("update workbook warnings: %w", err)
		}
		return facts.Apply(ctx, tx, in, q)
	})
}

// parseQAMapping validates a `<name>.qa.yaml` file. A mapping that parses has
// no state of its own to import: when its workbook is already PARSED, the
// workbook's sections are derived again under it (the covered sheets stop
// being content and their rows become Q&A sections), and generation is
// queued to top up the new rows. Whichever of the two files parses last
// therefore does the work, as with facts; the workbook parse reads the
// mapping, and the final transaction re-checks both siblings under a lock.
//
// The blame rule is the facts one: a problem that comes from the workbook's
// data (sheet or header not found, a sheet claimed twice) fails nothing here.
// The mapping stays PARSED, the workbook keeps its sections, and the issues
// are recorded in the workbook's parse_warnings.
func (w *Worker) parseQAMapping(ctx context.Context, job jobs.Job, t target, data []byte) (string, string, error) {
	qf, err := xlsx.ParseQAMappingFile(data)
	if err != nil {
		return w.fail(ctx, job, t, xlsx.CodeMappingInvalid, mappingWarnings(err))
	}
	bucket, wkey := t.ver.Bucket, workbookKeyOfQA(t.ver.ObjectKey)
	snap, err := w.peek(ctx, bucket, wkey)
	if err != nil {
		return t.kind, "", err
	}
	mf, fsnap, err := w.factsSibling(ctx, bucket, wkey)
	if err != nil {
		return t.kind, "", err
	}

	var an *analysis
	var issues []Warning
	if snap.Valid && snap.State == string(domain.FileVersionParsed) {
		wdata, reason, err := w.fetch(ctx, wkey, snap.SHA)
		if err != nil {
			return t.kind, "", err
		}
		if reason == "" {
			if an, err = analyzeWorkbook(wdata, wkey, mf, qf); err != nil {
				return t.kind, "", fmt.Errorf("re-read workbook %s: %w", wkey, err)
			}
			if code, details := an.failure(); code != "" {
				issues = details
				an = nil
			}
		} else {
			w.log().Warn("workbook object changed after its scan; the Q&A mapping waits for its next parse", "key", wkey, "reason", reason)
		}
	}

	return w.succeed(ctx, job, t, nil, nil, func(q *queries.Queries, tx pgx.Tx) error {
		cp, err := w.checkCounterpart(ctx, q, bucket, wkey, snap)
		if err != nil {
			return err
		}
		if _, err := w.checkCounterpart(ctx, q, bucket, mappingKeyOf(wkey), fsnap); err != nil {
			return err
		}
		if an == nil {
			if issues == nil {
				return nil // no workbook yet, or not parsed: its parse will read this mapping
			}
			raw, err := encodeWarnings(issues)
			if err != nil {
				return err
			}
			if err := q.SetParseWarnings(ctx, queries.SetParseWarningsParams{ID: cp.ID, ParseWarnings: raw}); err != nil {
				return fmt.Errorf("record import issues on the workbook: %w", err)
			}
			return nil
		}
		if err := w.writeSections(ctx, q, cp.ID, an.sections); err != nil {
			return err
		}
		raw, err := encodeWarnings(an.warnings)
		if err != nil {
			return err
		}
		if err := q.SetParseWarnings(ctx, queries.SetParseWarningsParams{ID: cp.ID, ParseWarnings: raw}); err != nil {
			return fmt.Errorf("update workbook warnings: %w", err)
		}
		if len(an.sections) > 0 {
			// Generation tops up the Q&A rows that have no candidate yet.
			if _, err := jobs.New(q).Rearm(ctx, jobs.Enqueue{
				Kind: jobs.KindGenerate, Payload: payload{FileVersionID: cp.ID}, DedupeKey: cp.ID.String(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
