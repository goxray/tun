package client

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// resolvedDNS redirects host DNS queries to the TUN link via systemd-resolved.
//
// Routing all traffic into the tunnel is not enough: systemd-resolved keeps
// sending queries to the DNS servers of the physical link. Those servers are
// on-link, so their routes outrank the /1 catch-all routes and the queries
// leak outside the VPN. Assigning a DNS server to the TUN link and marking it
// the routing domain for every query ("~.") forces resolved to resolve through
// the tunnel. Both settings are reverted on teardown.
type resolvedDNS struct {
	logger *slog.Logger
	server *net.IP
}

func newDNSManager(logger *slog.Logger, server *net.IP) dnsManager {
	return &resolvedDNS{logger: logger, server: server}
}

// Setup configures the TUN link as the resolver for all host DNS queries.
// On platforms or systems without systemd-resolved it is a no-op with a
// warning — DNS may leak there.
func (d *resolvedDNS) Setup(ifName string) error {
	if runtime.GOOS != "linux" {
		d.logger.Warn("host DNS redirection is only supported on Linux, DNS queries may leak", "tun", ifName)
		return nil
	}
	if !resolvedActive() {
		d.logger.Warn("systemd-resolved not detected, host DNS is not redirected, DNS queries may leak", "tun", ifName)
		return nil
	}

	server := d.server
	if server == nil {
		server = &defaultDNSServer
	}

	if err := d.resolvectl("dns", ifName, server.String()); err != nil {
		return fmt.Errorf("assign DNS server to TUN link: %w", err)
	}
	if err := d.resolvectl("domain", ifName, "~."); err != nil {
		return fmt.Errorf("set TUN link routing domain: %w", err)
	}

	d.logger.Debug("host DNS redirected to TUN link", "tun", ifName, "server", server)
	return nil
}

// Teardown reverts the per-link resolver settings applied by Setup.
func (d *resolvedDNS) Teardown(ifName string) error {
	if runtime.GOOS != "linux" || !resolvedActive() {
		return nil
	}

	return d.resolvectl("revert", ifName)
}

// resolvedActive reports whether systemd-resolved manages the host resolver.
func resolvedActive() bool {
	content, err := os.ReadFile("/etc/resolv.conf")
	if err == nil && strings.Contains(string(content), "127.0.0.53") {
		return true
	}

	_, err = os.Stat("/run/systemd/resolve/stub-resolv.conf")
	return err == nil
}

func (d *resolvedDNS) resolvectl(args ...string) error {
	out, err := exec.Command("resolvectl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("resolvectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	return nil
}
