// SPDX-License-Identifier: Apache-2.0

package domain

// CandidateFlag marks a candidate for the reviewer's attention. The set is
// the CHECK on candidates.flags; a gate test keeps the two equal.
type CandidateFlag string

// Candidate flags.
const (
	// FlagContainsFigures: the question or answer states a number (a price, a
	// duration, a percentage) the reviewer must check against the source.
	FlagContainsFigures CandidateFlag = "CONTAINS_FIGURES"
	// FlagNeedsShortening: a curated Q&A answer that fails the answer limits
	// and could not be condensed automatically. It carries the original answer
	// and cannot be approved as it is; the reviewer must EDIT it down.
	FlagNeedsShortening CandidateFlag = "NEEDS_SHORTENING"
)

// CandidateFlags returns the enum's value set.
func CandidateFlags() []CandidateFlag {
	return []CandidateFlag{FlagContainsFigures, FlagNeedsShortening}
}

// ReviewAction is what a reviewer decided about a candidate. EDIT means
// "approve with the edits I made": the candidate ends APPROVED with the new
// text.
type ReviewAction string

// Review actions.
const (
	ReviewApprove ReviewAction = "APPROVE"
	ReviewReject  ReviewAction = "REJECT"
	ReviewEdit    ReviewAction = "EDIT"
)

// ReviewActions returns the enum's value set.
func ReviewActions() []ReviewAction { return []ReviewAction{ReviewApprove, ReviewReject, ReviewEdit} }
