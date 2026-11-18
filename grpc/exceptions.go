package grpc

import "errors"

var (
	ErrNotConstructed = errors.New("grpc: server not constructed by NewServer")
	ErrAddrRequired   = errors.New("grpc: Run needs a listen address (Addr / WithAddr)")
	ErrAlreadyStarted = errors.New("grpc: Run already called; a Server cannot be restarted")
	ErrNotRunning     = errors.New("grpc: server is not running")
)
