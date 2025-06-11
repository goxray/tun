# Go VPN client for XRay
![Static Badge](https://img.shields.io/badge/OS-macOS%20%7C%20Linux-blue?style=flat&logo=linux&logoColor=white&logoSize=auto&color=blue)
![Static Badge](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go&logoColor=white)
[![Go Report Card](https://goreportcard.com/badge/github.com/goxray/tun)](https://goreportcard.com/report/github.com/goxray/tun)
[![Go Reference](https://pkg.go.dev/badge/github.com/goxray/tun.svg)](https://pkg.go.dev/github.com/goxray/tun)
![GitHub Downloads (all assets, all releases)](https://img.shields.io/github/downloads/goxray/tun/total?color=blue)


This project brings fully functioning [XRay](https://github.com/XTLS/Xray-core) VPN client implementation in Go.

> For desktop version see https://github.com/goxray/desktop

<img alt="Terminal example output" align="center" src="/.github/images/carbon.png">

> [!NOTE]
> The program will not damage your routing rules, default route is intact and only additional rules are added for the lifetime of application's TUN device. There are also additional complementary clean up procedures in place.

#### What is XRay?
Please visit https://xtls.github.io/en for more info.

#### Tested and supported on:
- macOS (tested on Sequoia 15.1.1)
- Linux (tested on Ubuntu 24.10)

> Feel free to test this on your system and let me know in the issues :)

## ✨ Features
- Stupidly easy to use
- Supports all [Xray-core](https://github.com/XTLS/Xray-core) protocols (vless, vmess e.t.c.) using link notation (`vless://` e.t.c.)
- Only soft routing rules are applied, no changes made to default routes

## ⚡️ Usage
> [!IMPORTANT]
> - `sudo` is required
> - CGO_ENABLED=1 is required in order to build the project

### Standalone application:

Running the VPN on your machine is as simple as running this little command:
```bash
sudo go run . <proto_link>
```

Where `proto_link` is your XRay link (like `vless://example.com...`), you can get this from your VPN provider or get it from your XRay server.

### As library in your own project:
> [!NOTE]
> This project is built upon the `core` package, see details and documentation at https://github.com/goxray/core

Install:
```bash
go get github.com/goxray/tun/pkg/client
```

Example:
```go
logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
vpn, _ := client.NewClientWithOpts(client.Config{
  TLSAllowInsecure: false,
  Logger:           logger,
})

_ = vpn.Connect(clientLink)
defer vpn.Disconnect(context.Background())

time.Sleep(60 * time.Second)
```

> Please refer to godoc for supported methods and types.

## 🛠 Build

The project compiles like a regular Go program:
```bash
CGO_ENABLED=1 go build -o goxray_cli .
```

#### Cross-compilation

```bash
env CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o goxray_cli_darwin_amd64 .
```

To cross-compile from macOS to Linux arm/amd I use these commands:
```bash
docker run --platform=linux/arm64 -v=${PWD}:/app --workdir=/app arm64v8/golang:1.24 env GOARCH=arm64 go build -o goxray_cli_linux_arm64 .
```
```bash
docker run --platform=linux/amd64 -v=${PWD}:/app --workdir=/app amd64/golang:1.24 env GOARCH=amd64 go build -o goxray_cli_linux_amd64 .
```

## How it works
- Application sets up new TUN device.
- Adds additional routes to route all system traffic to this newly created TUN device.
- Adds exception for XRay outbound address (basically your VPN server IP).
- Tunnel is created to process all incoming IP packets via TCP/IP stack. All outbound traffic is routed through the XRay inbound proxy and all incoming packets are routed back via TUN device.

## 📝 TODO
- [ ] Add IPV6 support

## 🎯 Motivation
There are no available XRay clients implementations in Go on Github, so I decided to do it myself. The attempt proved to be successfull and I wanted to share my findings in a complete and working VPN client.

## Credits

- https://github.com/xtls/xray-core
- https://github.com/lilendian0x00/xray-knife
- https://github.com/jackpal/gateway
