// Package statusrulesvc implements statusruledom.Service.
package statusrulesvc

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	projectdom "github.com/Paca-AI/api/internal/domain/project"
	statusruledom "github.com/Paca-AI/api/internal/domain/statusrule"
	taskdom "github.com/Paca-AI/api/internal/domain/task"
	userdom "github.com/Paca-AI/api/internal/domain/user"
	"github.com/Paca-AI/api/internal/events"
	"github.com/Paca-AI/api/internal/platform/messaging"
)

// taskLookup is the minimal task-domain surface the status-rule service
// needs: matching (CountTasks), reassignment (UpdateTask), and resolving
// custom-field types (ListCustomFieldDefinitions) and status existence
// (GetTaskStatus) at write time.
type taskLookup interface {
	CountTasks(ctx context.Context, projectID uuid.UUID, filter taskdom.TaskFilter) (int64, error)
	UpdateTask(ctx context.Context, projectID, id uuid.UUID, in taskdom.UpdateTaskInput) (*taskdom.Task, error)
	ListCustomFieldDefinitions(ctx context.Context, projectID uuid.UUID) ([]*taskdom.CustomFieldDefinition, error)
	GetTaskStatus(ctx context.Context, id uuid.UUID) (*taskdom.TaskStatus, error)
}

// memberLookup is the minimal project-domain surface the status-rule
// service needs to validate an assignee belongs to the rule's project and
// to resolve an authenticated actor to their project_members.id.
type memberLookup interface {
	FindMemberByID(ctx context.Context, memberID uuid.UUID) (*projectdom.ProjectMember, error)
	// FindMemberByActor resolves an authenticated actor (user, or agent when
	// agentID is non-nil) to their project_members.id.
	FindMemberByActor(ctx context.Context, projectID, actorID uuid.UUID, agentID *uuid.UUID) (*projectdom.ProjectMember, error)
}

// activityRecorder posts the status_rule.assigned activity entry.
type activityRecorder interface {
	RecordActivity(ctx context.Context, in taskdom.RecordActivityInput) error
}

// Service implements statusruledom.Service.
type Service struct {
	repo        statusruledom.Repository
	taskRepo    taskLookup
	memberRepo  memberLookup
	activityRec activityRecorder
	publisher   *messaging.Publisher
}

// New returns a Service backed by repo and the given collaborators.
// activityRec/publisher may be nil; the corresponding side effect is then
// skipped silently.
func New(repo statusruledom.Repository, tasks taskLookup, members memberLookup, activityRec activityRecorder, publisher *messaging.Publisher) *Service {
	return &Service{repo: repo, taskRepo: tasks, memberRepo: members, activityRec: activityRec, publisher: publisher}
}

// ListRules returns every rule in projectID, across all statuses.
func (s *Service) ListRules(ctx context.Context, projectID uuid.UUID) ([]*statusruledom.StatusAssignmentRule, error) {
	return s.repo.ListRulesByProject(ctx, projectID)
}

// CreateRule validates and persists a new rule, appended to the end of its
// status's priority list.
func (s *Service) CreateRule(ctx context.Context, in statusruledom.CreateRuleInput) (*statusruledom.StatusAssignmentRule, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, statusruledom.ErrNameInvalid
	}
	if err := s.validateCrossProject(ctx, in.ProjectID, in.StatusID, in.AssigneeMemberID); err != nil {
		return nil, err
	}
	if err := s.validateFilterCustomFields(ctx, in.ProjectID, in.Filter); err != nil {
		return nil, err
	}

	// Disabled rules still occupy a priority slot, so the next priority is
	// computed from every rule targeting this status, not just enabled ones.
	all, err := s.repo.ListRulesByProject(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}
	priority := 0
	for _, r := range all {
		if r.StatusID == in.StatusID && r.Priority >= priority {
			priority = r.Priority + 1
		}
	}

	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := time.Now()
	r := &statusruledom.StatusAssignmentRule{
		ID:               uuid.New(),
		ProjectID:        in.ProjectID,
		Name:             name,
		StatusID:         in.StatusID,
		AssigneeMemberID: in.AssigneeMemberID,
		Filter:           in.Filter,
		Priority:         priority,
		Enabled:          enabled,
		CreatedBy:        s.resolveMember(ctx, in.CreatedBy, in.AgentID, in.ProjectID),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.repo.CreateRule(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateRule applies in to the rule identified by ruleID, verifying it
// belongs to projectID.
func (s *Service) UpdateRule(ctx context.Context, projectID, ruleID uuid.UUID, in statusruledom.UpdateRuleInput) (*statusruledom.StatusAssignmentRule, error) {
	r, err := s.findOwnedRule(ctx, projectID, ruleID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, statusruledom.ErrNameInvalid
		}
		r.Name = name
	}
	if in.AssigneeMemberID != nil {
		member, err := s.memberRepo.FindMemberByID(ctx, *in.AssigneeMemberID)
		if err != nil {
			return nil, err
		}
		if member.ProjectID != projectID {
			return nil, statusruledom.ErrCrossProject
		}
		r.AssigneeMemberID = *in.AssigneeMemberID
	}
	if in.Filter != nil {
		if err := s.validateFilterCustomFields(ctx, projectID, *in.Filter); err != nil {
			return nil, err
		}
		r.Filter = *in.Filter
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	r.UpdatedAt = time.Now()
	if err := s.repo.UpdateRule(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteRule removes a rule, verifying it belongs to projectID.
func (s *Service) DeleteRule(ctx context.Context, projectID, ruleID uuid.UUID) error {
	r, err := s.findOwnedRule(ctx, projectID, ruleID)
	if err != nil {
		return err
	}
	return s.repo.DeleteRule(ctx, r.ID)
}

// ReorderRules re-prioritizes every rule targeting (projectID, statusID).
func (s *Service) ReorderRules(ctx context.Context, projectID, statusID uuid.UUID, ruleIDs []uuid.UUID) error {
	return s.repo.ReorderRules(ctx, projectID, statusID, ruleIDs)
}

// ApplyMatchingRule finds the highest-priority enabled rule targeting
// task's current status whose filter matches task and, if its assignee
// differs from task's current assignee(s), reassigns task.
func (s *Service) ApplyMatchingRule(ctx context.Context, projectID uuid.UUID, task *taskdom.Task, reason string, extra map[string]any) error {
	if task.StatusID == nil {
		return nil
	}

	rules, err := s.repo.ListEnabledRulesByProjectAndStatus(ctx, projectID, *task.StatusID)
	if err != nil {
		return err
	}

	var matched *statusruledom.StatusAssignmentRule
	for _, r := range rules {
		ok, err := s.matches(ctx, projectID, task.ID, r.Filter)
		if err != nil {
			return err
		}
		if ok {
			matched = r
			break
		}
	}
	if matched == nil {
		return nil
	}
	if len(task.AssigneeIDs) == 1 && task.AssigneeIDs[0] == matched.AssigneeMemberID {
		return nil // already assigned exactly to the rule's member — idempotent no-op
	}

	oldAssignees := task.AssigneeIDs
	newAssignee := matched.AssigneeMemberID
	newAssigneeIDs := []uuid.UUID{newAssignee}
	if _, err := s.taskRepo.UpdateTask(ctx, projectID, task.ID, taskdom.UpdateTaskInput{AssigneeIDs: &newAssigneeIDs}); err != nil {
		return err
	}
	task.AssigneeIDs = newAssigneeIDs

	if s.activityRec != nil {
		content := map[string]any{
			"rule_id":       matched.ID,
			"rule_name":     matched.Name,
			"reason":        reason,
			"old_assignees": oldAssignees,
			"new_assignee":  newAssignee,
		}
		if b, err := json.Marshal(content); err == nil {
			_ = s.activityRec.RecordActivity(ctx, taskdom.RecordActivityInput{
				TaskID:       task.ID,
				ProjectID:    projectID,
				ActivityType: taskdom.ActivityTypeStatusRuleAssigned,
				Content:      b,
			})
		}
	}

	// Only notify for a genuinely new assignment — mirrors the same dedup
	// the HTTP UpdateTask handler and the automation-workflow engine apply.
	if !slices.Contains(oldAssignees, newAssignee) {
		payload := map[string]any{
			"rule_id":   matched.ID.String(),
			"rule_name": matched.Name,
		}
		for k, v := range extra {
			payload[k] = v
		}
		_ = events.PublishAssignmentChanged(ctx, s.publisher, task.ID, projectID, newAssignee, nil, userdom.SystemActorUserID, payload)
	}

	return nil
}

// matches reports whether taskID (in projectID) satisfies filter, by
// translating filter into a taskdom.TaskFilter restricted to that one task
// and delegating to the same SQL used by ListTasks/CountTasks — this reuses
// all existing filter-evaluation logic (including server-side custom-field
// type casting) instead of duplicating it in Go.
func (s *Service) matches(ctx context.Context, projectID, taskID uuid.UUID, filter statusruledom.TaskFilterSpec) (bool, error) {
	if filter.IsEmpty() {
		return true, nil
	}

	tf := taskdom.TaskFilter{
		TaskID:           &taskID,
		TaskTypeIDs:      filter.TaskTypeIDs,
		SprintIDs:        filter.SprintIDs,
		BacklogOnly:      filter.BacklogOnly,
		AssigneeIDs:      filter.AssigneeIDs,
		AssigneeNull:     filter.AssigneeNull,
		Tags:             filter.Tags,
		ImportanceRanges: filter.ImportanceRanges,
		StoryPointsMin:   filter.StoryPointsMin,
		StoryPointsMax:   filter.StoryPointsMax,
		StartDateAfter:   filter.StartDateAfter,
		StartDateBefore:  filter.StartDateBefore,
		DueDateAfter:     filter.DueDateAfter,
		DueDateBefore:    filter.DueDateBefore,
	}

	if len(filter.CustomFields) > 0 {
		defs, err := s.taskRepo.ListCustomFieldDefinitions(ctx, projectID)
		if err != nil {
			return false, err
		}
		byKey := make(map[string]*taskdom.CustomFieldDefinition, len(defs))
		for _, d := range defs {
			byKey[d.FieldKey] = d
		}
		resolved := make(map[string]taskdom.CustomFieldFilterQuery, len(filter.CustomFields))
		for key, q := range filter.CustomFields {
			def, ok := byKey[key]
			if !ok {
				// The custom field was deleted after this rule was
				// created — skip it rather than fail the whole match.
				continue
			}
			resolved[key] = taskdom.CustomFieldFilterQuery{
				FieldType: string(def.FieldType),
				Values:    q.Values,
				Min:       q.Min,
				Max:       q.Max,
				After:     q.After,
				Before:    q.Before,
				Contains:  q.Contains,
			}
		}
		tf.CustomFieldFilters = resolved
	}

	count, err := s.taskRepo.CountTasks(ctx, projectID, tf)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// validateCrossProject checks that statusID and memberID both belong to
// projectID.
func (s *Service) validateCrossProject(ctx context.Context, projectID, statusID, memberID uuid.UUID) error {
	status, err := s.taskRepo.GetTaskStatus(ctx, statusID)
	if err != nil {
		return err
	}
	if status.ProjectID != projectID {
		return statusruledom.ErrCrossProject
	}
	member, err := s.memberRepo.FindMemberByID(ctx, memberID)
	if err != nil {
		return err
	}
	if member.ProjectID != projectID {
		return statusruledom.ErrCrossProject
	}
	return nil
}

// validateFilterCustomFields rejects a filter that references a custom
// field key with no corresponding definition in projectID — fail fast at
// write time rather than silently ignoring an unknown key at match time
// forever.
func (s *Service) validateFilterCustomFields(ctx context.Context, projectID uuid.UUID, filter statusruledom.TaskFilterSpec) error {
	if len(filter.CustomFields) == 0 {
		return nil
	}
	defs, err := s.taskRepo.ListCustomFieldDefinitions(ctx, projectID)
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		known[d.FieldKey] = struct{}{}
	}
	for key := range filter.CustomFields {
		if _, ok := known[key]; !ok {
			return statusruledom.ErrFilterUnknownCustomField
		}
	}
	return nil
}

// resolveMember resolves an authenticated actor to their project_members.id
// for storage in StatusAssignmentRule.CreatedBy (which references
// project_members, not users/agents directly). When agentID is non-nil, it
// resolves to the agent's own member row instead of userID's. Returns nil
// (no error) when userID is nil or the actor can't be resolved as a member
// of this project — CreatedBy is purely informational, so a resolution
// failure shouldn't block the write.
func (s *Service) resolveMember(ctx context.Context, userID, agentID *uuid.UUID, projectID uuid.UUID) *uuid.UUID {
	if userID == nil {
		return nil
	}
	member, err := s.memberRepo.FindMemberByActor(ctx, projectID, *userID, agentID)
	if err != nil {
		return nil
	}
	return &member.ID
}

func (s *Service) findOwnedRule(ctx context.Context, projectID, ruleID uuid.UUID) (*statusruledom.StatusAssignmentRule, error) {
	r, err := s.repo.FindRuleByID(ctx, ruleID)
	if err != nil {
		return nil, err
	}
	if r.ProjectID != projectID {
		return nil, statusruledom.ErrNotFound
	}
	return r, nil
}
