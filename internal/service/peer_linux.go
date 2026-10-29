package service

import "golang.org/x/sys/unix"

// socketUID reads the effective UID recorded by the Linux kernel at connection time.
func socketUID(fd int) (uint32, error) {
	credentials, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return credentials.Uid, nil
}
