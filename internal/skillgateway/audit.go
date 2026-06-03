// Package skillgateway provides skill gateway and routing.
package skillgateway

import (
	"time"

	"go.uber.org/zap"
)

type auditor struct {
	logger *zap.Logger
}

func newAuditor(logger *zap.Logger) *auditor {
	return &auditor{logger: logger.Named("skill_audit")}
}

func (a *auditor) log(traceID, skillID, caller, status string, duration time.Duration) {
	a.logger.Info("skill execution audit",
		zap.String("trace_id", traceID),
		zap.String("skill_id", skillID),
		zap.String("caller", caller),
		zap.String("status", status),
		zap.Duration("duration", duration),
	)
}
