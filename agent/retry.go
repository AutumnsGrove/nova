package agent

import (
	"math/rand"
	"time"

	"github.com/AutumnsGrove/nova/provider"
)

// constants for retry defaults and the upper bound on a Retry-After delay.
const (
	defaultRetryMax = 3
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
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
		if d >= capd {
			d = capd
			break
		}
	}
	if d > capd {
		d = capd
	}

	jitter := policy.Jitter
	if policy == (RetryPolicy{}) {
		jitter = 0.2
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
