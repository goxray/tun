package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/goxray/tun/pkg/client"
)

var cmdArgsErr = `ERROR: no config_link provided
usage: %s <config_url>
  - config_url - xray connection link, like "vless://example..."
`

func main() {
	debugEnabled := flag.Bool("debug", true, "enable extended debug instrumentation")
	debugPprofAddr := flag.String("debug.pprof", "", "start pprof HTTP listener on the provided address (e.g. 127.0.0.1:6060)")
	debugGatewayInterval := flag.Duration("debug.gateway-interval", 3*time.Second, "interval for gateway validation logs")
	debugResourceInterval := flag.Duration("debug.resource-interval", 5*time.Second, "interval for resource usage logs")
	debugDisableNetlink := flag.Bool("debug.disable-netlink", false, "disable linux netlink subscription")
	debugOutputDir := flag.String("debug.output-dir", "debug-output", "directory where automatic debug dumps will be stored")
	debugProfileInterval := flag.Duration("debug.profile-interval", 5*time.Second, "interval for writing goroutine/heap profiles to disk")
	debugCPUDuration := flag.Duration("debug.cpu-duration", 5*time.Second, "duration of each CPU profile capture")
	debugDisableCPUProfile := flag.Bool("debug.disable-cpu-profile", false, "disable automatic CPU profile capture")

	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, cmdArgsErr, os.Args[0])
		os.Exit(0)
	}
	clientLink := args[0]

	// Setup logging to file and stdout first (before any other operations)
	logFile, err := os.OpenFile("goxray.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// If we can't open log file, use stdout only
		logFile = os.Stdout
	} else {
		defer logFile.Close()
	}

	// Create multi-writer to write to both file and stdout
	multiWriter := io.MultiWriter(os.Stdout, logFile)

	// Setup slog logger with debug level to log everything
	logger := slog.New(slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Setup standard log package to also write to file
	log.SetOutput(multiWriter)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, os.Interrupt, syscall.SIGTERM)

	cfg := client.Config{
		TLSAllowInsecure: false,
		Logger:           logger,
	}

	if *debugEnabled {
		cfg.Debug = true

		cfg.DebugOptions = client.DebugOptions{
			ResourceInterval:   *debugResourceInterval,
			GatewayInterval:    *debugGatewayInterval,
			EnableNetlink:      !*debugDisableNetlink,
			VerbosePipe:        true,
			OutputDir:          *debugOutputDir,
			ProfileInterval:    *debugProfileInterval,
			CPUProfileDuration: *debugCPUDuration,
			CollectCPUProfile:  !*debugDisableCPUProfile,
		}
	}

	if *debugPprofAddr != "" {
		go func() {
			slog.Info("starting pprof debug listener", "addr", *debugPprofAddr)
			if err := http.ListenAndServe(*debugPprofAddr, nil); err != nil {
				slog.Error("pprof listener exited", "error", err)
			}
		}()
	}

	vpn, err := client.NewClientWithOpts(cfg)
	if err != nil {
		log.Fatal(err)
	}

	slog.Info("Connecting to VPN server")
	err = vpn.Connect(clientLink)
	if err != nil {
		log.Fatal(err)
	}

	slog.Info("Connected to VPN server")
	<-sigterm
	slog.Info("Received term signal, disconnecting...")
	if err = vpn.Disconnect(context.Background()); err != nil {
		slog.Warn("Disconnecting VPN failed", "error", err)
		os.Exit(0)
	}

	slog.Info("VPN disconnected successfully. DEBUG: wait 15s before exit...")
	<-time.After(15 * time.Second)
	os.Exit(0)
}
