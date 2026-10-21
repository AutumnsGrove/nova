package opencode

import (
	"fmt"
	"testing"
)

// TestDescribeUsageError_Wrapped checks a classified error is still recognised
// after a caller wraps it with %w.
func TestDescribeUsageError_Wrapped(t *testing.T) {
	err := fmt.Errorf("footer refresh: %w", &UsageError{Kind: ErrNoCredentials, Message: "raw"})
	want := "Not signed in. Run `nova login opencode-go`."
	if got := DescribeUsageError(err); got != want {
		t.Fatalf("DescribeUsageError = %q, want %q", got, want)
	}
}
