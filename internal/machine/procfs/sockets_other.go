//go:build !linux

package procfs

// UnixSockets has no kernel table to read outside Linux: sock_diag is Linux's
// own. Callers fall back on zellij's list-clients and on the socket files.
func UnixSockets() ([]UnixSocket, error) { return nil, ErrNoSockDiag }
