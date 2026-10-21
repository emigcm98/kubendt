package kubeclient

import (
	"log"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecTransportEnv forces the exec transport: "spdy" keeps the one every
// release before 1.6.0 used, anything else means WebSocket with SPDY as the
// fallback.
const ExecTransportEnv = "KUBENDT_EXEC_TRANSPORT"

// Set once the API server has refused a WebSocket upgrade: from then on
// every exec goes straight to SPDY instead of paying the refused attempt
// again, which kubectl does per command but a backend running thousands
// of execs should not.
var spdyOnly atomic.Bool

// NewExecutor builds the executor of one exec request. WebSocket is what
// kubectl uses since 1.29 (on by default in the API server from 1.30, GA in
// 1.31) and costs about a third less per exec than SPDY here; SPDY stays as
// the fallback for an API server or a proxy that cannot upgrade.
func NewExecutor(method string, u *url.URL) (remotecommand.Executor, error) {
	spdy, err := remotecommand.NewSPDYExecutor(Config, method, u)
	if err != nil {
		return nil, err
	}
	if ExecTransport() == "spdy" || spdyOnly.Load() {
		return spdy, nil
	}
	ws, err := remotecommand.NewWebSocketExecutor(Config, method, u.String())
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		if !httpstream.IsUpgradeFailure(err) && !httpstream.IsHTTPSProxyError(err) {
			return false
		}
		if spdyOnly.CompareAndSwap(false, true) {
			log.Printf("⚠️ The API server does not upgrade execs to WebSocket (%v): using SPDY from now on, as before 1.6.0", err)
		}
		return true
	})
}

// ExecTransport is the transport the backend is set to, "websocket" or
// "spdy"; a refused upgrade moves it to "spdy" at runtime.
func ExecTransport() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(ExecTransportEnv)), "spdy") || spdyOnly.Load() {
		return "spdy"
	}
	return "websocket"
}
