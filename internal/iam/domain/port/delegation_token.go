package port

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

type DelegationTokenService interface {
	SignInvocation(platformmcp.InvocationClaims, time.Duration) (string, error)
	VerifyInvocation(string) (*platformmcp.InvocationClaims, error)
	SignAPIDelegation(platformmcp.APIDelegationClaims, time.Duration) (string, error)
	VerifyAPIDelegation(string) (*platformmcp.APIDelegationClaims, error)
}
