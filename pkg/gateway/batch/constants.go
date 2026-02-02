package batch

import (
	"time"
)

const (
	// DefaultLockExpiration is the default expiration time for task and file locks
	DefaultLockExpiration = 5 * time.Minute

	// DefaultLockRenewalInterval is the default interval for renewing locks
	DefaultLockRenewalInterval = 2 * time.Minute // Renew lock every 2 minutes (half of 5-minute expiration)

	// DefaultStatusCheckInterval is the default interval for checking task status
	DefaultStatusCheckInterval = 10 * time.Second
)
