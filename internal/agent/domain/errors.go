package domain

import "errors"

// Sentinel errors returned by repositories. Application aliases these so
// callers can preserve errors.Is checks across layers.
var (
	ErrNotFound               = errors.New("agent not found")
	ErrNameConflict           = errors.New("agent name already exists")
	ErrInvalidSkill           = errors.New("skill not found")
	ErrSystemAssistantManaged = errors.New("system assistant is platform managed")
)
