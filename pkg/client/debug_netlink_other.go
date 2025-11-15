//go:build !linux

package client

func (c *Client) netlinkMonitor() {
	defer c.debug.wg.Done()
	c.cfg.Logger.Debug("debug.netlink.disabled", "reason", "unsupported platform")
}
