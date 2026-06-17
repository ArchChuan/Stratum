package domain

import "errors"

// ErrConcurrencyLimit is returned when a tenant or global execution cap is reached.
var ErrConcurrencyLimit = errors.New("concurrency limit reached")
