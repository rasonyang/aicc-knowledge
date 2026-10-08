// SPDX-License-Identifier: Apache-2.0

package parse

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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
	// issues are the import problems; any issue means FACTS_INVALID.
	issues []Warning
}

func addWarning(list *[]Warning, seen map[string]bool, w Warning) {
	k := w.Code + "\x00" + w.Location
	if seen[k] {
		return
	}
	seen[k] = true
	*list = append(*list, w)
}

// analyzeWorkbook extracts the content sections of the sheets no table covers
// and, when there is a mapping, imports every mapped table. A non-nil error is
// a file-level failure (*xlsx.Error).
func analyzeWorkbook(data []byte, objectKey string, mf *xlsx.MappingFile) (*analysis, error) {
	var opts xlsx.ExtractOptions
	if mf != nil {
		opts.ExcludeSheets = mf.Sheets()
	}
	content, err := xlsx.ExtractBytes(data, opts)
	if err != nil {
		return nil, err
	}
	an := &analysis{warnings: []Warning{}}
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

func (w *Worker) parseWorkbook(ctx context.Context, job jobs.Job, t target, data []byte) (string, string, error) {
	bucket, mkey := t.ver.Bucket, mappingKeyOf(t.ver.ObjectKey)
	snap, err := w.peek(ctx, bucket, mkey)
	if err != nil {
		return t.kind, "", err
	}
	var mf *xlsx.MappingFile
	if snap.Valid && snap.State == string(domain.FileVersionParsed) {
		mdata, reason, err := w.fetch(ctx, mkey, snap.SHA)
		if err != nil {
			return t.kind, "", err
		}
		if reason == "" {
			if mf, err = xlsx.ParseMappingFile(mdata); err != nil {
				w.log().Error("a PARSED mapping no longer parses; importing without it", "key", mkey, "error", err)
				mf = nil
			}
		} else {
			w.log().Warn("mapping object changed after its scan; importing without it", "key", mkey, "reason", reason)
		}
	}

	an, err := analyzeWorkbook(data, t.ver.ObjectKey, mf)
	if err != nil {
		code := xlsx.CodeOf(err)
		if code == "" {
			code = xlsx.CodeXLSXCorrupt
		}
		return w.fail(ctx, job, t, code, []Warning{{Code: code, Detail: err.Error()}})
	}
	if len(an.issues) > 0 {
		return w.fail(ctx, job, t, CodeFactsInvalid, append(an.issues, an.warnings...))
	}
	return w.succeed(ctx, job, t, an.sections, an.warnings, func(q *queries.Queries, tx pgx.Tx) error {
		cp, err := w.lockCounterpart(ctx, q, bucket, mkey)
		if err != nil {
			return err
		}
		if !cp.same(snap) {
			return errStale
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

	var an *analysis
	var issues []Warning // workbook-side import problems; the mapping still parses
	if snap.Valid && snap.State == string(domain.FileVersionParsed) {
		wdata, reason, err := w.fetch(ctx, wkey, snap.SHA)
		if err != nil {
			return t.kind, "", err
		}
		if reason == "" {
			if an, err = analyzeWorkbook(wdata, wkey, mf); err != nil {
				return t.kind, "", fmt.Errorf("re-read workbook %s: %w", wkey, err)
			}
			if len(an.issues) > 0 {
				// Blame rule: import problems in the data are the workbook's,
				// not the mapping's. The mapping stays PARSED so a fixed
				// workbook imports without operator action. The workbook is
				// already PARSED (there is no PARSED -> PARSE_FAILED edge), so
				// its issues are recorded in its parse_warnings and the tables
				// stay UNAVAILABLE/FACTS_INVALID.
				issues = append(an.issues, an.warnings...)
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
		in := facts.ApplyInput{MappingSourceFileID: t.ver.SourceFileID, MappingVersionID: t.ver.ID, WorkbookSourceFileID: wbSource}
		if an == nil {
			in.UnavailableCode = facts.ReasonWorkbookNotParsed
			if issues != nil {
				in.UnavailableCode = CodeFactsInvalid
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
