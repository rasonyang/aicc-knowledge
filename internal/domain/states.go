// SPDX-License-Identifier: Apache-2.0

package domain

import "strings"

// Language is the content language of a Q&A and of the index that serves it.
type Language string

// Languages. The value is the wire and database code; IndexBase maps it to
// the Meilisearch index family.
const (
	LanguageEN Language = "EN"
	LanguageZH Language = "ZH"
)

// Languages returns every language.
func Languages() []Language { return []Language{LanguageEN, LanguageZH} }

// Valid reports whether l is a known language.
func (l Language) Valid() bool { return l == LanguageEN || l == LanguageZH }

// LiveIndexUID is the Meilisearch uid searches use for this language
// (faq_en, faq_zh). Publication swaps a freshly built index into this uid.
func (l Language) LiveIndexUID() string { return "faq_" + strings.ToLower(string(l)) }

// FileVersionState is the state of one immutable version of a source file.
//
//	DISCOVERED -> PARSED | PARSE_FAILED | UNSUPPORTED
//	DISCOVERED | PARSED | PARSE_FAILED | UNSUPPORTED -> REMOVED (source deleted)
//	PARSE_FAILED -> DISCOVERED   (explicit operator retry only)
//
// PARSE_FAILED is retryable: the version's content is unchanged and stays
// immutable; only its processing state is reset, for example after a parser
// fix. Nothing retries automatically; an operator triggers it (flag
// `parse --retry-failed`). PARSED and UNSUPPORTED have no retry edge. REMOVED
// is terminal. A content change creates a new version row; it never mutates
// the old one.
type FileVersionState string

// File version states.
const (
	FileVersionDiscovered  FileVersionState = "DISCOVERED"
	FileVersionParsed      FileVersionState = "PARSED"
	FileVersionParseFailed FileVersionState = "PARSE_FAILED"
	FileVersionUnsupported FileVersionState = "UNSUPPORTED"
	FileVersionRemoved     FileVersionState = "REMOVED"
)

var fileVersionMachine = machine[FileVersionState]{
	name: "file_version",
	states: []FileVersionState{
		FileVersionDiscovered, FileVersionParsed, FileVersionParseFailed,
		FileVersionUnsupported, FileVersionRemoved,
	},
	edges: map[FileVersionState][]FileVersionState{
		FileVersionDiscovered:  {FileVersionParsed, FileVersionParseFailed, FileVersionUnsupported, FileVersionRemoved},
		FileVersionParsed:      {FileVersionRemoved},
		FileVersionParseFailed: {FileVersionDiscovered, FileVersionRemoved},
		FileVersionUnsupported: {FileVersionRemoved},
	},
}

// FileVersionStates returns the enum's value set.
func FileVersionStates() []FileVersionState { return fileVersionMachine.values() }

// CanTransition reports whether the edge from -> to exists.
func (s FileVersionState) CanTransition(to FileVersionState) bool {
	return fileVersionMachine.can(s, to)
}

// Transition returns to, or a coded *TransitionError.
func (s FileVersionState) Transition(to FileVersionState) (FileVersionState, error) {
	return fileVersionMachine.transition(s, to)
}

// CandidateState is the review state of a generated Q&A candidate.
//
//	PENDING_REVIEW -> APPROVED | REJECTED
//	PENDING_REVIEW | APPROVED | REJECTED -> STALE (its source version was superseded)
//
// STALE is terminal. Only APPROVED candidates are ever published.
type CandidateState string

// Candidate states.
const (
	CandidatePendingReview CandidateState = "PENDING_REVIEW"
	CandidateApproved      CandidateState = "APPROVED"
	CandidateRejected      CandidateState = "REJECTED"
	CandidateStale         CandidateState = "STALE"
)

var candidateMachine = machine[CandidateState]{
	name: "candidate",
	states: []CandidateState{
		CandidatePendingReview, CandidateApproved, CandidateRejected, CandidateStale,
	},
	edges: map[CandidateState][]CandidateState{
		CandidatePendingReview: {CandidateApproved, CandidateRejected, CandidateStale},
		CandidateApproved:      {CandidateStale},
		CandidateRejected:      {CandidateStale},
	},
}

// CandidateStates returns the enum's value set.
func CandidateStates() []CandidateState { return candidateMachine.values() }

// CanTransition reports whether the edge from -> to exists.
func (s CandidateState) CanTransition(to CandidateState) bool { return candidateMachine.can(s, to) }

// Transition returns to, or a coded *TransitionError.
func (s CandidateState) Transition(to CandidateState) (CandidateState, error) {
	return candidateMachine.transition(s, to)
}

// PublicationState is the state of one versioned build of a language's index.
//
//	BUILDING -> LIVE | FAILED
//	LIVE -> SUPERSEDED
//	SUPERSEDED -> LIVE   (rollback only)
//
// Rollback is a pair of edges applied in one transaction: the chosen
// SUPERSEDED publication becomes LIVE (SUPERSEDED -> LIVE) while the current
// LIVE one becomes SUPERSEDED (LIVE -> SUPERSEDED). The partial unique index
// uq_publications_live_language keeps at most one LIVE row per language, so the
// store must demote before it promotes inside that transaction. FAILED is
// terminal.
type PublicationState string

// Publication states.
const (
	PublicationBuilding   PublicationState = "BUILDING"
	PublicationLive       PublicationState = "LIVE"
	PublicationSuperseded PublicationState = "SUPERSEDED"
	PublicationFailed     PublicationState = "FAILED"
)

var publicationMachine = machine[PublicationState]{
	name: "publication",
	states: []PublicationState{
		PublicationBuilding, PublicationLive, PublicationSuperseded, PublicationFailed,
	},
	edges: map[PublicationState][]PublicationState{
		PublicationBuilding:   {PublicationLive, PublicationFailed},
		PublicationLive:       {PublicationSuperseded},
		PublicationSuperseded: {PublicationLive},
	},
}

// PublicationStates returns the enum's value set.
func PublicationStates() []PublicationState { return publicationMachine.values() }

// CanTransition reports whether the edge from -> to exists.
func (s PublicationState) CanTransition(to PublicationState) bool {
	return publicationMachine.can(s, to)
}

// Transition returns to, or a coded *TransitionError.
func (s PublicationState) Transition(to PublicationState) (PublicationState, error) {
	return publicationMachine.transition(s, to)
}

// JobState is the state of a row in the jobs queue.
//
//	QUEUED -> RUNNING
//	RUNNING -> SUCCEEDED | FAILED | QUEUED (retry after an error or an expired lease)
//
// SUCCEEDED and FAILED are terminal.
type JobState string

// Job states.
const (
	JobQueued    JobState = "QUEUED"
	JobRunning   JobState = "RUNNING"
	JobSucceeded JobState = "SUCCEEDED"
	JobFailed    JobState = "FAILED"
)

var jobMachine = machine[JobState]{
	name:   "job",
	states: []JobState{JobQueued, JobRunning, JobSucceeded, JobFailed},
	edges: map[JobState][]JobState{
		JobQueued:  {JobRunning},
		JobRunning: {JobSucceeded, JobFailed, JobQueued},
	},
}

// JobStates returns the enum's value set.
func JobStates() []JobState { return jobMachine.values() }

// CanTransition reports whether the edge from -> to exists.
func (s JobState) CanTransition(to JobState) bool { return jobMachine.can(s, to) }

// Transition returns to, or a coded *TransitionError.
func (s JobState) Transition(to JobState) (JobState, error) { return jobMachine.transition(s, to) }
