//go:build linux

package client

import (
	"github.com/vishvananda/netlink"
)

func (c *Client) netlinkMonitor() {
	defer c.debug.wg.Done()

	linkUpdates := make(chan netlink.LinkUpdate)
	addrUpdates := make(chan netlink.AddrUpdate)
	done := make(chan struct{})
	addrDone := make(chan struct{})

	if err := netlink.LinkSubscribeWithOptions(linkUpdates, done, netlink.LinkSubscribeOptions{
		ListExisting: true,
	}); err != nil {
		c.cfg.Logger.Warn("debug.netlink.link_subscribe_failed", "error", err)
		close(done)
		close(addrDone)
		return
	}

	if err := netlink.AddrSubscribe(addrUpdates, addrDone); err != nil {
		c.cfg.Logger.Warn("debug.netlink.addr_subscribe_failed", "error", err)
		close(done)
		close(addrDone)
		return
	}

	for {
		select {
		case <-c.debug.ctx.Done():
			close(done)
			close(addrDone)
			return
		case linkUpdate, ok := <-linkUpdates:
			if !ok {
				linkUpdates = nil
				continue
			}
			attrs := linkUpdate.Attrs()
			if attrs != nil {
				c.cfg.Logger.Info("debug.netlink.link",
					"name", attrs.Name,
					"flags", attrs.Flags,
					"mtu", attrs.MTU,
					"operstate", attrs.OperState.String(),
				)
			}
		case addrUpdate, ok := <-addrUpdates:
			if !ok {
				addrUpdates = nil
				continue
			}
			c.cfg.Logger.Info("debug.netlink.addr",
				"link_index", addrUpdate.LinkIndex,
				"addr", addrUpdate.LinkAddress,
				"new", addrUpdate.NewAddr,
			)
		}
	}
}
