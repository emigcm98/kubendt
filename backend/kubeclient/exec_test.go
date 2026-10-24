package kubeclient

import (
	"errors"
	"testing"
)

func TestUpgradeRefused(t *testing.T) {
	cases := map[string]bool{
		`unable to upgrade streaming request: websocket: bad handshake (500 Internal Server Error): container not found ("oneshot")`: false,
		`websocket: bad handshake (503 Service Unavailable)`:                                                                         false,
		`websocket: bad handshake (400 Bad Request): unable to upgrade`:                                                              true,
		`websocket: bad handshake (403 Forbidden)`:                                                                                   true,
		`proxy error from 10.0.0.1:3128 while dialing`:                                                                               true,
	}
	for msg, want := range cases {
		if got := upgradeRefused(errors.New(msg)); got != want {
			t.Errorf("%q: got %v, want %v", msg, got, want)
		}
	}
}
