//go:generate mockgen -destination=mocks/client_mocks.go -source=interfaces.go -package=mocks -typed

package client

import (
	"context"
	"io"

	"github.com/goxray/core/network/route"
	xcommon "github.com/xtls/xray-core/common"
)

type pipe interface {
	Copy(ctx context.Context, pipe io.ReadWriteCloser, socks5 string) error
}

type ipTable interface {
	// Add adds route to ip table.
	Add(options route.Opts) error
	// Delete deletes route from ip table.
	Delete(options route.Opts) error
}

type runnable interface {
	xcommon.Runnable
}

type dnsManager interface {
	// Setup redirects host DNS queries for the TUN link so they are resolved
	// through the VPN instead of the physical link.
	Setup(ifName string) error
	// Teardown reverts host DNS redirection applied by Setup.
	Teardown(ifName string) error
}

//nolint:unused
type ioReadWriteCloser interface {
	io.ReadWriteCloser
}
