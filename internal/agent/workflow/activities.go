package workflow

import (
	"context"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
)

const ExecuteCapabilityActivityName = "ExecuteCapabilityActivity"

// ActivityDeps holds dependencies injected via closure capture (Temporal Go SDK pattern).
type ActivityDeps struct {
	CapGateway capgateway.CapabilityGateway
}

func (d *ActivityDeps) ExecuteCapabilityActivity(ctx context.Context, req capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	return d.CapGateway.Route(ctx, req)
}
