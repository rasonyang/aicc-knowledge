// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/xuri/excelize/v2"

	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

func init() { register("facts", writeFacts) }

// factsFrom is the first day every plan row is valid.
const factsFrom = "2025-01-01"

// factTable is one workbook (one sheet) and the mapping that imports it.
type factTable struct {
	name    string
	sheet   string
	cols    []xlsx.Column
	headers []string
	rows    [][]any // nil cell = empty; string = text cell; int = number cell
	keys    []string
	from    string
	to      string
}

func col(name, header string, t xlsx.ColumnType, unit string, required bool) xlsx.Column {
	return xlsx.Column{Name: name, Header: header, Type: t, Unit: unit, Required: required}
}

func writeFacts(e *env) error {
	for _, t := range factTables(e) {
		if err := writeFactTable(e.out, t); err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
	}
	return nil
}

func factTables(e *env) []factTable {
	return []factTable{plansTable(e), topupsTable(), roamingTable()}
}

func opt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func plansTable(e *env) factTable {
	today := e.today.Format(time.DateOnly)
	t := factTable{
		name: "plans", sheet: "Plans", keys: []string{"plan"}, from: "valid_from", to: "valid_to",
		cols: []xlsx.Column{
			col("plan", "Plan", xlsx.TypeString, "", false),
			col("name_en", "Name (EN)", xlsx.TypeString, "", false),
			col("name_zh", "Name (ZH)", xlsx.TypeString, "", false),
			col("kind", "Kind", xlsx.TypeString, "", false),
			col("monthly_fee", "Monthly fee", xlsx.TypeDecimal, "CNY", true),
			col("data_gb", "Data (GB)", xlsx.TypeInteger, "GB", false),
			col("voice_minutes", "Voice (min)", xlsx.TypeInteger, "min", false),
			col("speed_mbps", "Speed (Mbps)", xlsx.TypeInteger, "Mbps", false),
			col("contract_months", "Contract (months)", xlsx.TypeInteger, "months", false),
			col("early_termination_fee", "Early termination fee", xlsx.TypeDecimal, "CNY", false),
			col("valid_from", "Valid from", xlsx.TypeDate, "", false),
			col("valid_to", "Valid to", xlsx.TypeDate, "", false),
		},
	}
	for _, p := range plans {
		row := func(fee, from string, to any) []any {
			return []any{p.Key, p.NameEN, p.NameZH, p.Kind, fee, opt(p.DataGB), opt(p.VoiceMinutes), opt(p.SpeedMbps), p.ContractMonths, p.EarlyTermFee, from, to}
		}
		if p.OldMonthlyFee != "" {
			t.rows = append(t.rows, row(p.OldMonthlyFee, factsFrom, today), row(p.MonthlyFee, today, nil))
		} else {
			t.rows = append(t.rows, row(p.MonthlyFee, factsFrom, nil))
		}
	}
	return t
}

// topupsTable has a DECIMAL key written as text in mixed forms; the importer
// canonicalizes it, so "50.00" is stored and looked up as 50.
func topupsTable() factTable {
	return factTable{
		name: "topups", sheet: "Topups", keys: []string{"amount"},
		cols: []xlsx.Column{
			col("amount", "Amount", xlsx.TypeDecimal, "CNY", false),
			col("bonus_credit", "Bonus credit", xlsx.TypeDecimal, "CNY", false),
			col("bonus_data_gb", "Bonus data (GB)", xlsx.TypeInteger, "GB", false),
		},
		rows: [][]any{
			{"30", "0.00", nil},
			{"50.00", "2.00", 1},
			{"100.0", "5.00", 3},
		},
	}
}

// roamingTable is valid except for one empty key cell, so its whole import
// fails and lookups answer FACT_TABLE_UNAVAILABLE.
func roamingTable() factTable {
	return factTable{
		name: "roaming_packs", sheet: "Roaming", keys: []string{"pack"},
		cols: []xlsx.Column{
			col("pack", "Pack", xlsx.TypeString, "", false),
			col("region", "Region", xlsx.TypeString, "", false),
			col("price", "Price", xlsx.TypeDecimal, "CNY", true),
			col("days", "Days", xlsx.TypeInteger, "days", false),
		},
		rows: [][]any{
			{"Asia 3 Day", "Asia", "68.00", 3},
			{"Asia 7 Day", "Asia", "108.00", 7},
			{nil, "Europe", "128.00", 7},
			{"Global 15 Day", "Worldwide", "288.00", 15},
		},
	}
}

func writeFactTable(out string, t factTable) error {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", t.sheet); err != nil {
		return err
	}
	fixed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := f.SetDocProps(&excelize.DocProperties{Creator: "NovaNet", LastModifiedBy: "NovaNet", Created: fixed.Format(time.RFC3339), Modified: fixed.Format(time.RFC3339)}); err != nil {
		return err
	}
	for i, c := range t.cols {
		ref, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellStr(t.sheet, ref, c.Header); err != nil {
			return err
		}
	}
	for r, row := range t.rows {
		for c, v := range row {
			ref, _ := excelize.CoordinatesToCellName(c+1, r+2)
			var err error
			switch x := v.(type) {
			case nil:
			case string:
				err = f.SetCellStr(t.sheet, ref, x)
			case int:
				err = f.SetCellInt(t.sheet, ref, int64(x))
			}
			if err != nil {
				return err
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return err
	}
	m := xlsx.MappingFile{Tables: []xlsx.Mapping{{
		Table: t.name, Sheet: t.sheet, HeaderRows: 1, Columns: t.cols,
		KeyColumns: t.keys, ValidFrom: t.from, ValidTo: t.to,
	}}}
	y, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	dir := filepath.Join(out, "facts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, t.name+".xlsx"), buf.Bytes(), 0o644); err != nil {
		return err
	}
	hdr := "# SPDX-License-Identifier: Apache-2.0\n"
	return os.WriteFile(filepath.Join(dir, t.name+".facts.yaml"), append([]byte(hdr), y...), 0o644)
}
