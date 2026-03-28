package domain

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrors_AreDistinct(t *testing.T) {
	errs := []error{ErrNotFound, ErrNotConfigured, ErrInvalidInput, ErrUnauthorized, ErrConflict}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Errorf("sentinel errors %d and %d should be distinct", i, j)
			}
		}
	}
}

func TestSentinelErrors_Wrapping(t *testing.T) {
	wrapped := fmt.Errorf("sqlite: getting user: %w", ErrNotFound)

	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("wrapped error should match ErrNotFound via errors.Is")
	}
	if errors.Is(wrapped, ErrUnauthorized) {
		t.Error("wrapped error should not match ErrUnauthorized")
	}
}

func TestSentinelErrors_DoubleWrapping(t *testing.T) {
	inner := fmt.Errorf("repo: %w", ErrNotFound)
	outer := fmt.Errorf("service: %w", inner)

	if !errors.Is(outer, ErrNotFound) {
		t.Error("double-wrapped error should still match ErrNotFound")
	}
}
