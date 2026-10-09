// SPDX-License-Identifier: Apache-2.0

package facts

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
	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// importLockKey serializes every fact import and definition change ("KNFT").
const importLockKey int64 = 0x4b4e4654

// LockImports takes the transaction-scoped advisory lock that serializes fact
// imports. Call it first in any transaction that will call Apply, before
// locking file versions, so concurrent imports cannot deadlock on each other.
func LockImports(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", importLockKey); err != nil {
		return fmt.Errorf("take facts import lock: %w", err)
	}
	return nil
}

// ConflictError means a table name is claimed by a mapping file or workbook
// that is not its owner.
type ConflictError struct {
	Table  string
	Detail string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s: table %q: %s", CodeNameConflict, e.Table, e.Detail)
}

// TableImport is one mapped table with its validated rows.
type TableImport struct {
	Mapping xlsx.Mapping
	// Rows are the rows of a successful xlsx.ImportFacts.
	Rows []xlsx.FactRow
}

// ApplyInput describes the effect of a successfully parsed mapping file.
type ApplyInput struct {
	MappingSourceFileID uuid.UUID
	MappingVersionID    uuid.UUID
	// WorkbookSourceFileID is nil when the workbook has never been seen.
	WorkbookSourceFileID *uuid.UUID
	// WorkbookVersionID is the workbook version whose rows are imported; nil
	// registers the table definitions only, leaving the tables UNAVAILABLE
	// with UnavailableCode.
	WorkbookVersionID *uuid.UUID
	UnavailableCode   string
	// WorkbookObjectKey prefixes every row's source ref ("<key>#<range>").
	WorkbookObjectKey string
	Tables            []TableImport
}

// Apply registers the tables a mapping file declares and, when
// WorkbookVersionID is set, replaces their rows and points them at the pair
// (workbook version, mapping version), all in tx. A table the file no longer
// declares becomes UNAVAILABLE (TABLE_NOT_DECLARED). A table name that belongs
// to another mapping file with a live version, or to another workbook, is a
// *ConflictError and nothing is written for the table. The caller holds
// LockImports and has locked both file versions.
func Apply(ctx context.Context, tx pgx.Tx, in ApplyInput, q *queries.Queries) error {
	declared := map[string]bool{}
	importing := in.WorkbookVersionID != nil
	reason := in.UnavailableCode
	if reason == "" {
		reason = ReasonWorkbookNotParsed
	}
	for _, ti := range in.Tables {
		m := ti.Mapping
		declared[m.Table] = true
		cols, err := json.Marshal(m.Columns)
		if err != nil {
			return fmt.Errorf("encode columns: %w", err)
		}
		var code *string
		if !importing {
			code = &reason
		}
		existing, err := q.GetFactTableByNameForUpdate(ctx, m.Table)
		var tableID uuid.UUID
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			t, err := q.InsertFactTable(ctx, queries.InsertFactTableParams{
				Name: m.Table, WorkbookSourceFileID: in.WorkbookSourceFileID, MappingSourceFileID: in.MappingSourceFileID,
				Columns: cols, KeyColumns: m.KeyColumns, LastErrorCode: code,
			})
			if err != nil {
				return fmt.Errorf("insert fact table %s: %w", m.Table, err)
			}
			tableID = t.ID
		case err != nil:
			return fmt.Errorf("load fact table %s: %w", m.Table, err)
		default:
			if err := checkOwner(ctx, q, existing, in); err != nil {
				return err
			}
			workbookSF := in.WorkbookSourceFileID
			if workbookSF == nil {
				workbookSF = existing.WorkbookSourceFileID
			}
			if err := q.UpdateFactTableDefinition(ctx, queries.UpdateFactTableDefinitionParams{
				ID: existing.ID, WorkbookSourceFileID: workbookSF, MappingSourceFileID: in.MappingSourceFileID,
				Columns: cols, KeyColumns: m.KeyColumns, LastErrorCode: code,
			}); err != nil {
				return fmt.Errorf("update fact table %s: %w", m.Table, err)
			}
			tableID = existing.ID
		}
		if !importing {
			continue
		}
		if _, err := q.DeleteFactRows(ctx, tableID); err != nil {
			return fmt.Errorf("delete old rows of %s: %w", m.Table, err)
		}
		rows := make([]queries.InsertFactRowsParams, 0, len(ti.Rows))
		for _, fr := range ti.Rows {
			p, err := rowParams(tableID, *in.WorkbookVersionID, in.MappingVersionID, in.WorkbookObjectKey, fr)
			if err != nil {
				return fmt.Errorf("table %s row %d: %w", m.Table, fr.Row, err)
			}
			rows = append(rows, p)
		}
		if n, err := q.InsertFactRows(ctx, rows); err != nil || n != int64(len(rows)) {
			return fmt.Errorf("insert rows of %s: inserted %d of %d: %v", m.Table, n, len(rows), err)
		}
		if err := q.PointFactTable(ctx, queries.PointFactTableParams{
			ID: tableID, WorkbookVersionID: in.WorkbookVersionID, MappingVersionID: &in.MappingVersionID,
		}); err != nil {
			return fmt.Errorf("point fact table %s: %w", m.Table, err)
		}
	}

	owned, err := q.ListFactTablesByMappingSource(ctx, in.MappingSourceFileID)
	if err != nil {
		return fmt.Errorf("list tables of the mapping: %w", err)
	}
	for _, t := range owned {
		if declared[t.Name] {
			continue
		}
		if err := q.ClearFactTable(ctx, queries.ClearFactTableParams{ID: t.ID, ErrorCode: ReasonNotDeclared}); err != nil {
			return fmt.Errorf("clear undeclared table %s: %w", t.Name, err)
		}
	}
	return nil
}

// checkOwner allows a mapping to (re)define an existing table only when the
// mapping file is the table's owner, or the previous owner has no live version
// left (it was removed). The workbook follows the mapping file by naming
// convention, so it can only differ when ownership changes hands; for the same
// mapping file a different workbook is refused as a defensive check.
func checkOwner(ctx context.Context, q *queries.Queries, t queries.FactTable, in ApplyInput) error {
	if t.MappingSourceFileID == in.MappingSourceFileID {
		if t.WorkbookSourceFileID != nil && in.WorkbookSourceFileID != nil && *t.WorkbookSourceFileID != *in.WorkbookSourceFileID {
			return &ConflictError{Table: t.Name, Detail: "the table is already fed by another workbook"}
		}
		return nil
	}
	_, err := q.GetCurrentVersionOfSourceFile(ctx, t.MappingSourceFileID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil // the previous owner is gone (removed); the name is free
	case err != nil:
		return fmt.Errorf("check owner of %s: %w", t.Name, err)
	}
	return &ConflictError{Table: t.Name, Detail: "the table name is declared by another mapping file"}
}

func rowParams(tableID, wbVersion, mapVersion uuid.UUID, objectKey string, fr xlsx.FactRow) (queries.InsertFactRowsParams, error) {
	key, err := json.Marshal(fr.Key)
	if err != nil {
		return queries.InsertFactRowsParams{}, err
	}
	row, err := json.Marshal(fr.Values)
	if err != nil {
		return queries.InsertFactRowsParams{}, err
	}
	from, err := dateParam(fr.ValidFrom)
	if err != nil {
		return queries.InsertFactRowsParams{}, err
	}
	to, err := dateParam(fr.ValidTo)
	if err != nil {
		return queries.InsertFactRowsParams{}, err
	}
	return queries.InsertFactRowsParams{
		FactTableID: tableID, WorkbookVersionID: wbVersion, MappingVersionID: mapVersion,
		Key: key, Row: row, ValidFrom: from, ValidTo: to, SourceRef: objectKey + "#" + fr.SourceRef,
	}, nil
}

func dateParam(s *string) (pgtype.Date, error) {
	if s == nil {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse(time.DateOnly, *s)
	if err != nil {
		return pgtype.Date{}, err
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}
