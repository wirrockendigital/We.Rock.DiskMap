package service

import (
	"context"
	"net"
	"os"
)

type peerKey struct{}

// peerContext obtains kernel credentials, never an HTTP-supplied identity header.
func peerContext(ctx context.Context, connection net.Conn) context.Context {
	return peerContextFor(uint32(os.Geteuid()))(ctx, connection)
}

// peerContextFor validates the package socket caller using kernel credentials.
func peerContextFor(expected uint32) func(context.Context, net.Conn) context.Context {
	return func(ctx context.Context, connection net.Conn) context.Context {
		unixConnection, ok := connection.(*net.UnixConn)
		if !ok {
			return ctx
		}
		raw, err := unixConnection.SyscallConn()
		if err != nil {
			return ctx
		}
		var uid uint32
		var lookupErr error
		if err = raw.Control(func(fd uintptr) { uid, lookupErr = socketUID(int(fd)) }); err != nil || lookupErr != nil {
			return ctx
		}
		// Root may invoke the package CLI but cannot change the service's scan identity.
		return context.WithValue(ctx, peerKey{}, uid == expected || (expected == uint32(os.Geteuid()) && uid == 0))
	}
}
