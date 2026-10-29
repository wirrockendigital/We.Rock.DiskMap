package dsmauth

import (
	"context"
	"errors"
	"io"
	"os/user"
	"strings"
	"testing"
	"time"
)

// authenticatedDependencies models successful vendor output and independent NSS identity data.
func authenticatedDependencies() identityDependencies {
	return identityDependencies{
		run: func(_ context.Context, output io.Writer) error {
			_, err := io.WriteString(output, "sample-user\n")
			return err
		},
		lookupUser: func(name string) (*user.User, error) {
			if name != "sample-user" {
				return nil, errors.New("unexpected identity")
			}
			return &user.User{Username: name, Gid: "100"}, nil
		},
		lookupGroup: func(name string) (*user.Group, error) {
			if name != "administrators" {
				return nil, errors.New("unexpected group")
			}
			return &user.Group{Name: name, Gid: "101"}, nil
		},
		groupIDs: func(*user.User) ([]string, error) { return []string{"100", "101"}, nil },
	}
}

// Helper output must not authorize a user when process execution itself failed.
func TestHelperFailures(t *testing.T) {
	cases := []struct {
		name, output string
		failure      bool
	}{
		{"empty", "", false}, {"whitespace", " \n\t", false},
		{"multiple-identities", "sample-user\nother", false},
		{"embedded-carriage-return", "sample\ruser", false},
		{"embedded-null", "sample\x00user", false},
		{"failure-with-valid-output", "sample-user\n", true},
		{"too-large", strings.Repeat("x", 257), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := authenticatedDependencies()
			d.run = func(_ context.Context, output io.Writer) error {
				_, err := io.WriteString(output, tc.output)
				if tc.failure {
					return errors.New("helper exit failure")
				}
				return err
			}
			d.lookupUser = func(string) (*user.User, error) { t.Fatal("failed session reached role lookup"); return nil, nil }
			name, err := identity(context.Background(), d)
			if name != "" || !errors.Is(err, ErrSession) {
				t.Fatalf("got identity=%q error=%v", name, err)
			}
		})
	}
}

// Output limits remain sticky even if an injected or future producer ignores a write error.
func TestBoundedOutput(t *testing.T) {
	var w boundedWriter
	if n, err := w.Write([]byte(strings.Repeat("x", 256))); n != 256 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := w.Write([]byte("x")); n != 0 || !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal(n, err)
	}
	if w.buffer.Len() != 256 {
		t.Fatal("overflow was retained")
	}
	if _, err := w.Write(nil); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal("overflow was not sticky")
	}
	d := authenticatedDependencies()
	d.run = func(_ context.Context, output io.Writer) error {
		_, _ = io.WriteString(output, "sample-user")
		_, _ = io.WriteString(output, strings.Repeat("x", 257))
		return nil
	}
	if name, err := identity(context.Background(), d); name != "" || !errors.Is(err, ErrSession) {
		t.Fatal(name, err)
	}
}

// Roles come from exact GIDs, including the primary group, never account-name substrings.
func TestExactAdministratorGroups(t *testing.T) {
	for _, tc := range []struct {
		name, primary string
		groups        []string
		allowed       bool
	}{
		{"supplemental", "100", []string{"100", "101"}, true},
		{"primary", "101", nil, true},
		{"substring", "100", []string{"1101", "1010"}, false},
		{"group-name-is-not-id", "100", []string{"administrators"}, false},
		{"ordinary-user", "100", []string{"100"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := authenticatedDependencies()
			d.lookupUser = func(name string) (*user.User, error) { return &user.User{Username: name, Gid: tc.primary}, nil }
			d.groupIDs = func(*user.User) ([]string, error) { return tc.groups, nil }
			name, err := identity(context.Background(), d)
			if tc.allowed {
				if name != "sample-user" || err != nil {
					t.Fatal(name, err)
				}
			} else if name != "" || !errors.Is(err, ErrRole) {
				t.Fatal(name, err)
			}
		})
	}
}

// Unavailable NSS data denies access, including a failed supplementary lookup for a primary admin.
func TestLookupFailures(t *testing.T) {
	for _, stage := range []string{"user", "nil-user", "group", "nil-group", "empty-gid", "memberships"} {
		t.Run(stage, func(t *testing.T) {
			d := authenticatedDependencies()
			unavailable := errors.New("NSS unavailable")
			switch stage {
			case "user":
				d.lookupUser = func(string) (*user.User, error) { return nil, unavailable }
			case "nil-user":
				d.lookupUser = func(string) (*user.User, error) { return nil, nil }
			case "group":
				d.lookupGroup = func(string) (*user.Group, error) { return nil, unavailable }
			case "nil-group":
				d.lookupGroup = func(string) (*user.Group, error) { return nil, nil }
			case "empty-gid":
				d.lookupGroup = func(string) (*user.Group, error) { return &user.Group{}, nil }
			case "memberships":
				d.lookupUser = func(string) (*user.User, error) { return &user.User{Gid: "101"}, nil }
				d.groupIDs = func(*user.User) ([]string, error) { return nil, unavailable }
			}
			if name, err := identity(context.Background(), d); name != "" || !errors.Is(err, ErrRole) {
				t.Fatal(name, err)
			}
		})
	}
}

// Parent deadlines cancel the helper; late output cannot resurrect an expired request.
func TestHelperDeadline(t *testing.T) {
	for _, ignoreError := range []bool{false, true} {
		d := authenticatedDependencies()
		d.run = func(ctx context.Context, output io.Writer) error {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 3*time.Second {
				t.Fatal("missing bounded helper deadline")
			}
			<-ctx.Done()
			_, _ = io.WriteString(output, "sample-user")
			if ignoreError {
				return nil
			}
			return ctx.Err()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		name, err := identity(ctx, d)
		cancel()
		if name != "" || !errors.Is(err, ErrSession) {
			t.Fatal(name, err)
		}
	}
}

// Stream copies cannot inherit bytes.Buffer fast paths that bypass the size bound.
func TestStreamOutputBound(t *testing.T) {
	var output boundedWriter
	if _, err := io.Copy(&output, strings.NewReader(strings.Repeat("x", 257))); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal(err)
	}
	if output.buffer.Len() > 256 || !output.overflow {
		t.Fatal("unbounded stream output")
	}
}

// Identity imposes its own deadline even when the caller supplies none.
func TestDefaultDeadlineAndCancelledLookup(t *testing.T) {
	d := authenticatedDependencies()
	d.run = func(ctx context.Context, _ io.Writer) error {
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining > 3*time.Second || remaining < 2*time.Second {
			t.Fatal("unexpected helper deadline", remaining)
		}
		return errors.New("stop without waiting")
	}
	if _, err := identity(context.Background(), d); !errors.Is(err, ErrSession) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d = authenticatedDependencies()
	d.groupIDs = func(*user.User) ([]string, error) { cancel(); return []string{"101"}, nil }
	if name, err := identity(ctx, d); name != "" || !errors.Is(err, ErrSession) {
		t.Fatal(name, err)
	}
}
