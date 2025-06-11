package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goxray/core/network/route"
	"github.com/goxray/core/network/tun"
	"github.com/goxray/core/pipe2socks"
	"github.com/jackpal/gateway"

	xrayproto "github.com/lilendian0x00/xray-knife/v3/pkg/protocol"
	"github.com/lilendian0x00/xray-knife/v3/pkg/xray"
	xapplog "github.com/xtls/xray-core/app/log"
	xcommlog "github.com/xtls/xray-core/common/log"
)

const disconnectTimeout = 30 * time.Second

var (
	// defaultTUNAddress is the address new TUN device will be set up with.
	defaultTUNAddress = &net.IPNet{IP: net.IPv4(192, 18, 0, 1), Mask: net.IPv4Mask(255, 255, 255, 255)}
	// defaultInboundProxy default proxy will be set up for listening on 127.0.0.1.
	defaultInboundProxy = &Proxy{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: getFreePort(),
	}

	// DefaultRoutesToTUN will route all system traffic through the TUN.
	DefaultRoutesToTUN = []*route.Addr{
		// Reroute all traffic.
		route.MustParseAddr("0.0.0.0/1"),
		route.MustParseAddr("128.0.0.0/1"),
	}
)

// Config serves configuration for new Client. Empty fields will be set up with defaults values.
//
// It is advised to not configure the cl yourself, please use NewClient() with default config values,
// normally you don't have to set these fields yourself.
type Config struct {
	// GatewayIP to direct outbound traffic. Must be able to reach remote XRay server.
	// (default: will be dynamically detected from your default gateway).
	//
	// Client will determine the system gateway IP automatically,
	// and you don't have to set this field explicitly.
	GatewayIP *net.IP
	// Socks proxy address on which XRay creates inbound proxy (default: 127.0.0.1:10808).
	InboundProxy *Proxy
	// TUN device address (default: 192.18.0.1).
	TUNAddress *net.IPNet
	// List of routes to be pointed to TUN device (default: DefaultRoutesToTUN).
	//
	// One exception is explicitly added for XRay remote server IP and can not be altered.
	RoutesToTUN []*route.Addr
	// Whether to allow self-signed certificates or not.
	TLSAllowInsecure bool
	// Pass logger with debug level to observe debug logs (default: slog.TextHandler).
	Logger *slog.Logger
	// XRayLogType is used to redefine xray core log type (default: LogType_None).
	XRayLogType xapplog.LogType
}

func (c *Config) apply(new *Config) {
	if new.GatewayIP != nil {
		c.GatewayIP = new.GatewayIP
	}
	if new.InboundProxy != nil {
		c.InboundProxy = new.InboundProxy
	}
	if new.TUNAddress != nil {
		c.TUNAddress = new.TUNAddress
	}
	if new.Logger != nil {
		c.Logger = new.Logger
	}
	if new.RoutesToTUN != nil {
		c.RoutesToTUN = new.RoutesToTUN
	}
	if new.XRayLogType != xapplog.LogType_None {
		c.XRayLogType = new.XRayLogType
	}
}

// Client is the actual VPN cl. It manages connections, routing and tunneling of the requests.
// It is safe to make a Client connection as it does not change the default system routing and
// just adds on existing infrastructure.
type Client struct {
	cfg Config

	xInst  runnable
	xCfg   *xrayproto.GeneralConfig
	xSrvIP *net.IPAddr
	tunnel io.ReadWriteCloser
	pipe   pipe
	routes ipTable

	tunnelStopped chan error
	stopTunnel    func()
}

// Proxy will set up XRay inbound.
type Proxy struct {
	IP   net.IP // Inbound proxy IP (e.g. 127.0.0.1)
	Port int    // Inbound proxy port (e.g. 1080)
}

func (p *Proxy) String() string {
	return fmt.Sprintf("%s:%d", p.IP, p.Port)
}

// NewClient initializes default Client with default proxy address.
// If you want more options use Client struct.
func NewClient() (*Client, error) {
	gatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return nil, fmt.Errorf("discover gateway: %w", err)
	}

	p, err := pipe2socks.NewPipe(pipe2socks.DefaultOpts)
	if err != nil {
		return nil, fmt.Errorf("tun2socks new pipe: %w", err)
	}

	r, err := route.New()
	if err != nil {
		return nil, fmt.Errorf("route new: %w", err)
	}

	return &Client{
		cfg: Config{
			GatewayIP:    &gatewayIP,
			InboundProxy: defaultInboundProxy,
			TUNAddress:   defaultTUNAddress,
			RoutesToTUN:  DefaultRoutesToTUN,
			Logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
		},
		tunnelStopped: make(chan error),
		pipe:          p,
		routes:        r,
	}, nil
}

// NewClientWithOpts initializes Client with specified Config. It is recommended to just use NewClient().
func NewClientWithOpts(cfg Config) (*Client, error) {
	client, err := NewClient()
	if err != nil {
		return nil, err
	}

	client.cfg.apply(&cfg)

	return client, nil
}

// GatewayIP returns gateway IP used to route outbound traffic through.
// It is used to route packets destined to XRay remote server.
func (c *Client) GatewayIP() net.IP {
	return *c.cfg.GatewayIP
}

// TUNAddress returns address the TUN device is set up on.
// Traffic is routed to this TUN device.
func (c *Client) TUNAddress() net.IP {
	return c.cfg.TUNAddress.IP
}

// InboundProxy returns proxy address initialized by XRay core.
// Traffic from TUN device is routed to this proxy.
func (c *Client) InboundProxy() Proxy {
	return *c.cfg.InboundProxy
}

// Connect creates a global tunnel and routes all incoming connections (or traffic specified in Config.RoutesToTUN)
// to the VPN server via newly created defaultInboundProxy.
func (c *Client) Connect(link string) error {
	var err error
	c.cfg.Logger.Debug("Connecting to tunnel", "cfg", c.cfg)

	c.xInst, c.xCfg, err = c.createXrayProxy(link)
	if err != nil {
		c.cfg.Logger.Error("xray core creation failed", "err", err, "xray_config", c.xCfg)

		return fmt.Errorf("create xray core instance: %w", err)
	}
	c.cfg.Logger.Debug("xray core instance created", "xray_config", c.xCfg)

	c.cfg.Logger.Debug("starting xray core instance")
	if err = c.xInst.Start(); err != nil {
		c.cfg.Logger.Error("xray core instance startup failed", "err", err)

		return fmt.Errorf("start xray core instance: %w", err)
	}
	time.Sleep(100 * time.Millisecond) // Sometimes XRay instance should have a bit more time to set up.
	c.cfg.Logger.Debug("xray core instance started")

	c.cfg.Logger.Debug("Setting up TUN device")
	// Create TUN and route all traffic to it.
	c.tunnel, err = c.setupTunnel()
	if err != nil {
		c.cfg.Logger.Error("TUN creation failed", "err", err)

		return fmt.Errorf("setup TUN device: %w", err)
	}
	c.tunnel = newReaderMetrics(c.tunnel)
	c.cfg.Logger.Debug("TUN device created")

	c.cfg.Logger.Debug("adding routes for TUN device")
	// Set XRay remote address to be routed through the default gateway, so that we don't get a loop.
	_ = c.routes.Delete(c.xrayToGatewayRoute()) // In case previous run failed.
	c.cfg.Logger.Debug("deleted dangling routes")
	err = c.routes.Add(c.xrayToGatewayRoute())
	if err != nil {
		c.cfg.Logger.Error("routing xray server IP to default route failed", "err", err, "route", c.xrayToGatewayRoute())

		return fmt.Errorf("add xray server route exception: %w", err)
	}
	c.cfg.Logger.Debug("routing xray server IP to default route")

	var wg sync.WaitGroup
	wg.Add(1)
	var ctx context.Context
	ctx, c.stopTunnel = context.WithCancel(context.Background())
	go func() {
		wg.Done()
		c.tunnelStopped <- c.pipe.Copy(ctx, c.tunnel, c.cfg.InboundProxy.String())
		c.cfg.Logger.Debug("tunnel pipe closed", "err", err)
	}()
	wg.Wait()
	c.cfg.Logger.Debug("client connected")

	return nil
}

// Disconnect stops all listeners and cleans up route for XRay server.
//
// It will block till all resources are done processing or
// context is cancelled (method also enforces timeout of disconnectTimeout)
func (c *Client) Disconnect(ctx context.Context) error {
	if c.stopTunnel == nil {
		return nil // not connected
	}

	c.stopTunnel()
	err := errors.Join(c.xInst.Close(), c.tunnel.Close(), c.routes.Delete(c.xrayToGatewayRoute()))

	// Waiting till the tunnel actually done with processing connections.
	ctx, cancel := context.WithTimeout(ctx, disconnectTimeout)
	defer cancel()
	select {
	case tunErr := <-c.tunnelStopped:
		err = errors.Join(tunErr, err)
	case <-ctx.Done():
		err = errors.Join(ctx.Err(), err)
	}

	if err != nil {
		c.cfg.Logger.Error("client disconnect encountered failures", "err", err)

		return err
	}

	c.cfg.Logger.Debug("client disconnected")

	return nil
}

// BytesRead returns number of bytes read from TUN device.
func (c *Client) BytesRead() int {
	if c.tunnel == nil {
		return 0
	}

	return c.tunnel.(*readerMetrics).BytesRead()
}

// BytesWritten returns number of bytes written to TUN device.
func (c *Client) BytesWritten() int {
	if c.tunnel == nil {
		return 0
	}

	return c.tunnel.(*readerMetrics).BytesWritten()
}

// xrayToGatewayRoute is a setup to route VPN requests to gateway.
// Used as exception to not interfere with traffic going to remote XRay instance.
func (c *Client) xrayToGatewayRoute() route.Opts {
	// Append "/32" to match only the XRay server route.
	return route.Opts{Gateway: *c.cfg.GatewayIP, Routes: []*route.Addr{route.MustParseAddr(c.xSrvIP.String() + "/32")}}
}

// createXrayProxy creates XRay instance from connection link with additional proxy listening on {addr}:{port}.
func (c *Client) createXrayProxy(link string) (xrayproto.Instance, *xrayproto.GeneralConfig, error) {
	// Make the inbound for local proxy.
	// We will later use it to redirect all traffic from TUN device to this proxy.
	inbound := &xray.Socks{
		Remark:  "GoXRay-TUN-Listener",
		Address: c.cfg.InboundProxy.IP.String(),
		Port:    strconv.Itoa(c.cfg.InboundProxy.Port),
	}

	svc := xray.NewXrayService(true,
		c.cfg.TLSAllowInsecure,
		xray.WithCustomLogLevel(c.cfg.XRayLogType, xRayLogLevel(c.cfg.Logger.Handler())),
		xray.WithInbound(inbound),
	)

	link = strings.TrimSpace(link)
	protocol, err := svc.CreateProtocol(link)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid config: protocol create: %w", err)
	}

	if err := protocol.Parse(); err != nil {
		return nil, nil, fmt.Errorf("invalid config: parse: %w", err)
	}

	cfg := protocol.ConvertToGeneralConfig()

	inst, err := svc.MakeInstance(protocol)
	if err != nil {
		return nil, nil, fmt.Errorf("make instance: %w", err)
	}

	// Validate xray proto addr.
	ip, err := net.ResolveIPAddr("ip", cfg.Address)
	if err != nil {
		return nil, nil, fmt.Errorf("xray address not resolvable: %w", err)
	}
	c.xSrvIP = ip

	return inst, &cfg, nil
}

// xRayLogLevel maps slog.Level to xray core log level (xcommlog.Severity) by checking Config.Logger level.
func xRayLogLevel(h slog.Handler) xcommlog.Severity {
	ctx := context.Background()
	switch {
	case h.Enabled(ctx, slog.LevelDebug):
		return xcommlog.Severity_Debug
	case h.Enabled(ctx, slog.LevelInfo):
		return xcommlog.Severity_Info
	case h.Enabled(ctx, slog.LevelError):
		return xcommlog.Severity_Error
	case h.Enabled(ctx, slog.LevelWarn):
		return xcommlog.Severity_Warning
	}

	return xcommlog.Severity_Unknown
}

// setupTunnel creates new TUN interface in the system and routes all traffic to it.
func (c *Client) setupTunnel() (*tun.Interface, error) {
	ifc, err := tun.New("", 1500)
	if err != nil {
		return nil, fmt.Errorf("create tun: %w", err)
	}

	if err = ifc.Up(c.cfg.TUNAddress, c.cfg.TUNAddress.IP); err != nil {
		return nil, fmt.Errorf("setup interface: %w", err)
	}

	if err = c.routes.Add(route.Opts{IfName: ifc.Name(), Routes: c.cfg.RoutesToTUN}); err != nil {
		return nil, fmt.Errorf("add route: %w", err)
	}

	return ifc, nil
}

func getFreePort() int {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 10808
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	port := addr.Port

	return port
}
