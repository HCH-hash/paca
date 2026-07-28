// Package statusrulesvc_test contains unit tests for the status-assignment-
// rule service layer. Tests use in-memory fakes and do not require any
// infrastructure. fakeTaskLookup.CountTasks implements a deliberately
// simplified interpretation of taskdom.TaskFilter (TaskID + TaskTypeIDs
// only) — enough to exercise priority/routing/idempotency without
// re-testing the real SQL filter builder, which belongs to
// repository/postgres's own tests.
package statusrulesvc_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	projectdom "github.com/Paca-AI/api/internal/domain/project"
	statusruledom "github.com/Paca-AI/api/internal/domain/statusrule"
	taskdom "github.com/Paca-AI/api/internal/domain/task"
	statusrulesvc "github.com/Paca-AI/api/internal/service/statusrule"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeRuleRepo struct {
	mu    sync.Mutex
	rules map[uuid.UUID]*statusruledom.StatusAssignmentRule
}

func newFakeRuleRepo() *fakeRuleRepo {
	return &fakeRuleRepo{rules: make(map[uuid.UUID]*statusruledom.StatusAssignmentRule)}
}

func (r *fakeRuleRepo) CreateRule(_ context.Context, rule *statusruledom.StatusAssignmentRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *fakeRuleRepo) FindRuleByID(_ context.Context, id uuid.UUID) (*statusruledom.StatusAssignmentRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule, ok := r.rules[id]
	if !ok {
		return nil, statusruledom.ErrNotFound
	}
	cp := *rule
	return &cp, nil
}

func (r *fakeRuleRepo) ListRulesByProject(_ context.Context, projectID uuid.UUID) ([]*statusruledom.StatusAssignmentRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*statusruledom.StatusAssignmentRule
	for _, rule := range r.rules {
		if rule.ProjectID == projectID {
			cp := *rule
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *fakeRuleRepo) ListEnabledRulesByProjectAndStatus(_ context.Context, projectID, statusID uuid.UUID) ([]*statusruledom.StatusAssignmentRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*statusruledom.StatusAssignmentRule
	for _, rule := range r.rules {
		if rule.ProjectID == projectID && rule.StatusID == statusID && rule.Enabled {
			cp := *rule
			out = append(out, &cp)
		}
	}
	// Sort by priority ascending (simple insertion sort, fine for tests).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Priority > out[j].Priority; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}

func (r *fakeRuleRepo) UpdateRule(_ context.Context, rule *statusruledom.StatusAssignmentRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rules[rule.ID]; !ok {
		return statusruledom.ErrNotFound
	}
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *fakeRuleRepo) DeleteRule(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rules[id]; !ok {
		return statusruledom.ErrNotFound
	}
	delete(r.rules, id)
	return nil
}

func (r *fakeRuleRepo) ReorderRules(_ context.Context, projectID, statusID uuid.UUID, ruleIDs []uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, id := range ruleIDs {
		rule, ok := r.rules[id]
		if !ok || rule.ProjectID != projectID || rule.StatusID != statusID {
			return statusruledom.ErrReorderInvalid
		}
		rule.Priority = i
	}
	return nil
}

func (r *fakeRuleRepo) StatusUsedByStatusRule(_ context.Context, statusID uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rule := range r.rules {
		if rule.StatusID == statusID {
			return true, nil
		}
	}
	return false, nil
}

// fakeTaskLookup implements the minimal taskLookup surface. CountTasks only
// interprets TaskID and TaskTypeIDs — see package doc comment.
type fakeTaskLookup struct {
	tasks       map[uuid.UUID]*taskdom.Task
	statuses    map[uuid.UUID]*taskdom.TaskStatus
	customDefs  []*taskdom.CustomFieldDefinition
	updateCalls int
}

func newFakeTaskLookup() *fakeTaskLookup {
	return &fakeTaskLookup{
		tasks:    make(map[uuid.UUID]*taskdom.Task),
		statuses: make(map[uuid.UUID]*taskdom.TaskStatus),
	}
}

func (f *fakeTaskLookup) CountTasks(_ context.Context, _ uuid.UUID, filter taskdom.TaskFilter) (int64, error) {
	if filter.TaskID == nil {
		return 0, errors.New("test fake: expected TaskID filter")
	}
	task, ok := f.tasks[*filter.TaskID]
	if !ok {
		return 0, nil
	}
	if len(filter.TaskTypeIDs) > 0 {
		match := false
		for _, id := range filter.TaskTypeIDs {
			if task.TaskTypeID != nil && *task.TaskTypeID == id {
				match = true
				break
			}
		}
		if !match {
			return 0, nil
		}
	}
	return 1, nil
}

func (f *fakeTaskLookup) UpdateTask(_ context.Context, _, id uuid.UUID, in taskdom.UpdateTaskInput) (*taskdom.Task, error) {
	f.updateCalls++
	task, ok := f.tasks[id]
	if !ok {
		return nil, taskdom.ErrTaskNotFound
	}
	if in.AssigneeIDs != nil {
		task.AssigneeIDs = *in.AssigneeIDs
	}
	cp := *task
	return &cp, nil
}

func (f *fakeTaskLookup) ListCustomFieldDefinitions(_ context.Context, _ uuid.UUID) ([]*taskdom.CustomFieldDefinition, error) {
	return f.customDefs, nil
}

func (f *fakeTaskLookup) GetTaskStatus(_ context.Context, id uuid.UUID) (*taskdom.TaskStatus, error) {
	st, ok := f.statuses[id]
	if !ok {
		return nil, taskdom.ErrStatusNotFound
	}
	return st, nil
}

type fakeMemberLookup struct {
	members map[uuid.UUID]*projectdom.ProjectMember
}

func newFakeMemberLookup() *fakeMemberLookup {
	return &fakeMemberLookup{members: make(map[uuid.UUID]*projectdom.ProjectMember)}
}

func (f *fakeMemberLookup) FindMemberByID(_ context.Context, memberID uuid.UUID) (*projectdom.ProjectMember, error) {
	m, ok := f.members[memberID]
	if !ok {
		return nil, errors.New("member not found")
	}
	return m, nil
}

func (f *fakeMemberLookup) FindMemberByActor(_ context.Context, projectID, actorID uuid.UUID, _ *uuid.UUID) (*projectdom.ProjectMember, error) {
	for _, m := range f.members {
		if m.ProjectID == projectID && m.UserID == actorID {
			return m, nil
		}
	}
	return nil, errors.New("actor not found")
}

type fakeActivityRecorder struct{ calls int }

func (f *fakeActivityRecorder) RecordActivity(_ context.Context, _ taskdom.RecordActivityInput) error {
	f.calls++
	return nil
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

type fixture struct {
	repo     *fakeRuleRepo
	tasks    *fakeTaskLookup
	members  *fakeMemberLookup
	activity *fakeActivityRecorder
	svc      *statusrulesvc.Service

	projectID uuid.UUID
}

func newFixture() *fixture {
	repo := newFakeRuleRepo()
	tasks := newFakeTaskLookup()
	members := newFakeMemberLookup()
	activity := &fakeActivityRecorder{}
	return &fixture{
		repo:      repo,
		tasks:     tasks,
		members:   members,
		activity:  activity,
		svc:       statusrulesvc.New(repo, tasks, members, activity, nil),
		projectID: uuid.New(),
	}
}

func (f *fixture) addStatus() *taskdom.TaskStatus {
	st := &taskdom.TaskStatus{ID: uuid.New(), ProjectID: f.projectID, Name: "status"}
	f.tasks.statuses[st.ID] = st
	return st
}

func (f *fixture) addMember(projectID uuid.UUID) *projectdom.ProjectMember {
	m := &projectdom.ProjectMember{ID: uuid.New(), ProjectID: projectID, UserID: uuid.New()}
	f.members.members[m.ID] = m
	return m
}

func (f *fixture) addTask(statusID uuid.UUID, taskTypeID *uuid.UUID) *taskdom.Task {
	task := &taskdom.Task{ID: uuid.New(), ProjectID: f.projectID, StatusID: &statusID, TaskTypeID: taskTypeID}
	f.tasks.tasks[task.ID] = task
	return task
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCreateRule_RejectsEmptyName(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	member := f.addMember(f.projectID)

	_, err := f.svc.CreateRule(context.Background(), statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "  ", StatusID: status.ID, AssigneeMemberID: member.ID,
	})
	if !errors.Is(err, statusruledom.ErrNameInvalid) {
		t.Fatalf("expected ErrNameInvalid, got %v", err)
	}
}

func TestCreateRule_RejectsCrossProjectStatus(t *testing.T) {
	f := newFixture()
	otherStatus := &taskdom.TaskStatus{ID: uuid.New(), ProjectID: uuid.New()}
	f.tasks.statuses[otherStatus.ID] = otherStatus
	member := f.addMember(f.projectID)

	_, err := f.svc.CreateRule(context.Background(), statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "rule", StatusID: otherStatus.ID, AssigneeMemberID: member.ID,
	})
	if !errors.Is(err, statusruledom.ErrCrossProject) {
		t.Fatalf("expected ErrCrossProject, got %v", err)
	}
}

func TestCreateRule_RejectsUnknownCustomField(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	member := f.addMember(f.projectID)

	_, err := f.svc.CreateRule(context.Background(), statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "rule", StatusID: status.ID, AssigneeMemberID: member.ID,
		Filter: statusruledom.TaskFilterSpec{
			CustomFields: map[string]statusruledom.CustomFieldFilterSpec{"nonexistent": {}},
		},
	})
	if !errors.Is(err, statusruledom.ErrFilterUnknownCustomField) {
		t.Fatalf("expected ErrFilterUnknownCustomField, got %v", err)
	}
}

func TestCreateRule_AppendsToEndOfPriorityList(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	member := f.addMember(f.projectID)
	ctx := context.Background()

	r1, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{ProjectID: f.projectID, Name: "r1", StatusID: status.ID, AssigneeMemberID: member.ID})
	if err != nil {
		t.Fatalf("CreateRule r1: %v", err)
	}
	r2, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{ProjectID: f.projectID, Name: "r2", StatusID: status.ID, AssigneeMemberID: member.ID})
	if err != nil {
		t.Fatalf("CreateRule r2: %v", err)
	}
	if r1.Priority != 0 || r2.Priority != 1 {
		t.Fatalf("expected priorities 0,1, got %d,%d", r1.Priority, r2.Priority)
	}
}

func TestApplyMatchingRule_FirstEnabledMatchByPriorityWins(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	taskTypeA := uuid.New()
	memberGeneral := f.addMember(f.projectID)
	memberTypeA := f.addMember(f.projectID)
	ctx := context.Background()

	// Priority 0: unfiltered, matches everything.
	general, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "general", StatusID: status.ID, AssigneeMemberID: memberGeneral.ID,
	})
	if err != nil {
		t.Fatalf("CreateRule general: %v", err)
	}
	// Priority 1: filtered to taskTypeA.
	if _, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "type-a", StatusID: status.ID, AssigneeMemberID: memberTypeA.ID,
		Filter: statusruledom.TaskFilterSpec{TaskTypeIDs: []uuid.UUID{taskTypeA}},
	}); err != nil {
		t.Fatalf("CreateRule type-a: %v", err)
	}

	task := f.addTask(status.ID, &taskTypeA)
	if err := f.svc.ApplyMatchingRule(ctx, f.projectID, task, "status_rule", nil); err != nil {
		t.Fatalf("ApplyMatchingRule: %v", err)
	}
	if len(task.AssigneeIDs) != 1 || task.AssigneeIDs[0] != memberGeneral.ID {
		t.Fatalf("expected the lower-priority (first-created) unfiltered rule %v to win, got %+v", general.ID, task.AssigneeIDs)
	}
}

func TestApplyMatchingRule_SkipsNonMatchingFilterFallsThroughToNextRule(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	taskTypeA := uuid.New()
	taskTypeB := uuid.New()
	memberTypeA := f.addMember(f.projectID)
	memberFallback := f.addMember(f.projectID)
	ctx := context.Background()

	if _, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "type-a", StatusID: status.ID, AssigneeMemberID: memberTypeA.ID,
		Filter: statusruledom.TaskFilterSpec{TaskTypeIDs: []uuid.UUID{taskTypeA}},
	}); err != nil {
		t.Fatalf("CreateRule type-a: %v", err)
	}
	if _, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "fallback", StatusID: status.ID, AssigneeMemberID: memberFallback.ID,
	}); err != nil {
		t.Fatalf("CreateRule fallback: %v", err)
	}

	// Task is type B, so the first (type-A-filtered) rule doesn't match;
	// the second (unfiltered) rule should.
	task := f.addTask(status.ID, &taskTypeB)
	if err := f.svc.ApplyMatchingRule(ctx, f.projectID, task, "status_rule", nil); err != nil {
		t.Fatalf("ApplyMatchingRule: %v", err)
	}
	if len(task.AssigneeIDs) != 1 || task.AssigneeIDs[0] != memberFallback.ID {
		t.Fatalf("expected fallback rule's member %v, got %+v", memberFallback.ID, task.AssigneeIDs)
	}
}

func TestApplyMatchingRule_IdempotentWhenAlreadyAssigned(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	member := f.addMember(f.projectID)
	ctx := context.Background()

	if _, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "rule", StatusID: status.ID, AssigneeMemberID: member.ID,
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	task := f.addTask(status.ID, nil)
	task.AssigneeIDs = []uuid.UUID{member.ID} // already assigned before the event fires

	if err := f.svc.ApplyMatchingRule(ctx, f.projectID, task, "status_rule", nil); err != nil {
		t.Fatalf("ApplyMatchingRule: %v", err)
	}
	if f.tasks.updateCalls != 0 {
		t.Fatalf("expected no UpdateTask call when already assigned, got %d", f.tasks.updateCalls)
	}
	if f.activity.calls != 0 {
		t.Fatalf("expected no activity recorded on a no-op, got %d", f.activity.calls)
	}
}

func TestApplyMatchingRule_NoMatchingRuleIsNoOp(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	task := f.addTask(status.ID, nil) // no rules created for this status at all

	if err := f.svc.ApplyMatchingRule(context.Background(), f.projectID, task, "status_rule", nil); err != nil {
		t.Fatalf("ApplyMatchingRule: %v", err)
	}
	if len(task.AssigneeIDs) != 0 {
		t.Fatalf("expected task to remain unassigned, got %+v", task.AssigneeIDs)
	}
	if f.tasks.updateCalls != 0 {
		t.Fatalf("expected no UpdateTask call, got %d", f.tasks.updateCalls)
	}
}

func TestApplyMatchingRule_DisabledRuleIsSkipped(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	member := f.addMember(f.projectID)
	ctx := context.Background()

	disabled := false
	if _, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "disabled", StatusID: status.ID, AssigneeMemberID: member.ID, Enabled: &disabled,
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	task := f.addTask(status.ID, nil)
	if err := f.svc.ApplyMatchingRule(ctx, f.projectID, task, "status_rule", nil); err != nil {
		t.Fatalf("ApplyMatchingRule: %v", err)
	}
	if len(task.AssigneeIDs) != 0 {
		t.Fatalf("expected a disabled rule to never match, got %+v", task.AssigneeIDs)
	}
}

func TestReorderRules_RejectsMismatchedSet(t *testing.T) {
	f := newFixture()
	status := f.addStatus()
	member := f.addMember(f.projectID)
	ctx := context.Background()

	if _, err := f.svc.CreateRule(ctx, statusruledom.CreateRuleInput{
		ProjectID: f.projectID, Name: "rule", StatusID: status.ID, AssigneeMemberID: member.ID,
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	err := f.svc.ReorderRules(ctx, f.projectID, status.ID, []uuid.UUID{uuid.New()})
	if !errors.Is(err, statusruledom.ErrReorderInvalid) {
		t.Fatalf("expected ErrReorderInvalid, got %v", err)
	}
}
