// SPDX-License-Identifier: Apache-2.0

// Package facts serves and stores structured facts: typed rows imported from a
// workbook under a per-sheet mapping, looked up by exact key.
//
// The data model is two generic tables (fact_tables, fact_rows); there is no
// runtime DDL. A fact table is served only through its import pointer, the
// exact pair (workbook version, mapping version) whose rows are live. When
// either source is superseded, removed or fails to import, the pointer is
// cleared and lookups fail with ErrUnavailable; rows of an older import are
// never served.
//
// Key coercion. A lookup key must contain exactly the table's key columns.
// Each value is checked against the column type and turned into the canonical
// form stored at import (so jsonb equality is the match):
//
//	STRING   a JSON string, as is
//	INTEGER  a JSON integer ("42" or 42; no fraction or exponent) or a string
//	         of optional minus and digits ("42"); must fit in int64
//	DECIMAL  a JSON number or a string of optional minus, digits and an
//	         optional fraction ("9.90"); compared by value, so 9.90 equals 9.9
//	DATE     a string yyyy-mm-dd naming a real calendar date
//	BOOLEAN  true or false, or the strings "true" or "false"
//
// Anything else (null, an object, a number for a STRING column, 42.5 for an
// INTEGER column, "2024-1-5" for a DATE column) is a KeyError.
package facts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rasonyang/aicc-knowledge/internal/domain"
	"github.com/rasonyang/aicc-knowledge/internal/store"
	"github.com/rasonyang/aicc-knowledge/internal/store/queries"
	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// Coded failures.
const (
	// CodeNameConflict means a table name is already owned by another mapping
	// file or another workbook.
	CodeNameConflict = "FACT_TABLE_NAME_CONFLICT"
)

// Reasons stored in fact_tables.last_error_code (besides the parse error codes
// FACTS_INVALID and MAPPING_INVALID and the scan's SOURCE_SUPERSEDED and
// SOURCE_REMOVED).
const (
	ReasonWorkbookNotParsed = "WORKBOOK_NOT_PARSED"
	ReasonNotDeclared       = "TABLE_NOT_DECLARED"
)

// ErrTableNotFound means no fact table has the requested name.
var ErrTableNotFound = errors.New("fact table not found")

// UnavailableError means the table exists but has no live import.
type UnavailableError struct {
	Table string
	// Code is the reason recorded on the table (fact_tables.last_error_code).
	Code string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("fact table %q is unavailable (%s)", e.Table, e.Code)
}

// KeyInvalid is one key value that does not fit its column.
type KeyInvalid struct {
	Column   string `json:"column"`
	Type     string `json:"type"`
	Detail   string `json:"detail"`
	Provided any    `json:"-"`
}

// KeyError means the lookup key does not match the table's key columns.
type KeyError struct {
	// Missing are key columns the request lacks, Unknown are request keys
	// that are not key columns; both sorted.
	Missing []string
	Unknown []string
	Invalid []KeyInvalid
	// KeyColumns are the table's key columns, in mapping order.
	KeyColumns []string
}

func (e *KeyError) Error() string {
	var parts []string
	if len(e.Missing) > 0 {
		parts = append(parts, "missing key columns: "+strings.Join(e.Missing, ", "))
	}
	if len(e.Unknown) > 0 {
		parts = append(parts, "unknown key columns: "+strings.Join(e.Unknown, ", "))
	}
	for _, iv := range e.Invalid {
		parts = append(parts, fmt.Sprintf("key column %s (%s): %s", iv.Column, iv.Type, iv.Detail))
	}
	return strings.Join(parts, "; ")
}

// Match is the row a lookup found.
type Match struct {
	Row       map[string]any
	SourceRef string
	// ValidFrom and ValidTo are nil for an unbounded side.
	ValidFrom *time.Time
	ValidTo   *time.Time
}

// ErrAmbiguous means more than one row matched. Import validation (no
// overlapping validity per key) makes it impossible; the lookup refuses to
// guess if it ever happens.
var ErrAmbiguous = errors.New("more than one fact row matches the key")

// Lookup finds the row of table whose key equals key and whose validity
// period contains at (valid_from <= at < valid_to, either side may be open).
// It returns (nil, nil) when no row matches. Table existence, availability and
// the row query run in one repeatable-read snapshot, so a concurrent import
// can never produce a mixed answer.
func Lookup(ctx context.Context, st *store.Store, name string, key map[string]any, at time.Time) (*Match, error) {
	var match *Match
	err := pgx.BeginTxFunc(ctx, st.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := st.Queries.WithTx(tx)
		t, err := q.GetFactTableByName(ctx, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTableNotFound
		}
		if err != nil {
			return fmt.Errorf("load fact table: %w", err)
		}
		if domain.FactTableStatus(t.Status) != domain.FactTableAvailable {
			code := ""
			if t.LastErrorCode != nil {
				code = *t.LastErrorCode
			}
			return &UnavailableError{Table: name, Code: code}
		}
		var cols []xlsx.Column
		if err := json.Unmarshal(t.Columns, &cols); err != nil {
			return fmt.Errorf("decode columns of %s: %w", name, err)
		}
		canon, err := CanonicalKey(cols, t.KeyColumns, key)
		if err != nil {
			return err
		}
		rows, err := q.LookupFactRows(ctx, queries.LookupFactRowsParams{
			TableID: t.ID, Key: canon, At: pgtype.Date{Time: at, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("look up fact rows: %w", err)
		}
		switch len(rows) {
		case 0:
			return nil
		case 1:
		default:
			return ErrAmbiguous
		}
		dec := json.NewDecoder(bytes.NewReader(rows[0].Row))
		dec.UseNumber()
		m := &Match{SourceRef: rows[0].SourceRef}
		if err := dec.Decode(&m.Row); err != nil {
			return fmt.Errorf("decode fact row: %w", err)
		}
		if rows[0].ValidFrom.Valid {
			v := rows[0].ValidFrom.Time
			m.ValidFrom = &v
		}
		if rows[0].ValidTo.Valid {
			v := rows[0].ValidTo.Time
			m.ValidTo = &v
		}
		match = m
		return nil
	})
	return match, err
}

var (
	intRe     = regexp.MustCompile(`^-?[0-9]+$`)
	decimalRe = regexp.MustCompile(`^(-?)([0-9]+)(?:\.([0-9]+))?$`)
)

// CanonicalKey validates key against the key columns and returns the JSON
// object stored at import for the same values. It returns a *KeyError when the
// key does not fit.
func CanonicalKey(cols []xlsx.Column, keyColumns []string, key map[string]any) ([]byte, error) {
	kerr := &KeyError{KeyColumns: slices.Clone(keyColumns)}
	for _, k := range keyColumns {
		if _, ok := key[k]; !ok {
			kerr.Missing = append(kerr.Missing, k)
		}
	}
	for k := range key {
		if !slices.Contains(keyColumns, k) {
			kerr.Unknown = append(kerr.Unknown, k)
		}
	}
	slices.Sort(kerr.Missing)
	slices.Sort(kerr.Unknown)
	out := make(map[string]any, len(keyColumns))
	for _, k := range keyColumns {
		v, ok := key[k]
		if !ok {
			continue
		}
		i := slices.IndexFunc(cols, func(c xlsx.Column) bool { return c.Name == k })
		if i < 0 {
			return nil, fmt.Errorf("key column %q is not a column of the table", k)
		}
		cv, detail := coerce(cols[i].Type, v)
		if detail != "" {
			kerr.Invalid = append(kerr.Invalid, KeyInvalid{Column: k, Type: string(cols[i].Type), Detail: detail})
			continue
		}
		out[k] = cv
	}
	if len(kerr.Missing) > 0 || len(kerr.Unknown) > 0 || len(kerr.Invalid) > 0 {
		return nil, kerr
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode key: %w", err)
	}
	return b, nil
}

// coerce returns the canonical value or a non-empty problem description.
func coerce(t xlsx.ColumnType, v any) (any, string) {
	str, isStr := v.(string)
	num, isNum := numberText(v)
	switch t {
	case xlsx.TypeString:
		if isStr {
			return str, ""
		}
		return nil, "expected a JSON string"
	case xlsx.TypeInteger:
		text := str
		if isNum {
			text, isStr = num, true
		}
		if !isStr || !intRe.MatchString(text) {
			return nil, "expected an integer (a JSON integer or a string of digits)"
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, "integer is out of range"
		}
		return n, ""
	case xlsx.TypeDecimal:
		if isNum {
			return json.Number(num), "" // already valid JSON number syntax
		}
		if m := decimalRe.FindStringSubmatch(str); isStr && m != nil {
			whole := strings.TrimLeft(m[2], "0")
			if whole == "" {
				whole = "0"
			}
			s := m[1] + whole
			if m[3] != "" {
				s += "." + m[3]
			}
			return json.Number(s), ""
		}
		return nil, "expected a decimal number (a JSON number or a string like \"9.90\")"
	case xlsx.TypeDate:
		if isStr {
			if d, err := time.Parse(time.DateOnly, str); err == nil && d.Format(time.DateOnly) == str {
				return str, ""
			}
		}
		return nil, "expected a date as yyyy-mm-dd"
	case xlsx.TypeBoolean:
		switch x := v.(type) {
		case bool:
			return x, ""
		case string:
			if x == "true" || x == "false" {
				return x == "true", ""
			}
		}
		return nil, "expected true or false"
	}
	return nil, "unsupported column type " + string(t)
}

// numberText returns the literal of a JSON number however it was decoded.
func numberText(v any) (string, bool) {
	switch x := v.(type) {
	case json.Number:
		return x.String(), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	}
	return "", false
}
