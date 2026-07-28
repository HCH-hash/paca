package statusruledom

import "errors"

// Sentinel errors for the status-assignment-rule aggregate.
var (
	ErrNotFound                 = errors.New("status assignment rule: not found")
	ErrNameInvalid              = errors.New("status assignment rule: name is required")
	ErrCrossProject             = errors.New("status assignment rule: status or member does not belong to this project")
	ErrFilterUnknownCustomField = errors.New("status assignment rule: filter references a custom field that does not exist in this project")
	ErrReorderInvalid           = errors.New("status assignment rule: provided rule IDs do not match this status's existing rules")
)
