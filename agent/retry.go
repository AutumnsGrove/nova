package agent

import (
	"math/rand/v2"
	"time"

	"github.com/AutumnsGrove/nova/provider"
)

// Retry defaults, and the upper bound on any delay including a server's
// Retry-After, so a hostile header cannot park a run for hours.
const (
	defaultRetryMax = 3
	defaultJitter   = 0.2
	maxRetryAfter   = 5 * time.Minute
)

// retryDelay computes the wait before retrying a retryable provider error:
// min(Cap, Base*2^attempt) plus up to Jitter, never below an explicit
// Retry-After, and never above five minutes.
func retryDelay(policy RetryPolicy, pe *provider.Error, attempt int) time.Duration {
	base := policy.Base
	if base <= 0 {
		base = time.Second
	}
	capd := policy.Cap
	if capd <= 0 {
		capd = 30 * time.Second
	}
	// Doubling stops at the cap, so a large attempt count cannot overflow.
	d := base
	for i := 0; i < attempt && d < capd; i++ {
		d *= 2
	}
	d = min(d, capd)

	// Zero means "unset", so a caller that fills in other fields still gets
	// jitter; only a negative value turns it off.
	jitter := policy.Jitter
	if jitter == 0 {
		jitter = defaultJitter
	}
	if jitter > 0 {
		d += time.Duration(jitter * float64(d) * rand.Float64())
	}

	if pe != nil && pe.RetryAfter > d {
		d = pe.RetryAfter
	}
	if d > maxRetryAfter {
		d = maxRetryAfter
	}
	return d
}
