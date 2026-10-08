// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"errors"
	"slices"
	"testing"
)

// checkMachine asserts, for every ordered pair of states, that CanTransition
// and Transition agree with the allowed set, and that the set of allowed edges
// has exactly the expected cardinality.
func checkMachine[S ~string](t *testing.T, states []S, allowed map[S][]S,
	can func(S, S) bool, tr func(S, S) (S, error)) {
	t.Helper()
	wantEdges, gotEdges := 0, 0
	for _, tos := range allowed {
		wantEdges += len(tos)
	}
	for _, from := range states {
		for _, to := range states {
			want := slices.Contains(allowed[from], to)
			if got := can(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
			next, err := tr(from, to)
			if want {
				gotEdges++
				if err != nil || next != to {
					t.Errorf("Transition(%s, %s) = %s, %v; want success", from, to, next, err)
				}
				continue
			}
			var te *TransitionError
			if !errors.As(err, &te) || te.Code != CodeIllegalTransition {
				t.Errorf("Transition(%s, %s) error = %v, want %s", from, to, err, CodeIllegalTransition)
			}
			if !IsIllegalTransition(err) {
				t.Errorf("IsIllegalTransition(%s -> %s) = false", from, to)
			}
			if next != from {
				t.Errorf("Transition(%s, %s) returned %s, want the old state back", from, to, next)
			}
		}
	}
	if gotEdges != wantEdges {
		t.Errorf("allowed edges = %d, want %d", gotEdges, wantEdges)
	}
	var zero S
	if _, err := tr(zero, states[0]); !errors.Is(err, ErrUnknownState) {
		t.Errorf("unknown from-state error = %v, want UNKNOWN_STATE", err)
	}
	if _, err := tr(states[0], S("NOPE")); !errors.Is(err, ErrUnknownState) {
		t.Errorf("unknown to-state error = %v, want UNKNOWN_STATE", err)
	}
}

func TestFileVersionStateMachine(t *testing.T) {
	checkMachine(t, FileVersionStates(), map[FileVersionState][]FileVersionState{
		FileVersionDiscovered:  {FileVersionParsed, FileVersionParseFailed, FileVersionUnsupported, FileVersionRemoved},
		FileVersionParsed:      {FileVersionRemoved},
		FileVersionParseFailed: {FileVersionDiscovered, FileVersionRemoved},
		FileVersionUnsupported: {FileVersionRemoved},
	}, FileVersionState.CanTransition, FileVersionState.Transition)
	if n := len(FileVersionStates()); n != 5 {
		t.Errorf("file version states = %d, want 5", n)
	}
}

func TestParseFailedIsRetryableOnly(t *testing.T) {
	if !FileVersionParseFailed.CanTransition(FileVersionDiscovered) {
		t.Fatal("retry edge PARSE_FAILED -> DISCOVERED missing")
	}
	for _, s := range []FileVersionState{FileVersionParsed, FileVersionUnsupported, FileVersionRemoved} {
		if s.CanTransition(FileVersionDiscovered) {
			t.Errorf("%s -> DISCOVERED must not exist", s)
		}
	}
}

func TestCandidateStateMachine(t *testing.T) {
	checkMachine(t, CandidateStates(), map[CandidateState][]CandidateState{
		CandidatePendingReview: {CandidateApproved, CandidateRejected, CandidateStale},
		CandidateApproved:      {CandidateStale},
		CandidateRejected:      {CandidateStale},
	}, CandidateState.CanTransition, CandidateState.Transition)
	if n := len(CandidateStates()); n != 4 {
		t.Errorf("candidate states = %d, want 4", n)
	}
}

func TestPublicationStateMachine(t *testing.T) {
	checkMachine(t, PublicationStates(), map[PublicationState][]PublicationState{
		PublicationBuilding:   {PublicationLive, PublicationFailed},
		PublicationLive:       {PublicationSuperseded},
		PublicationSuperseded: {PublicationLive},
	}, PublicationState.CanTransition, PublicationState.Transition)
	if n := len(PublicationStates()); n != 4 {
		t.Errorf("publication states = %d, want 4", n)
	}
}

func TestPublicationRollbackEdgeIsExplicit(t *testing.T) {
	if !PublicationSuperseded.CanTransition(PublicationLive) {
		t.Fatal("rollback edge SUPERSEDED -> LIVE missing")
	}
	if PublicationFailed.CanTransition(PublicationLive) {
		t.Fatal("a FAILED publication must never become LIVE")
	}
	if PublicationBuilding.CanTransition(PublicationSuperseded) {
		t.Fatal("a BUILDING publication cannot be superseded before it is LIVE")
	}
}

func TestJobStateMachine(t *testing.T) {
	checkMachine(t, JobStates(), map[JobState][]JobState{
		JobQueued:  {JobRunning},
		JobRunning: {JobSucceeded, JobFailed, JobQueued},
	}, JobState.CanTransition, JobState.Transition)
}

func TestTerminalStatesHaveNoExit(t *testing.T) {
	for _, s := range []FileVersionState{FileVersionRemoved} {
		for _, to := range FileVersionStates() {
			if s.CanTransition(to) {
				t.Errorf("%s -> %s allowed from a terminal state", s, to)
			}
		}
	}
	for _, to := range CandidateStates() {
		if CandidateStale.CanTransition(to) {
			t.Errorf("STALE -> %s allowed", to)
		}
	}
}

func TestEnumValuesAreScreamingSnake(t *testing.T) {
	var all []string
	for _, s := range FileVersionStates() {
		all = append(all, string(s))
	}
	for _, s := range CandidateStates() {
		all = append(all, string(s))
	}
	for _, s := range PublicationStates() {
		all = append(all, string(s))
	}
	for _, s := range JobStates() {
		all = append(all, string(s))
	}
	for _, l := range Languages() {
		all = append(all, string(l))
	}
	for _, v := range all {
		for _, r := range v {
			if (r < 'A' || r > 'Z') && r != '_' {
				t.Errorf("%q is not SCREAMING_SNAKE", v)
				break
			}
		}
	}
}

func TestLanguageIndexUID(t *testing.T) {
	if LanguageEN.LiveIndexUID() != "faq_en" || LanguageZH.LiveIndexUID() != "faq_zh" {
		t.Fatalf("index uids = %s %s", LanguageEN.LiveIndexUID(), LanguageZH.LiveIndexUID())
	}
	if Language("FR").Valid() || !LanguageEN.Valid() {
		t.Fatal("Language.Valid wrong")
	}
}
