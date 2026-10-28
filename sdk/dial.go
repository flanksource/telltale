package telltale

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
	"github.com/gorilla/websocket"
)

func hubURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid agent hub URL %q", raw)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("invalid agent hub URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/agents/connect"
	return u.String(), nil
}

// WebSocketStream adapts message framing to the agent's connection lifecycle.
type WebSocketStream struct{ *websocket.Conn }

func (s *WebSocketStream) Read([]byte) (int, error)  { return 0, io.ErrNoProgress }
func (s *WebSocketStream) Write([]byte) (int, error) { return 0, io.ErrNoProgress }
func (s *WebSocketStream) LocalAddr() net.Addr       { return s.UnderlyingConn().LocalAddr() }
func (s *WebSocketStream) RemoteAddr() net.Addr      { return s.UnderlyingConn().RemoteAddr() }
func (s *WebSocketStream) SetDeadline(t time.Time) error {
	if err := s.SetReadDeadline(t); err != nil {
		return err
	}
	return s.SetWriteDeadline(t)
}

func (a *Agent) dial(address, token string) {
	defer close(a.dialDone)
	delay := time.Second
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, response, err := websocket.DefaultDialer.DialContext(ctx, address, http.Header{"Authorization": []string{"Bearer " + token}})
		cancel()
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err == nil {
			conn.SetReadLimit(protocol.MaxFrameSize)
			a.serveWithToken(&WebSocketStream{Conn: conn}, token, true)
			delay = time.Second
		} else {
			log.Printf("agent hub connection failed: %v", err)
		}
		wait := time.NewTimer(delay + time.Duration(rand.Int64N(int64(delay/2))))
		select {
		case <-a.dialStop:
			wait.Stop()
			return
		case <-wait.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}
