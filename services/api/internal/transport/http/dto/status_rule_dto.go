package dto

import (
	"time"

	"github.com/google/uuid"

	statusruledom "github.com/Paca-AI/api/internal/domain/statusrule"
	taskdom "github.com/Paca-AI/api/internal/domain/task"
)

// IntRangeDTO is the wire shape of one inclusive [min,max] range, mirroring
// taskdom.IntRange.
type IntRangeDTO struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// CustomFieldFilterSpecDTO is the wire shape of one custom-field filter
// entry within a rule's Filter, mirroring statusruledom.CustomFieldFilterSpec.
// Which members are meaningful is determined server-side from the field's
// CustomFieldDefinition — see status_rule_handler.go.
type CustomFieldFilterSpecDTO struct {
	Values   []string `json:"values,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	After    *string  `json:"after,omitempty"`
	Before   *string  `json:"before,omitempty"`
	Contains *string  `json:"contains,omitempty"`
}

// TaskFilterSpecDTO is the wire shape of a rule's Filter, mirroring
// statusruledom.TaskFilterSpec field-for-field. Used for both request
// bodies (ToDomain) and response bodies (TaskFilterSpecFromEntity).
type TaskFilterSpecDTO struct {
	TaskTypeIDs []uuid.UUID `json:"task_type_ids,omitempty"`

	SprintIDs   []uuid.UUID `json:"sprint_ids,omitempty"`
	BacklogOnly bool        `json:"backlog_only,omitempty"`

	AssigneeIDs  []uuid.UUID `json:"assignee_ids,omitempty"`
	AssigneeNull bool        `json:"assignee_null,omitempty"`

	Tags []string `json:"tags,omitempty"`

	ImportanceRanges []IntRangeDTO `json:"importance_ranges,omitempty"`

	StoryPointsMin *int `json:"story_points_min,omitempty"`
	StoryPointsMax *int `json:"story_points_max,omitempty"`

	StartDateAfter  *string `json:"start_date_after,omitempty"`
	StartDateBefore *string `json:"start_date_before,omitempty"`
	DueDateAfter    *string `json:"due_date_after,omitempty"`
	DueDateBefore   *string `json:"due_date_before,omitempty"`

	CustomFields map[string]CustomFieldFilterSpecDTO `json:"custom_fields,omitempty"`
}

// ToDomain converts d to a statusruledom.TaskFilterSpec.
func (d TaskFilterSpecDTO) ToDomain() statusruledom.TaskFilterSpec {
	ranges := make([]taskdom.IntRange, 0, len(d.ImportanceRanges))
	for _, r := range d.ImportanceRanges {
		ranges = append(ranges, taskdom.IntRange{Min: r.Min, Max: r.Max})
	}
	var customFields map[string]statusruledom.CustomFieldFilterSpec
	if len(d.CustomFields) > 0 {
		customFields = make(map[string]statusruledom.CustomFieldFilterSpec, len(d.CustomFields))
		for key, cf := range d.CustomFields {
			customFields[key] = statusruledom.CustomFieldFilterSpec{
				Values:   cf.Values,
				Min:      cf.Min,
				Max:      cf.Max,
				After:    cf.After,
				Before:   cf.Before,
				Contains: cf.Contains,
			}
		}
	}
	return statusruledom.TaskFilterSpec{
		TaskTypeIDs:      d.TaskTypeIDs,
		SprintIDs:        d.SprintIDs,
		BacklogOnly:      d.BacklogOnly,
		AssigneeIDs:      d.AssigneeIDs,
		AssigneeNull:     d.AssigneeNull,
		Tags:             d.Tags,
		ImportanceRanges: ranges,
		StoryPointsMin:   d.StoryPointsMin,
		StoryPointsMax:   d.StoryPointsMax,
		StartDateAfter:   d.StartDateAfter,
		StartDateBefore:  d.StartDateBefore,
		DueDateAfter:     d.DueDateAfter,
		DueDateBefore:    d.DueDateBefore,
		CustomFields:     customFields,
	}
}

// TaskFilterSpecFromEntity maps a domain TaskFilterSpec to a TaskFilterSpecDTO.
func TaskFilterSpecFromEntity(f statusruledom.TaskFilterSpec) TaskFilterSpecDTO {
	ranges := make([]IntRangeDTO, 0, len(f.ImportanceRanges))
	for _, r := range f.ImportanceRanges {
		ranges = append(ranges, IntRangeDTO{Min: r.Min, Max: r.Max})
	}
	var customFields map[string]CustomFieldFilterSpecDTO
	if len(f.CustomFields) > 0 {
		customFields = make(map[string]CustomFieldFilterSpecDTO, len(f.CustomFields))
		for key, cf := range f.CustomFields {
			customFields[key] = CustomFieldFilterSpecDTO{
				Values:   cf.Values,
				Min:      cf.Min,
				Max:      cf.Max,
				After:    cf.After,
				Before:   cf.Before,
				Contains: cf.Contains,
			}
		}
	}
	return TaskFilterSpecDTO{
		TaskTypeIDs:      f.TaskTypeIDs,
		SprintIDs:        f.SprintIDs,
		BacklogOnly:      f.BacklogOnly,
		AssigneeIDs:      f.AssigneeIDs,
		AssigneeNull:     f.AssigneeNull,
		Tags:             f.Tags,
		ImportanceRanges: ranges,
		StoryPointsMin:   f.StoryPointsMin,
		StoryPointsMax:   f.StoryPointsMax,
		StartDateAfter:   f.StartDateAfter,
		StartDateBefore:  f.StartDateBefore,
		DueDateAfter:     f.DueDateAfter,
		DueDateBefore:    f.DueDateBefore,
		CustomFields:     customFields,
	}
}

// CreateStatusAssignmentRuleRequest is the body for
// POST /projects/:projectId/status-assignment-rules.
type CreateStatusAssignmentRuleRequest struct {
	Name             string            `json:"name"`
	StatusID         uuid.UUID         `json:"status_id"`
	AssigneeMemberID uuid.UUID         `json:"assignee_member_id"`
	Filter           TaskFilterSpecDTO `json:"filter"`
	Enabled          *bool             `json:"enabled,omitempty"`
}

// UpdateStatusAssignmentRuleRequest is the body for
// PATCH /projects/:projectId/status-assignment-rules/:ruleId. StatusID is
// intentionally not included — immutable after creation.
type UpdateStatusAssignmentRuleRequest struct {
	Name             *string            `json:"name,omitempty"`
	AssigneeMemberID *uuid.UUID         `json:"assignee_member_id,omitempty"`
	Filter           *TaskFilterSpecDTO `json:"filter,omitempty"`
	Enabled          *bool              `json:"enabled,omitempty"`
}

// ReorderStatusAssignmentRulesRequest is the body for
// PUT /projects/:projectId/status-assignment-rules/positions. RuleIDs must
// be exactly StatusID's existing rule IDs, in the new desired order.
type ReorderStatusAssignmentRulesRequest struct {
	StatusID uuid.UUID   `json:"status_id"`
	RuleIDs  []uuid.UUID `json:"rule_ids"`
}

// StatusAssignmentRuleResponse is the public representation of a rule.
type StatusAssignmentRuleResponse struct {
	ID               uuid.UUID         `json:"id"`
	ProjectID        uuid.UUID         `json:"project_id"`
	Name             string            `json:"name"`
	StatusID         uuid.UUID         `json:"status_id"`
	AssigneeMemberID uuid.UUID         `json:"assignee_member_id"`
	Filter           TaskFilterSpecDTO `json:"filter"`
	Priority         int               `json:"priority"`
	Enabled          bool              `json:"enabled"`
	CreatedBy        *uuid.UUID        `json:"created_by,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// StatusAssignmentRuleFromEntity maps a domain StatusAssignmentRule to a
// StatusAssignmentRuleResponse DTO.
func StatusAssignmentRuleFromEntity(r *statusruledom.StatusAssignmentRule) StatusAssignmentRuleResponse {
	return StatusAssignmentRuleResponse{
		ID:               r.ID,
		ProjectID:        r.ProjectID,
		Name:             r.Name,
		StatusID:         r.StatusID,
		AssigneeMemberID: r.AssigneeMemberID,
		Filter:           TaskFilterSpecFromEntity(r.Filter),
		Priority:         r.Priority,
		Enabled:          r.Enabled,
		CreatedBy:        r.CreatedBy,
		CreatedAt:        r.CreatedAt,
		UpdatedAt:        r.UpdatedAt,
	}
}
