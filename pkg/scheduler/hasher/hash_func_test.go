package hasher

import (
	"testing"
)

func TestGetHashStr_RangeChecks(t *testing.T) {
	// Negative => Python would raise OverflowError; Go should return error.
	if _, err := HashBlockSha256Hex([]int64{-1}, ""); err == nil {
		t.Fatalf("expected error for negative token")
	}

	// > uint32 max
	if _, err := HashBlockSha256Hex([]int64{4294967296}, ""); err == nil {
		t.Fatalf("expected error for >uint32 token")
	}

	// prior hash malformed
	if _, err := HashBlockSha256Hex([]int64{1}, "zz"); err == nil {
		t.Fatalf("expected error for invalid prior hash hex")
	}
}
