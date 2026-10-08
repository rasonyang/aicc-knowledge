// SPDX-License-Identifier: Apache-2.0

package domain

// SectionKind says which parser produced a parsed section.
type SectionKind string

// Section kinds.
const (
	SectionKindDocxSection SectionKind = "DOCX_SECTION"
	SectionKindXlsxChunk   SectionKind = "XLSX_CHUNK"
	// SectionKindXlsxQARow is one row of a Q&A sheet imported through a
	// `.qa.yaml` mapping: question, alternates and answer are kept apart.
	SectionKindXlsxQARow SectionKind = "XLSX_QA_ROW"
)

// SectionKinds returns the enum's value set.
func SectionKinds() []SectionKind {
	return []SectionKind{SectionKindDocxSection, SectionKindXlsxChunk, SectionKindXlsxQARow}
}

// FactTableStatus says whether a fact table can be served.
//
// AVAILABLE means the table's current import pointer names the current
// workbook version and the current mapping version and their rows are in
// fact_rows. UNAVAILABLE means there is no such pair right now (a source was
// superseded, removed or failed to parse); lookups answer 503 and never fall
// back to older rows.
type FactTableStatus string

// Fact table statuses.
const (
	FactTableAvailable   FactTableStatus = "AVAILABLE"
	FactTableUnavailable FactTableStatus = "UNAVAILABLE"
)

// FactTableStatuses returns the enum's value set.
func FactTableStatuses() []FactTableStatus {
	return []FactTableStatus{FactTableAvailable, FactTableUnavailable}
}
