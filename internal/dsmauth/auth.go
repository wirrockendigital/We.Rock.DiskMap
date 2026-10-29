// Package dsmauth checks DSM sessions using the vendor's local authenticator.
package dsmauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"os/user"
	"strings"
	"time"
)

var ErrSession = errors.New("DSM session is invalid")
var ErrRole = errors.New("DSM administrator role is required")

// boundedWriter rejects unexpected output without retaining arbitrary helper data.
type boundedWriter struct {
	buffer   bytes.Buffer
	overflow bool
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if w.overflow || w.buffer.Len()+len(p) > 256 {
		w.overflow = true
		return 0, io.ErrShortBuffer
	}
	return w.buffer.Write(p)
}

// identityDependencies isolates OS calls without replacing process-global behavior.
type identityDependencies struct {
	run         func(context.Context, io.Writer) error
	lookupUser  func(string) (*user.User, error)
	lookupGroup func(string) (*user.Group, error)
	groupIDs    func(*user.User) ([]string, error)
}

// Identity trusts only the documented authenticator and a fresh local group lookup.
func Identity(ctx context.Context) (string, error) {
	return identity(ctx, identityDependencies{
		run: func(ctx context.Context, output io.Writer) error {
			cmd := exec.CommandContext(ctx, "/usr/syno/synoman/webman/modules/authenticate.cgi")
			cmd.Stdout = output
			return cmd.Run()
		},
		lookupUser:  user.Lookup,
		lookupGroup: user.LookupGroup,
		groupIDs:    (*user.User).GroupIds,
	})
}

// authenticatedName validates vendor output independently from application role policy.
func authenticatedName(ctx context.Context, run func(context.Context, io.Writer) error) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var result boundedWriter
	if err := run(ctx, &result); err != nil || ctx.Err() != nil || result.overflow {
		return "", ErrSession
	}
	name := strings.TrimSpace(result.buffer.String())
	if name == "" || strings.ContainsAny(name, "\r\n\x00") {
		return "", ErrSession
	}
	return name, nil
}

// identity requires a fresh administrator membership check for every guarded request.
func identity(ctx context.Context, dependencies identityDependencies) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	name, err := authenticatedName(ctx, dependencies.run)
	if err != nil {
		return "", err
	}
	account, err := dependencies.lookupUser(name)
	if err != nil || account == nil {
		return "", ErrRole
	}
	admin, err := dependencies.lookupGroup("administrators")
	if err != nil || admin == nil || admin.Gid == "" {
		return "", ErrRole
	}
	groups, err := dependencies.groupIDs(account)
	if err != nil {
		return "", ErrRole
	}
	for _, group := range append(groups, account.Gid) {
		if group == admin.Gid {
			if ctx.Err() != nil {
				return "", ErrSession
			}
			return name, nil
		}
	}
	return "", ErrRole
}
