package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/goxray/core/network/route"
	xkp "github.com/lilendian0x00/xray-knife/v3/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/goxray/tun/pkg/client/mocks"
)

func TestConnect_InvalidLink(t *testing.T) {
	cl := Client{
		cfg: Config{
			Logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
			InboundProxy: &Proxy{},
		},
	}

	err := cl.Connect("invalid_link")
	require.ErrorContains(t, err, "invalid config: protocol create:")

	err = cl.Connect("vless://example.com") // no port
	require.ErrorContains(t, err, "invalid config: parse:")
}

func TestDisconnect_NonConnected(t *testing.T) {
	cl := newTestClient(nil, nil, nil, nil, nil)
	require.NoError(t, cl.Disconnect(context.Background()))
}

func TestDisconnect_CtxTimeout(t *testing.T) {
	tests := []struct {
		name        string
		stopTunFunc func(stopped chan error)
		setupMocks  func(*Client, *mocks.Mockrunnable, *mocks.Mockpipe, *mocks.MockipTable, *mocks.MockioReadWriteCloser)
		assert      func(ctx context.Context, cl *Client, t *testing.T)
	}{
		{
			name: "ok",
			stopTunFunc: func(stopped chan error) {
				stopped <- nil
			},
			setupMocks: func(cl *Client, r *mocks.Mockrunnable, _ *mocks.Mockpipe, ip *mocks.MockipTable, rwc *mocks.MockioReadWriteCloser) {
				r.EXPECT().Close().Return(nil)
				rwc.EXPECT().Close().Return(nil)
				mockSuccessDisconnectIP(t, cl, ip)
			},
			assert: func(ctx context.Context, cl *Client, t *testing.T) {
				require.NoError(t, cl.Disconnect(context.Background()))
			},
		},
		{
			name:        "ctx timeout",
			stopTunFunc: func(stopped chan error) {},
			setupMocks: func(cl *Client, r *mocks.Mockrunnable, _ *mocks.Mockpipe, ip *mocks.MockipTable, rwc *mocks.MockioReadWriteCloser) {
				r.EXPECT().Close().Return(nil)
				rwc.EXPECT().Close().Return(nil)
				mockSuccessDisconnectIP(t, cl, ip)
			},
			assert: func(ctx context.Context, cl *Client, t *testing.T) {
				require.ErrorIs(t, cl.Disconnect(ctx), context.DeadlineExceeded)
			},
		},
		{
			name: "error from instance",
			stopTunFunc: func(stopped chan error) {
				stopped <- nil
			},
			setupMocks: func(cl *Client, r *mocks.Mockrunnable, _ *mocks.Mockpipe, ip *mocks.MockipTable, rwc *mocks.MockioReadWriteCloser) {
				r.EXPECT().Close().Return(errors.New("instance close err"))
				rwc.EXPECT().Close().Return(nil)
				mockSuccessDisconnectIP(t, cl, ip)
			},
			assert: func(ctx context.Context, cl *Client, t *testing.T) {
				require.ErrorContains(t, cl.Disconnect(ctx), "instance close err")
			},
		},
		{
			name: "error from tun",
			stopTunFunc: func(stopped chan error) {
				stopped <- nil
			},
			setupMocks: func(cl *Client, r *mocks.Mockrunnable, _ *mocks.Mockpipe, ip *mocks.MockipTable, rwc *mocks.MockioReadWriteCloser) {
				r.EXPECT().Close().Return(nil)
				rwc.EXPECT().Close().Return(errors.New("tun close err"))
				mockSuccessDisconnectIP(t, cl, ip)
			},
			assert: func(ctx context.Context, cl *Client, t *testing.T) {
				require.ErrorContains(t, cl.Disconnect(ctx), "tun close err")
			},
		},
		{
			name: "error from everything",
			stopTunFunc: func(stopped chan error) {
				stopped <- errors.New("stop err")
			},
			setupMocks: func(cl *Client, r *mocks.Mockrunnable, _ *mocks.Mockpipe, ip *mocks.MockipTable, rwc *mocks.MockioReadWriteCloser) {
				r.EXPECT().Close().Return(errors.New("instance close err"))
				rwc.EXPECT().Close().Return(errors.New("tun close err"))
				mockSuccessDisconnectIP(t, cl, ip)
			},
			assert: func(ctx context.Context, cl *Client, t *testing.T) {
				err := cl.Disconnect(ctx)
				require.ErrorContains(t, err, "tun close err")
				require.ErrorContains(t, err, "instance close err")
				require.ErrorContains(t, err, "stop err")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NotNil(t, test.setupMocks)

			xInstMock := mocks.NewMockrunnable(gomock.NewController(t))
			pipeMock := mocks.NewMockpipe(gomock.NewController(t))
			routesMock := mocks.NewMockipTable(gomock.NewController(t))
			tunMock := mocks.NewMockioReadWriteCloser(gomock.NewController(t))

			cl := newTestClient(xInstMock, tunMock, routesMock, pipeMock, test.stopTunFunc)
			test.setupMocks(cl, xInstMock, pipeMock, routesMock, tunMock)

			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*10)
			defer cancel()

			test.assert(ctx, cl, t)
		})
	}
}

func newTestClient(xInst runnable, tun io.ReadWriteCloser, routes ipTable, pipe pipe, stopTunnel func(chan error)) *Client {
	expGateway := &net.IP{127, 0, 0, 2}
	expProxy := &Proxy{IP: net.IP{127, 0, 0, 1}, Port: 10234}
	expGeneralConfig := &xkp.GeneralConfig{Address: "127.0.0.3"}

	cl := &Client{
		cfg: Config{
			Logger:       slog.New(slog.NewTextHandler(os.Stdout, nil)),
			InboundProxy: expProxy,
			GatewayIP:    expGateway,
		},
		tunnelStopped: make(chan error),
		xInst:         xInst,
		tunnel:        tun,
		routes:        routes,
		pipe:          pipe,
		xCfg:          expGeneralConfig,
	}
	if stopTunnel != nil {
		cl.stopTunnel = func() {
			go func() {
				stopTunnel(cl.tunnelStopped)
			}()
		}
	}

	return cl
}

func mockSuccessDisconnectIP(t *testing.T, cl *Client, ip *mocks.MockipTable) {
	ip.EXPECT().Delete(gomock.Any()).DoAndReturn(func(opts route.Opts) error {
		require.Empty(t, opts.IfName)
		require.Equal(t, *cl.cfg.GatewayIP, opts.Gateway)
		require.Contains(t, opts.Routes, route.MustParseAddr(cl.xCfg.Address+"/32"))
		require.Len(t, opts.Routes, 1)

		return nil
	})
}
