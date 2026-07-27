package application

const (
	minProposalMCPTimeoutSec = 1
	maxProposalMCPTimeoutSec = 300

	minProposalMCPRetryCount = 1
	maxProposalMCPRetryCount = 20

	minProposalMCPRetryInitialDelayMs int64 = 100
	maxProposalMCPRetryInitialDelayMs int64 = 60000
	minProposalMCPRetryMaxDelayMs     int64 = 1000
	maxProposalMCPRetryMaxDelayMs     int64 = 300000

	minProposalMCPRetryBackoffFactor = 1.0
	maxProposalMCPRetryBackoffFactor = 10.0
)
