package jobs

import (
	"context"
	"errors"
	"testing"
)

// Invalid limits are a stable input error even when native inventory is unavailable.
func TestResourceUpdateRejectsBeforeCatalog(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	calls := 0
	m.catalog = func(context.Context, profileSources) (profileSources, error) {
		calls++
		return profileSources{}, errors.New("catalog unavailable")
	}
	bad := in.Profile
	bad.Limits.Workers = 2
	if _, err := m.UpdateProfile(context.Background(), bad.ID, bad.Revision, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("unsupported input was not rejected", err)
	}
	if calls != 0 {
		t.Fatal("invalid policy accessed inventory", calls)
	}
}
