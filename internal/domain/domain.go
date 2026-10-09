// SPDX-License-Identifier: Apache-2.0

// Package domain holds the enums and state machines of the service. Each
// machine is a typed string with SCREAMING_SNAKE values that are byte-identical
// in JSON, Go and the database CHECK constraints; a gate test in
// internal/store keeps the Go and database sets equal.
package domain

import (
	"errors"
	"fmt"
	"slices"
)

// ErrorCode is a machine-readable domain failure identifier.
type ErrorCode string

const (
	// CodeIllegalTransition means the state machine has no edge from -> to.
	CodeIllegalTransition ErrorCode = "ILLEGAL_TRANSITION"
	// CodeUnknownState means a state value is not a member of the enum.
	CodeUnknownState ErrorCode = "UNKNOWN_STATE"
)

// TransitionError is returned by every Transition function.
type TransitionError struct {
	Code    ErrorCode
	Machine string
	From    string
	To      string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s: %s %s -> %s", e.Code, e.Machine, e.From, e.To)
}

// Is lets errors.Is match on the code alone.
func (e *TransitionError) Is(target error) bool {
	t, ok := target.(*TransitionError)
	return ok && t.Code == e.Code && t.Machine == "" && t.From == "" && t.To == ""
}

// ErrIllegalTransition matches any TransitionError with CodeIllegalTransition.
var ErrIllegalTransition = &TransitionError{Code: CodeIllegalTransition}

// ErrUnknownState matches any TransitionError with CodeUnknownState.
var ErrUnknownState = &TransitionError{Code: CodeUnknownState}

// IsIllegalTransition reports whether err is an illegal-transition error.
func IsIllegalTransition(err error) bool { return errors.Is(err, ErrIllegalTransition) }

// machine is a transition table plus its closed state set.
type machine[S ~string] struct {
	name   string
	states []S
	edges  map[S][]S
}

func (m machine[S]) can(from, to S) bool {
	return slices.Contains(m.edges[from], to)
}

func (m machine[S]) transition(from, to S) (S, error) {
	if !slices.Contains(m.states, from) || !slices.Contains(m.states, to) {
		return from, &TransitionError{Code: CodeUnknownState, Machine: m.name, From: string(from), To: string(to)}
	}
	if !m.can(from, to) {
		return from, &TransitionError{Code: CodeIllegalTransition, Machine: m.name, From: string(from), To: string(to)}
	}
	return to, nil
}

func (m machine[S]) values() []S { return slices.Clone(m.states) }
