package workflowdom

import "errors"

// Sentinel errors for the workflow aggregate.
var (
	ErrNotFound                       = errors.New("workflow: not found")
	ErrNameInvalid                    = errors.New("workflow: name is required")
	ErrNodeNotFound                   = errors.New("workflow: node not found")
	ErrNodeDuplicateTask              = errors.New("workflow: task is already a node in this workflow")
	ErrNodeTaskCrossProject           = errors.New("workflow: task does not belong to this workflow's project")
	ErrStatusTransitionNotFound       = errors.New("workflow: status transition not found")
	ErrStatusTransitionCrossProject   = errors.New("workflow: status does not belong to this workflow's project")
	ErrStatusTransitionSelfLoop       = errors.New("workflow: a status cannot transition to itself")
	ErrStatusTransitionConflict       = errors.New("workflow: a status transition for this status was just created concurrently")
	ErrEdgeNotFound                   = errors.New("workflow: edge not found")
	ErrEdgeSelfLoop                   = errors.New("workflow: a node cannot link to itself")
	ErrEdgeCrossWorkflow              = errors.New("workflow: source and target nodes must belong to the same workflow")
	ErrEdgeCycle                      = errors.New("workflow: this edge would create a cycle")
	ErrEdgeDuplicate                  = errors.New("workflow: this edge already exists")
	ErrNotDraft                       = errors.New("workflow: can only be activated while in draft")
	ErrNotActive                      = errors.New("workflow: is not active")
	ErrArchived                       = errors.New("workflow: cannot be edited once archived")
	ErrActivateNoNodes                = errors.New("workflow: cannot activate an empty workflow")
	ErrActivateDoneStatusUndetermined = errors.New("workflow: exactly one status must have no configured next status (the done status) before this workflow can be activated")
	ErrActivateTaskMissing            = errors.New("workflow: a node references a task that no longer exists in this project")
)
