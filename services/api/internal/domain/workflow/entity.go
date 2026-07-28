// Package workflowdom defines the automation-workflow aggregate and its
// domain contracts.
//
// A Workflow is a project-scoped, draft/active/archived dependency graph
// over existing tasks. Each WorkflowNode wraps one task; WorkflowEdge is a
// plain dependency link between two nodes with no per-edge configuration.
// A workflow also carries a shared, workflow-level (not per-node) lookup
// table:
//
//   - StatusTransition — the "status workflow": maps a status to the status
//     that should come next once work at that status is done, so an
//     AI-agent assignee can be told exactly what to set next instead of
//     guessing. The workflow's single "done" status is DERIVED from this
//     chain — whichever status has no next status configured (see
//     DeriveDoneStatusID) — rather than being its own field.
//
// Reassignment itself (status->assignee "rules") is NOT a workflow concept:
// it lives in the separate, project-wide statusruledom package, which
// applies to every task in the project regardless of workflow membership.
// A workflow only triggers a re-evaluation of that engine once a node's
// task reaches the workflow's derived done status (and, for nodes with
// multiple incoming edges, once ALL predecessors have reached theirs) — see
// worker.WorkflowConsumer's predecessor-done cascade — because the
// downstream task's own status may not itself be changing, so nothing else
// would re-trigger the rule engine for it.
package workflowdom

import (
	"time"

	"github.com/google/uuid"
)

// Status is the lifecycle state of a Workflow.
type Status string

const (
	// StatusDraft is the initial state: the graph can be freely edited and
	// the automation engine ignores it.
	StatusDraft Status = "draft"
	// StatusActive means the automation engine evaluates this workflow on
	// every relevant task status change. The graph can still be edited while
	// active; the engine reads it fresh per event with graceful fallbacks,
	// so an edit takes effect on the next event rather than requiring a
	// draft/reactivate round-trip.
	StatusActive Status = "active"
	// StatusArchived is a terminal-ish state: the engine ignores it, editing
	// is disallowed, and it can be reverted to draft to resume editing.
	StatusArchived Status = "archived"
)

// ValidStatuses is the set of allowed workflow status values.
var ValidStatuses = map[Status]bool{
	StatusDraft:    true,
	StatusActive:   true,
	StatusArchived: true,
}

// Workflow is the aggregate root: a named automation graph within a project.
type Workflow struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	Name        string
	Description string
	Status      Status
	CreatedBy   *uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// Node wraps a single task within a workflow.
type Node struct {
	ID         uuid.UUID
	WorkflowID uuid.UUID
	TaskID     uuid.UUID
	PosX       float64
	PosY       float64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// StatusTransition declares, for one status in the workflow, which status
// should come next once a task reaches it — the "status workflow." A nil
// NextStatusID marks StatusID as terminal (the workflow's done status).
type StatusTransition struct {
	ID           uuid.UUID
	WorkflowID   uuid.UUID
	StatusID     uuid.UUID
	NextStatusID *uuid.UUID
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// DeriveDoneStatusID returns the status_id of the single StatusTransition
// entry whose NextStatusID is nil (the workflow's terminal/done status). ok
// is false if there is zero or more than one such entry — callers must
// treat that as "undetermined" (not activatable, and no agent hint).
func DeriveDoneStatusID(transitions []*StatusTransition) (id uuid.UUID, ok bool) {
	found := uuid.Nil
	count := 0
	for _, t := range transitions {
		if t.NextStatusID == nil {
			found = t.StatusID
			count++
		}
	}
	if count != 1 {
		return uuid.Nil, false
	}
	return found, true
}

// Edge is a plain directed dependency link between two nodes in the same
// workflow: "once source is done, re-evaluate target's assignment."
type Edge struct {
	ID           uuid.UUID
	WorkflowID   uuid.UUID
	SourceNodeID uuid.UUID
	TargetNodeID uuid.UUID
	CreatedAt    time.Time
}

// Graph bundles a workflow with its full node/transition/edge set, as
// returned by the single-fetch "get workflow" read used to hydrate the
// canvas builder.
type Graph struct {
	Workflow          *Workflow
	Nodes             []*Node
	StatusTransitions []*StatusTransition
	Edges             []*Edge
}
