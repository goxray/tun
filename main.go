package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/goxray/tun/pkg/client"
)

var cmdArgsErr = `ERROR: no config_link provided
usage: %s <config_url>
  - config_url - xray connection link, like "vless://example..."
`

func main() {
	// Get connection link from first cmd argument
	if len(os.Args[1:]) != 1 {
		fmt.Printf(cmdArgsErr, os.Args[0])
		os.Exit(0)
	}
	clientLink := os.Args[1]

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, os.Interrupt, syscall.SIGTERM)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	vpn, err := client.NewClientWithOpts(client.Config{
		TLSAllowInsecure: false,
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
