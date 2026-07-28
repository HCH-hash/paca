// Package statusruledom defines the status-assignment-rule aggregate: a
// project-scoped rule that says "when a task's status becomes X (and,
// optionally, the task matches these field filters), assign it to member
// M." Unlike the automation-workflow feature, a rule is not tied to any
// workflow or node — it applies to every task in the project, including
// tasks that are not part of any workflow.
//
// Several rules may target the same (project, status) pair; Priority
// (lower first) breaks the tie when more than one rule's filter matches a
// given task — the first enabled match wins. A rule with an empty Filter
// matches every task with that status, i.e. applies project-wide with no
// scoping at all.
package statusruledom

import (
	"time"

	"github.com/google/uuid"

	taskdom "github.com/Paca-AI/api/internal/domain/task"
)

// StatusAssignmentRule is the aggregate root.
type StatusAssignmentRule struct {
	ID   uuid.UUID
	Name string
	// ProjectID is the project this rule belongs to and applies within.
	ProjectID uuid.UUID
	// StatusID is the trigger status: the rule is evaluated whenever a task
	// in this project reaches this status. Immutable after creation — delete
	// and recreate instead of moving a rule to a different status.
	StatusID uuid.UUID
	// AssigneeMemberID is who the task is reassigned to when this rule matches.
	AssigneeMemberID uuid.UUID
	// Filter scopes which tasks at StatusID this rule applies to. A zero
	// value (all fields empty/nil) matches every task at that status.
	Filter TaskFilterSpec
	// Priority orders rules that share the same (ProjectID, StatusID): lower
	// values are evaluated first, and the first match wins. Assigned as
	// "append to end of this status's list" on creation; reorderable via
	// Service.ReorderRules.
	Priority int
	// Enabled lets a rule be temporarily turned off without deleting it.
	Enabled   bool
	CreatedBy *uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TaskFilterSpec is the persisted (JSONB) filter shape for one rule. It
// mirrors the filterable subset of taskdom.TaskFilter field-for-field so it
// can be translated into one directly (see the statusrulesvc matching
// logic) without re-implementing filter evaluation.
type TaskFilterSpec struct {
	TaskTypeIDs []uuid.UUID `json:"task_type_ids,omitempty"`

	SprintIDs   []uuid.UUID `json:"sprint_ids,omitempty"`
	BacklogOnly bool        `json:"backlog_only,omitempty"`

	AssigneeIDs  []uuid.UUID `json:"assignee_ids,omitempty"`
	AssigneeNull bool        `json:"assignee_null,omitempty"`

	Tags []string `json:"tags,omitempty"`

	ImportanceRanges []taskdom.IntRange `json:"importance_ranges,omitempty"`

	StoryPointsMin *int `json:"story_points_min,omitempty"`
	StoryPointsMax *int `json:"story_points_max,omitempty"`

	StartDateAfter  *string `json:"start_date_after,omitempty"`
	StartDateBefore *string `json:"start_date_before,omitempty"`
	DueDateAfter    *string `json:"due_date_after,omitempty"`
	DueDateBefore   *string `json:"due_date_before,omitempty"`

	// CustomFields is keyed by custom-field-definition field_key. FieldType
	// is intentionally not part of this struct — it is never trusted from
	// storage, only ever resolved fresh from the project's current
	// CustomFieldDefinition at write time (rejecting unknown keys) and at
	// match time (skipping keys whose definition has since been deleted).
	CustomFields map[string]CustomFieldFilterSpec `json:"custom_fields,omitempty"`
}

// CustomFieldFilterSpec carries the same members as
// taskdom.CustomFieldFilterQuery, minus FieldType.
type CustomFieldFilterSpec struct {
	Values   []string `json:"values,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	After    *string  `json:"after,omitempty"`
	Before   *string  `json:"before,omitempty"`
	Contains *string  `json:"contains,omitempty"`
}

// IsEmpty reports whether f has no scoping at all, i.e. matches every task.
func (f TaskFilterSpec) IsEmpty() bool {
	return len(f.TaskTypeIDs) == 0 &&
		len(f.SprintIDs) == 0 && !f.BacklogOnly &&
		len(f.AssigneeIDs) == 0 && !f.AssigneeNull &&
		len(f.Tags) == 0 &&
		len(f.ImportanceRanges) == 0 &&
		f.StoryPointsMin == nil && f.StoryPointsMax == nil &&
		f.StartDateAfter == nil && f.StartDateBefore == nil &&
		f.DueDateAfter == nil && f.DueDateBefore == nil &&
		len(f.CustomFields) == 0
}
