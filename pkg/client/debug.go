package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"

	"github.com/jackpal/gateway"
	"github.com/shirou/gopsutil/v4/process"
)

// DebugOptions define instrumentation behavior for a Client.
type DebugOptions struct {
	// OutputDir defines a base directory where debug dumps are stored.
	OutputDir string
	// ResourceInterval controls how often runtime/gopsutil stats are logged.
	ResourceInterval time.Duration
	// GatewayInterval controls how often gateway IP is validated.
	GatewayInterval time.Duration
	// EnableNetlink toggles linux netlink monitoring.
	EnableNetlink bool
	// VerbosePipe enables verbose pipe copy logging.
	VerbosePipe bool
	// ProfileInterval controls how often static pprof snapshots are captured.
	ProfileInterval time.Duration
	// CPUProfileDuration controls duration of each CPU profile capture.
	CPUProfileDuration time.Duration
	// CollectCPUProfile toggles periodic CPU profile captures.
	CollectCPUProfile bool
}

type debugState struct {
	enabled bool

	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup

	proc *process.Process

	outputDir string

	cpuProfileMu sync.Mutex
}

func defaultDebugOptions() DebugOptions {
	return DebugOptions{
		OutputDir:          "debug-output",
		ResourceInterval:   5 * time.Second,
		GatewayInterval:    3 * time.Second,
		ProfileInterval:    30 * time.Second,
		CPUProfileDuration: 30 * time.Second,
		EnableNetlink:      true,
		VerbosePipe:        true,
		CollectCPUProfile:  true,
	}
}

func (o DebugOptions) normalized() DebugOptions {
	if o.OutputDir == "" {
		o.OutputDir = "debug-output"
	}
	if o.ResourceInterval <= 0 {
		o.ResourceInterval = 5 * time.Second
	}
	if o.GatewayInterval <= 0 {
		o.GatewayInterval = 3 * time.Second
	}
	if o.ProfileInterval <= 0 {
		o.ProfileInterval = 30 * time.Second
	}
	if o.CPUProfileDuration <= 0 {
		o.CPUProfileDuration = 30 * time.Second
	}

	return o
}

func (c *Client) syncDebugState() {
	c.cfg.DebugOptions = c.cfg.DebugOptions.normalized()
	c.debug.enabled = c.cfg.Debug
}

func (c *Client) startDebugging() {
	if !c.debug.enabled || c.debug.cancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.debug.ctx = ctx
	c.debug.cancel = cancel

	c.prepareDebugOutputDir()

	if proc, err := process.NewProcess(int32(os.Getpid())); err != nil {
		c.cfg.Logger.Warn("debug resource monitor init failed", "error", err)
	} else {
		c.debug.proc = proc
	}

	c.debug.wg.Add(1)
	go c.resourceMonitor()

	c.debug.wg.Add(1)
	go c.gatewayMonitor()

	c.debug.wg.Add(1)
	go c.profileDumper()

	if c.cfg.DebugOptions.CollectCPUProfile {
		c.debug.wg.Add(1)
		go c.cpuProfileLoop()
	}

	if runtime.GOOS == "linux" && c.cfg.DebugOptions.EnableNetlink {
		c.debug.wg.Add(1)
		go c.netlinkMonitor()
	}
}

func (c *Client) stopDebugging() {
	if c.debug.cancel == nil {
		return
	}
	c.debug.cancel()
	c.debug.wg.Wait()
	c.debug.cancel = nil
	c.debug.ctx = nil
	c.debug.proc = nil
	c.debug.outputDir = ""
}

func (c *Client) resourceMonitor() {
	defer c.debug.wg.Done()

	ticker := time.NewTicker(c.cfg.DebugOptions.ResourceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.debug.ctx.Done():
			return
		case <-ticker.C:
			c.logResourceSnapshot()
		}
	}
}

func (c *Client) logResourceSnapshot() {
	numGoroutines := runtime.NumGoroutine()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	cpuPercent := 0.0
	var rss uint64

	if c.debug.proc != nil {
		if cpu, err := c.debug.proc.Percent(0); err == nil {
			cpuPercent = cpu
		}
		if info, err := c.debug.proc.MemoryInfo(); err == nil {
			rss = info.RSS
		}
	}

	stats := ReaderStats{}
	if rm, ok := c.tunnel.(*readerMetrics); ok {
		stats = rm.Stats()
	}

	c.cfg.Logger.Info("debug.resources",
		"goroutines", numGoroutines,
		"cpu_percent", cpuPercent,
		"rss_bytes", rss,
		"heap_alloc", mem.HeapAlloc,
		"heap_inuse", mem.HeapInuse,
		"tun_bytes_read", stats.BytesRead,
		"tun_bytes_written", stats.BytesWritten,
		"tun_last_read", stats.LastReadAt,
		"tun_last_write", stats.LastWriteAt,
	)
}

func (c *Client) gatewayMonitor() {
	defer c.debug.wg.Done()

	ticker := time.NewTicker(c.cfg.DebugOptions.GatewayInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.debug.ctx.Done():
			return
		case <-ticker.C:
			c.validateGateway()
		}
	}
}

func (c *Client) validateGateway() {
	newGateway, err := gateway.DiscoverGateway()
	if err != nil {
		c.cfg.Logger.Warn("debug.gateway.discover_failed", "error", err)
		return
	}

	currentGateway := c.currentGatewayIP()
	if currentGateway != nil && newGateway.Equal(currentGateway) {
		return
	}

	c.cfg.Logger.Info("debug.gateway.changed", "old", currentGateway, "new", newGateway)
	c.updateGatewayIP(newGateway)
}

func (c *Client) currentGatewayIP() net.IP {
	c.gwMu.RLock()
	defer c.gwMu.RUnlock()
	if c.cfg.GatewayIP == nil {
		return nil
	}

	ipCopy := make(net.IP, len(*c.cfg.GatewayIP))
	copy(ipCopy, *c.cfg.GatewayIP)

	return ipCopy
}

func (c *Client) updateGatewayIP(newIP net.IP) {
	oldRoute := c.xrayToGatewayRoute()

	c.gwMu.Lock()
	c.cfg.GatewayIP = &newIP
	c.gwMu.Unlock()

	if err := c.routes.Delete(oldRoute); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.cfg.Logger.Warn("debug.gateway.cleanup_failed", "error", err)
	}

	if err := c.routes.Add(c.xrayToGatewayRoute()); err != nil {
		c.cfg.Logger.Error("debug.gateway.route_update_failed", "error", err)
	}
}

func (c *Client) prepareDebugOutputDir() {
	base := c.cfg.DebugOptions.OutputDir
	if base == "" {
		return
	}

	sessionDir := filepath.Join(base, fmt.Sprintf("session-%s", time.Now().Format("20060102-150405")))
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		c.cfg.Logger.Warn("debug.output.mkdir_failed", "dir", sessionDir, "error", err)
		return
	}

	c.debug.outputDir = sessionDir
	c.cfg.Logger.Info("debug.output.dir", "path", sessionDir)
}

func (c *Client) profileDumper() {
	defer c.debug.wg.Done()

	if c.debug.outputDir == "" || c.cfg.DebugOptions.ProfileInterval <= 0 {
		return
	}

	ticker := time.NewTicker(c.cfg.DebugOptions.ProfileInterval)
	defer ticker.Stop()

	c.captureSnapshotProfiles()

	for {
		select {
		case <-c.debug.ctx.Done():
			return
		case <-ticker.C:
			c.captureSnapshotProfiles()
		}
	}
}

func (c *Client) captureSnapshotProfiles() {
	c.writeProfile("goroutine", 2)
	c.writeProfile("heap", 0)
	c.writeProfile("allocs", 0)
}

func (c *Client) writeProfile(name string, debug int) {
	if c.debug.outputDir == "" {
		return
	}

	prof := pprof.Lookup(name)
	if prof == nil {
		return
	}

	filename := filepath.Join(c.debug.outputDir, fmt.Sprintf("%s_%s.pprof", name, time.Now().Format("20060102-150405")))
	file, err := os.Create(filename)
	if err != nil {
		c.cfg.Logger.Warn("debug.profile.create_failed", "profile", name, "error", err)
		return
	}
	defer file.Close()

	if err := prof.WriteTo(file, debug); err != nil {
		c.cfg.Logger.Warn("debug.profile.write_failed", "profile", name, "error", err)
		return
	}

	c.cfg.Logger.Info("debug.profile.saved", "profile", name, "path", filename)
}

func (c *Client) cpuProfileLoop() {
	defer c.debug.wg.Done()

	if c.debug.outputDir == "" || c.cfg.DebugOptions.ProfileInterval <= 0 || c.cfg.DebugOptions.CPUProfileDuration <= 0 {
		return
	}

	ticker := time.NewTicker(c.cfg.DebugOptions.ProfileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.debug.ctx.Done():
			return
		case <-ticker.C:
			c.captureCPUProfile()
		}
	}
}

func (c *Client) captureCPUProfile() {
	if c.debug.outputDir == "" {
		return
	}

	c.debug.cpuProfileMu.Lock()
	defer c.debug.cpuProfileMu.Unlock()

	filename := filepath.Join(c.debug.outputDir, fmt.Sprintf("cpu_%s.pprof", time.Now().Format("20060102-150405")))
	file, err := os.Create(filename)
	if err != nil {
		c.cfg.Logger.Warn("debug.cpu_profile.create_failed", "error", err)
		return
	}
	defer file.Close()

	if err := pprof.StartCPUProfile(file); err != nil {
		c.cfg.Logger.Warn("debug.cpu_profile.start_failed", "error", err)
		return
	}

	timer := time.NewTimer(c.cfg.DebugOptions.CPUProfileDuration)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-c.debug.ctx.Done():
	}
	pprof.StopCPUProfile()

	c.cfg.Logger.Info("debug.cpu_profile.saved", "path", filename, "duration", c.cfg.DebugOptions.CPUProfileDuration)
}

func wrapPipeWithDebug(p pipe, logger *slog.Logger, enabled bool) pipe {
	if !enabled {
		return p
	}
	if _, ok := p.(*instrumentedPipe); ok {
		return p
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
	}

	return &instrumentedPipe{
		inner:  p,
		logger: logger,
	}
}

type instrumentedPipe struct {
	inner  pipe
	logger *slog.Logger
}

func (p *instrumentedPipe) Copy(ctx context.Context, rwc io.ReadWriteCloser, socks5 string) error {
	start := time.Now()
	p.logger.Info("debug.pipe.copy.start", "socks5", socks5)

	err := p.inner.Copy(ctx, rwc, socks5)

	duration := time.Since(start)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		p.logger.Warn("debug.pipe.copy.done", "socks5", socks5, "duration", duration, "error", err)
	} else {
		p.logger.Info("debug.pipe.copy.done", "socks5", socks5, "duration", duration, "error", err)
	}

	return err
}
