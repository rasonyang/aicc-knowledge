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

// GenerateOutcome says what `generate` made of a parsed section it sent to the
// LLM. A section it did not send (stub, Q&A row) has none.
//
// GENERATED: at least one candidate was produced. EMPTY: the model answered
// with an empty list; there is nothing to retry. FAILED: no usable output
// (the output never conformed or the LLM did not answer); the section is
// retried by `generate --retry-failed`.
type GenerateOutcome string

// Generate outcomes.
const (
	GenerateGenerated GenerateOutcome = "GENERATED"
	GenerateEmpty     GenerateOutcome = "EMPTY"
	GenerateFailed    GenerateOutcome = "FAILED"
)

// GenerateOutcomes returns the enum's value set.
func GenerateOutcomes() []GenerateOutcome {
	return []GenerateOutcome{GenerateGenerated, GenerateEmpty, GenerateFailed}
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
