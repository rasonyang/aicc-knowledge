// SPDX-License-Identifier: Apache-2.0

package xlsx

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// ColumnType is the value type of a mapped column.
type ColumnType string

// Column types.
const (
	TypeString  ColumnType = "STRING"
	TypeInteger ColumnType = "INTEGER"
	TypeDecimal ColumnType = "DECIMAL"
	TypeDate    ColumnType = "DATE"
	TypeBoolean ColumnType = "BOOLEAN"
)

// Column maps one sheet column to a fact attribute.
type Column struct {
	// Name is the snake_case attribute name in the stored row.
	Name string `yaml:"name" json:"name"`
	// Header is the header text to match. With several header rows it is the
	// path of the header rows joined by " / ", e.g. "Price / Monthly".
	Header   string     `yaml:"header" json:"header"`
	Type     ColumnType `yaml:"type" json:"type"`
	Unit     string     `yaml:"unit,omitempty" json:"unit,omitempty"`
	Required bool       `yaml:"required,omitempty" json:"required,omitempty"`
}

// Mapping describes how one sheet becomes one fact table. Unknown YAML fields
// are rejected.
type Mapping struct {
	Table string `yaml:"table" json:"table"`
	Sheet string `yaml:"sheet" json:"sheet"`
	// HeaderRows is the number of header rows (>= 1).
	HeaderRows int `yaml:"headerRows" json:"headerRows"`
	// HeaderStartRow is the first header row (optional, default 1).
	HeaderStartRow int `yaml:"headerStartRow,omitempty" json:"headerStartRow,omitempty"`
	// DataStartRow is the first data row (optional, default: the row after
	// the header rows).
	DataStartRow int      `yaml:"dataStartRow,omitempty" json:"dataStartRow,omitempty"`
	Columns      []Column `yaml:"columns" json:"columns"`
	// KeyColumns are column names forming the lookup key. Key columns are
	// implicitly required.
	KeyColumns []string `yaml:"keyColumns" json:"keyColumns"`
	// ValidFrom / ValidTo name DATE columns bounding a row's validity. The
	// period is half-open [validFrom, validTo); an empty validFrom is
	// unbounded in the past and an empty validTo is open-ended.
	ValidFrom string `yaml:"validFrom,omitempty" json:"validFrom,omitempty"`
	ValidTo   string `yaml:"validTo,omitempty" json:"validTo,omitempty"`
}

// MappingError is returned (as the *Error's Err) with code MAPPING_INVALID and
// lists every problem found.
type MappingError struct{ Issues []string }

func (e *MappingError) Error() string { return strings.Join(e.Issues, "; ") }

var snakeRe = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

const maxNameLen = 63

// ParseMapping parses and validates one YAML mapping document, rejecting
// unknown fields.
func ParseMapping(data []byte) (*Mapping, error) {
	var m Mapping
	dec := yaml.NewDecoder(bytes.NewReader(data), yaml.Strict(), yaml.DisallowUnknownField())
	if err := dec.Decode(&m); err != nil {
		return nil, &Error{Code: CodeMappingInvalid, Message: "mapping YAML cannot be decoded", Err: err}
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks the mapping and returns an *Error (MAPPING_INVALID) whose
// Err is a *MappingError listing every issue.
func (m *Mapping) Validate() error {
	var iss []string
	add := func(f string, a ...any) { iss = append(iss, fmt.Sprintf(f, a...)) }
	if !validName(m.Table) {
		add("table %q must be snake_case (max %d chars)", m.Table, maxNameLen)
	}
	if strings.TrimSpace(m.Sheet) == "" {
		add("sheet is required")
	}
	if m.HeaderRows < 1 {
		add("headerRows must be >= 1, got %d", m.HeaderRows)
	}
	if m.HeaderStartRow < 0 {
		add("headerStartRow must be >= 1 when set, got %d", m.HeaderStartRow)
	}
	hs := m.headerStart()
	if m.DataStartRow != 0 && m.DataStartRow < hs+m.HeaderRows {
		add("dataStartRow %d must be >= %d (first row after the header rows)", m.DataStartRow, hs+m.HeaderRows)
	}
	if len(m.Columns) == 0 {
		add("columns must not be empty")
	}
	byName := map[string]*Column{}
	headers := map[string]string{}
	for i := range m.Columns {
		c := &m.Columns[i]
		if !validName(c.Name) {
			add("columns[%d].name %q must be snake_case (max %d chars)", i, c.Name, maxNameLen)
		} else if byName[c.Name] != nil {
			add("columns[%d].name %q is duplicated", i, c.Name)
		} else {
			byName[c.Name] = c
		}
		h := normSpace(c.Header)
		if h == "" {
			add("columns[%d].header is required", i)
		} else if prev, ok := headers[h]; ok {
			add("columns[%d].header %q duplicates column %q", i, c.Header, prev)
		} else {
			headers[h] = c.Name
		}
		switch c.Type {
		case TypeString, TypeInteger, TypeDecimal, TypeDate, TypeBoolean:
		default:
			add("columns[%d].type %q must be one of STRING, INTEGER, DECIMAL, DATE, BOOLEAN", i, c.Type)
		}
	}
	if len(m.KeyColumns) == 0 {
		add("keyColumns must not be empty")
	}
	seen := map[string]bool{}
	for _, k := range m.KeyColumns {
		if byName[k] == nil {
			add("keyColumns entry %q is not a column", k)
		}
		if seen[k] {
			add("keyColumns entry %q is duplicated", k)
		}
		seen[k] = true
	}
	for _, v := range []struct{ field, name string }{{"validFrom", m.ValidFrom}, {"validTo", m.ValidTo}} {
		if v.name == "" {
			continue
		}
		c := byName[v.name]
		switch {
		case c == nil:
			add("%s %q is not a column", v.field, v.name)
		case c.Type != TypeDate:
			add("%s column %q must have type DATE", v.field, v.name)
		}
	}
	if m.ValidFrom != "" && m.ValidFrom == m.ValidTo {
		add("validFrom and validTo must be different columns")
	}
	if len(iss) == 0 {
		return nil
	}
	return &Error{Code: CodeMappingInvalid, Message: "mapping is invalid", Err: &MappingError{Issues: iss}}
}

func (m *Mapping) headerStart() int {
	if m.HeaderStartRow > 0 {
		return m.HeaderStartRow
	}
	return 1
}

func validName(s string) bool { return len(s) <= maxNameLen && snakeRe.MatchString(s) }

// MappingFile is the content of a `<name>.facts.yaml` file: one mapping per
// sheet that becomes a fact table. Unknown YAML fields are rejected.
type MappingFile struct {
	Tables []Mapping `yaml:"tables" json:"tables"`
}

// ParseMappingFile parses and validates a mapping file. The file must declare
// at least one table, every table must be valid and table names must be
// unique within the file. Every problem found is listed in the returned
// *Error's *MappingError (code MAPPING_INVALID).
func ParseMappingFile(data []byte) (*MappingFile, error) {
	var mf MappingFile
	dec := yaml.NewDecoder(bytes.NewReader(data), yaml.Strict(), yaml.DisallowUnknownField())
	if err := dec.Decode(&mf); err != nil {
		return nil, &Error{Code: CodeMappingInvalid, Message: "mapping file YAML cannot be decoded", Err: err}
	}
	var iss []string
	if len(mf.Tables) == 0 {
		iss = append(iss, "tables must not be empty")
	}
	names := map[string]bool{}
	for i := range mf.Tables {
		t := &mf.Tables[i]
		if err := t.Validate(); err != nil {
			var me *MappingError
			if e, ok := err.(*Error); ok && errors.As(e.Err, &me) {
				for _, s := range me.Issues {
					iss = append(iss, fmt.Sprintf("tables[%d]: %s", i, s))
				}
			} else {
				iss = append(iss, fmt.Sprintf("tables[%d]: %v", i, err))
			}
		}
		if names[t.Table] {
			iss = append(iss, fmt.Sprintf("tables[%d]: table %q is declared twice", i, t.Table))
		}
		names[t.Table] = true
	}
	if len(iss) > 0 {
		return nil, &Error{Code: CodeMappingInvalid, Message: "mapping file is invalid", Err: &MappingError{Issues: iss}}
	}
	return &mf, nil
}

// Sheets returns the sheet names the file's tables read, in declaration
// order without repeats.
func (f *MappingFile) Sheets() []string {
	var out []string
	for _, t := range f.Tables {
		if !slices.Contains(out, t.Sheet) {
			out = append(out, t.Sheet)
		}
	}
	return out
}
