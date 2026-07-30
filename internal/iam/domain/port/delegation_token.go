package port

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

type InvocationTokenService interface {
	SignInvocation(platformmcp.InvocationClaims, time.Duration) (string, error)
	VerifyInvocation(string) (*platformmcp.InvocationClaims, error)
}

type APIDelegationTokenService interface {
	SignAPIDelegation(platformmcp.APIDelegationClaims, time.Duration) (string, error)
	VerifyAPIDelegation(string) (*platformmcp.APIDelegationClaims, error)
}

type DelegationTokenService interface {
	InvocationTokenService
	APIDelegationTokenService
}

type MCPTokenExchangeTokenService interface {
	VerifyInvocation(string) (*platformmcp.InvocationClaims, error)
	SignAPIDelegation(platformmcp.APIDelegationClaims, time.Duration) (string, error)
}
