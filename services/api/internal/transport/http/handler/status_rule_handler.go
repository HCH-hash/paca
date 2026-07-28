package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Paca-AI/api/internal/apierr"
	statusruledom "github.com/Paca-AI/api/internal/domain/statusrule"
	"github.com/Paca-AI/api/internal/transport/http/dto"
	"github.com/Paca-AI/api/internal/transport/http/middleware"
	"github.com/Paca-AI/api/internal/transport/http/presenter"
)

// StatusRuleHandler handles project-wide status-assignment-rule management
// endpoints.
type StatusRuleHandler struct {
	svc statusruledom.Service
}

// NewStatusRuleHandler returns a StatusRuleHandler wired to the
// status-assignment-rule service.
func NewStatusRuleHandler(svc statusruledom.Service) *StatusRuleHandler {
	return &StatusRuleHandler{svc: svc}
}

// ListRules handles GET /projects/:projectId/status-assignment-rules.
func (h *StatusRuleHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	rules, err := h.svc.ListRules(r.Context(), projectID)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	resp := make([]dto.StatusAssignmentRuleResponse, 0, len(rules))
	for _, rule := range rules {
		resp = append(resp, dto.StatusAssignmentRuleFromEntity(rule))
	}
	presenter.OK(w, r, map[string]any{"items": resp})
}

// CreateRule handles POST /projects/:projectId/status-assignment-rules.
func (h *StatusRuleHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.CreateStatusAssignmentRuleRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}
	if req.StatusID == uuid.Nil || req.AssigneeMemberID == uuid.Nil {
		presenter.Error(w, r, apierr.New(apierr.CodeBadRequest, "status_id and assignee_member_id are required"))
		return
	}
	if err := validateImportanceRanges(req.Filter.ImportanceRanges); err != nil {
		presenter.Error(w, r, err)
		return
	}

	var createdBy *uuid.UUID
	if actorID, ok := middleware.ActorIDFromContext(r.Context()); ok && actorID != uuid.Nil {
		createdBy = &actorID
	}
	var agentID *uuid.UUID
	if id, ok := middleware.AgentIDFromContext(r.Context()); ok && id != uuid.Nil {
		agentID = &id
	}

	rule, err := h.svc.CreateRule(r.Context(), statusruledom.CreateRuleInput{
		ProjectID:        projectID,
		Name:             req.Name,
		StatusID:         req.StatusID,
		AssigneeMemberID: req.AssigneeMemberID,
		Filter:           req.Filter.ToDomain(),
		Enabled:          req.Enabled,
		CreatedBy:        createdBy,
		AgentID:          agentID,
	})
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.Created(w, r, dto.StatusAssignmentRuleFromEntity(rule))
}

// UpdateRule handles PATCH /projects/:projectId/status-assignment-rules/:ruleId.
func (h *StatusRuleHandler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	ruleID, err := parseStatusRuleID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.UpdateStatusAssignmentRuleRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}
	var filter *statusruledom.TaskFilterSpec
	if req.Filter != nil {
		if err := validateImportanceRanges(req.Filter.ImportanceRanges); err != nil {
			presenter.Error(w, r, err)
			return
		}
		f := req.Filter.ToDomain()
		filter = &f
	}

	rule, err := h.svc.UpdateRule(r.Context(), projectID, ruleID, statusruledom.UpdateRuleInput{
		Name:             req.Name,
		AssigneeMemberID: req.AssigneeMemberID,
		Filter:           filter,
		Enabled:          req.Enabled,
	})
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.OK(w, r, dto.StatusAssignmentRuleFromEntity(rule))
}

// DeleteRule handles DELETE /projects/:projectId/status-assignment-rules/:ruleId.
func (h *StatusRuleHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	ruleID, err := parseStatusRuleID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}
	if err := h.svc.DeleteRule(r.Context(), projectID, ruleID); err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.NoContent(w)
}

// ReorderRules handles PUT /projects/:projectId/status-assignment-rules/positions.
func (h *StatusRuleHandler) ReorderRules(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		presenter.Error(w, r, err)
		return
	}

	var req dto.ReorderStatusAssignmentRulesRequest
	if !middleware.BindJSON(w, r, &req) {
		return
	}
	if req.StatusID == uuid.Nil {
		presenter.Error(w, r, apierr.New(apierr.CodeBadRequest, "status_id is required"))
		return
	}

	if err := h.svc.ReorderRules(r.Context(), projectID, req.StatusID, req.RuleIDs); err != nil {
		presenter.Error(w, r, err)
		return
	}
	presenter.NoContent(w)
}

func parseStatusRuleID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "ruleId"))
	if err != nil {
		return uuid.Nil, apierr.New(apierr.CodeBadRequest, "invalid status assignment rule id")
	}
	return id, nil
}

// validateImportanceRanges rejects a filter whose importance ranges have
// min > max, mirroring parseImportanceRanges' validation for ListTasks.
func validateImportanceRanges(ranges []dto.IntRangeDTO) error {
	for _, rg := range ranges {
		if rg.Min > rg.Max {
			return apierr.New(apierr.CodeBadRequest, "invalid importance_ranges: min > max")
		}
	}
	return nil
}
