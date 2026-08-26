package main

import "sync"

var (
	ownerTokenOnce sync.Once
	ownerTokenVal  string
	ownerTokenErr  error
)

// ownerTokenFor mints the tenant-owner JWT exactly once per process, and
// surfaces a minting failure immediately (fail-fast: a broken auth setup is
// infra, not a silent defect).
func ownerTokenFor(o options) (string, error) {
	ownerTokenOnce.Do(func() {
		ownerTokenVal, ownerTokenErr = mintOwnerToken(o.tenantID, o.userID)
	})
	return ownerTokenVal, ownerTokenErr
}
