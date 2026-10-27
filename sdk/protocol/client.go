package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ErrAuthRejected indicates that the agent rejected the client's credentials.
var ErrAuthRejected = errors.New("agent authentication rejected")

// Authenticate reads the agent hello and sends the client's token.
func Authenticate(stream io.ReadWriter, token string) (*Conn, Hello, error) {
	conn := NewConn(stream)
	if err := conn.Send("welcome", Welcome{Accepted: true, Token: token}); err != nil {
		return nil, Hello{}, err
	}
	frame, _, err := conn.Read()
	if err != nil {
		return nil, Hello{}, fmt.Errorf("authentication acknowledgement: %w", err)
	}
	if frame.Type != "auth" {
		return nil, Hello{}, fmt.Errorf("authentication acknowledgement: unexpected %q", frame.Type)
	}
	var ack Welcome
	if err := json.Unmarshal(frame.Body, &ack); err != nil {
		return nil, Hello{}, fmt.Errorf("authentication acknowledgement: %w", err)
	}
	if !ack.Accepted {
		return nil, Hello{}, fmt.Errorf("%w: %s", ErrAuthRejected, ack.Error)
	}
	frame, _, err = conn.Read()
	if err != nil {
		return nil, Hello{}, fmt.Errorf("agent hello: %w", err)
	}
	if frame.Type == TypeError {
		var response Warn
		if err := json.Unmarshal(frame.Body, &response); err != nil {
			return nil, Hello{}, fmt.Errorf("agent hello error: %w", err)
		}
		return nil, Hello{}, fmt.Errorf("agent hello: %s", response.Message)
	}
	if frame.Type != "hello" {
		return nil, Hello{}, fmt.Errorf("agent hello: unexpected %q", frame.Type)
	}
	var hello Hello
	if err := json.Unmarshal(frame.Body, &hello); err != nil {
		return nil, Hello{}, fmt.Errorf("agent hello: %w", err)
	}
	if err := CheckVersion(hello.Protocol); err != nil {
		return nil, Hello{}, err
	}
	return conn, hello, nil
}

// Client owns an authenticated stream and its advertised agent identity.
type Client struct {
	Stream net.Conn
	Conn   *Conn
	Hello  Hello
}

// Close closes the authenticated stream.
func (c *Client) Close() error { return c.Stream.Close() }

// Dial opens and authenticates a local agent stream.
func Dial(ctx context.Context, network, address, token string) (*Client, error) {
	stream, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := stream.SetDeadline(deadline); err != nil {
			_ = stream.Close()
			return nil, err
		}
	}
	conn, hello, err := Authenticate(stream, token)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := stream.SetDeadline(time.Time{}); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &Client{Stream: stream, Conn: conn, Hello: hello}, nil
}

// Send writes a message body as an envelope.
func (c *Conn) Send(kind string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.Write(Envelope{Type: kind, Body: body}, nil)
}
