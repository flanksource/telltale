package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const MaxFrameSize = 64 << 20

var ErrFrameTooLarge = errors.New("protocol frame too large")

// Conn encodes length-prefixed protocol envelopes and blob frames.
type Conn struct {
	rw      io.ReadWriter
	writeMu sync.Mutex
}

// NewConn wraps a stream with the protocol codec.
func NewConn(rw io.ReadWriter) *Conn { return &Conn{rw: rw} }

// Close closes the underlying stream when it supports closing.
func (c *Conn) Close() error {
	if closer, ok := c.rw.(io.Closer); ok {
		return closer.Close()
	}
	return errors.New("protocol stream cannot be closed")
}

// Write sends an envelope and, for a blob, its raw follow-up frame.
func (c *Conn) Write(envelope Envelope, blob []byte) error {
	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	if envelope.Type == "blob" {
		var info Blob
		if err := json.Unmarshal(envelope.Body, &info); err != nil {
			return fmt.Errorf("decode blob: %w", err)
		}
		if info.Len != len(blob) {
			return fmt.Errorf("blob length: declared %d, got %d", info.Len, len(blob))
		}
	} else if blob != nil {
		return errors.New("raw frame requires blob envelope")
	}
	if len(data) > MaxFrameSize || len(blob) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if stream, ok := c.rw.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if err := stream.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
	}
	if err := c.writeFrame(data, 1); err != nil {
		return err
	}
	if envelope.Type == "blob" {
		return c.writeFrame(blob, 2)
	}
	return nil
}

func (c *Conn) writeFrame(data []byte, kind int) error {
	if stream, ok := c.rw.(interface{ WriteMessage(int, []byte) error }); ok {
		return stream.WriteMessage(kind, data)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	for _, part := range [][]byte{header[:], data} {
		for len(part) > 0 {
			n, err := c.rw.Write(part)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			part = part[n:]
		}
	}
	return nil
}

// Read receives an envelope and its raw blob frame when present.
func (c *Conn) Read() (Envelope, []byte, error) {
	return c.ReadLimit(MaxFrameSize)
}

// ReadLimit receives a frame while enforcing the supplied size cap.
func (c *Conn) ReadLimit(limit uint32) (Envelope, []byte, error) {
	data, err := c.readFrame(limit, 1)
	if err != nil {
		return Envelope{}, nil, err
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Envelope{}, nil, fmt.Errorf("decode envelope: %w", err)
	}
	if envelope.Type != "blob" {
		return envelope, nil, nil
	}
	var info Blob
	if err := json.Unmarshal(envelope.Body, &info); err != nil {
		return Envelope{}, nil, fmt.Errorf("decode blob: %w", err)
	}
	if info.Len < 0 || info.Len > MaxFrameSize {
		return Envelope{}, nil, ErrFrameTooLarge
	}
	blob, err := c.readFrame(limit, 2)
	if err != nil {
		return Envelope{}, nil, err
	}
	if len(blob) != info.Len {
		return Envelope{}, nil, fmt.Errorf("blob length: declared %d, got %d", info.Len, len(blob))
	}
	return envelope, blob, nil
}

func (c *Conn) readFrame(limit uint32, expected int) ([]byte, error) {
	if stream, ok := c.rw.(interface{ ReadMessage() (int, []byte, error) }); ok {
		kind, data, err := stream.ReadMessage()
		if err != nil {
			return nil, err
		}
		if kind != expected {
			return nil, fmt.Errorf("unexpected message type %d, expected %d", kind, expected)
		}
		if len(data) > int(limit) || len(data) > MaxFrameSize {
			return nil, ErrFrameTooLarge
		}
		return data, nil
	}
	var header [4]byte
	if _, err := io.ReadFull(c.rw, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length > MaxFrameSize || length > limit {
		return nil, ErrFrameTooLarge
	}
	data := make([]byte, length)
	_, err := io.ReadFull(c.rw, data)
	return data, err
}
