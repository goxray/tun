package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/goxray/tun/pkg/client"
)

var cmdArgsErr = `ERROR: no config_link provided
usage: %s <config_url>
  - config_url - xray connection link, like "vless://example..."
  - or set GOXRAY_CONFIG_URL env var
  - GOXRAY_CONFIG_DNS - optional DNS server IP resolved through the VPN (default: 1.1.1.1)
`

// dnsServerFromEnv returns the host DNS server from the GOXRAY_CONFIG_DNS env
// var. Nil when unset - the client then falls back to its default (1.1.1.1).
func dnsServerFromEnv() (*net.IP, error) {
	value := os.Getenv("GOXRAY_CONFIG_DNS")
	if value == "" {
		return nil, nil
	}

	ip := net.ParseIP(value)
	if ip == nil {
		return nil, fmt.Errorf("GOXRAY_CONFIG_DNS is not a valid IP address: %q", value)
	}

	return &ip, nil
}

func main() {
	// Get connection link from first cmd argument or env var.
	var clientLink string
	if len(os.Args[1:]) > 0 {
		clientLink = os.Args[1]
	} else {
		clientLink = os.Getenv("GOXRAY_CONFIG_URL")
	}
	if clientLink == "" {
		fmt.Printf(cmdArgsErr, os.Args[0])
		os.Exit(0)
	}

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, os.Interrupt, syscall.SIGTERM)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	dnsServer, err := dnsServerFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	vpn, err := client.NewClientWithOpts(client.Config{
		TLSAllowInsecure: false,
		DNSServer:        dnsServer,
		Logger:           logger,
	})
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

	slog.Info("VPN disconnected successfully")
	os.Exit(0)
}
