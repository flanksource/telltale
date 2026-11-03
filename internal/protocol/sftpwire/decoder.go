package sftpwire

import (
	"encoding/binary"
	"fmt"
	"sync"
)

type Event struct {
	RequestID uint32  `json:"requestId"`
	Operation string  `json:"operation"`
	Path      string  `json:"path,omitempty"`
	Offset    uint64  `json:"offset,omitempty"`
	Bytes     uint32  `json:"bytes,omitempty"`
	Status    *uint32 `json:"status,omitempty"`
}

type Decoder struct {
	Emit    func(Event)
	mu      sync.Mutex
	pending map[uint32]pending
	handles map[string]string
}

type pending struct {
	event  Event
	handle string
}

var operations = map[byte]string{3: "open", 4: "close", 5: "read", 6: "write", 7: "lstat", 8: "fstat", 9: "setstat", 10: "fsetstat", 11: "opendir", 12: "readdir", 13: "remove", 14: "mkdir", 15: "rmdir", 16: "realpath", 17: "stat", 18: "rename", 19: "readlink", 20: "symlink"}

func (d *Decoder) Request(packet []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(packet) < 5 {
		return fmt.Errorf("incomplete SFTP request")
	}
	if packet[0] == 1 {
		if binary.BigEndian.Uint32(packet[1:]) != 3 {
			return fmt.Errorf("SFTP decoder supports version 3")
		}
		return nil
	}
	id := binary.BigEndian.Uint32(packet[1:5])
	payload := packet[5:]
	if d.pending == nil {
		d.pending = map[uint32]pending{}
		d.handles = map[string]string{}
	}
	if _, exists := d.pending[id]; exists {
		return fmt.Errorf("duplicate SFTP request %d", id)
	}
	if len(d.pending) >= 4096 {
		return fmt.Errorf("SFTP outstanding request limit exceeded")
	}
	value, err := takeString(&payload)
	if err != nil {
		return err
	}
	p := pending{event: Event{RequestID: id, Operation: operations[packet[0]], Path: value}}
	switch packet[0] {
	case 4, 5, 6, 8, 10, 12:
		p.handle = value
		path, ok := d.handles[value]
		if !ok {
			return fmt.Errorf("unknown SFTP handle")
		}
		p.event.Path = path
		if packet[0] == 5 || packet[0] == 6 {
			if len(payload) < 12 {
				return fmt.Errorf("incomplete SFTP transfer request")
			}
			p.event.Offset = binary.BigEndian.Uint64(payload)
			p.event.Bytes = binary.BigEndian.Uint32(payload[8:])
			if packet[0] == 6 && uint64(p.event.Bytes) != uint64(len(payload)-12) {
				return fmt.Errorf("invalid SFTP write length")
			}
		}
	case 200:
		p.event.Operation = "extension:" + value
		p.event.Path = ""
	default:
		if p.event.Operation == "" {
			return fmt.Errorf("unsupported SFTP request type %d", packet[0])
		}
	}
	d.pending[id] = p
	return nil
}

func (d *Decoder) Response(packet []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(packet) < 5 {
		return fmt.Errorf("incomplete SFTP response")
	}
	if packet[0] == 2 {
		if binary.BigEndian.Uint32(packet[1:]) != 3 {
			return fmt.Errorf("SFTP decoder supports version 3")
		}
		return nil
	}
	id := binary.BigEndian.Uint32(packet[1:5])
	payload := packet[5:]
	p, ok := d.pending[id]
	if !ok {
		return fmt.Errorf("unmatched SFTP response %d", id)
	}
	switch packet[0] {
	case 101:
		if len(payload) < 4 {
			return fmt.Errorf("incomplete SFTP status")
		}
		status := binary.BigEndian.Uint32(payload)
		p.event.Status = &status
		if status == 0 && p.event.Operation == "close" {
			delete(d.handles, p.handle)
		}
	case 102:
		handle, err := takeString(&payload)
		if err != nil {
			return err
		}
		if len(d.handles) >= 4096 {
			return fmt.Errorf("SFTP open handle limit exceeded")
		}
		d.handles[handle] = p.event.Path
	case 103:
		if len(payload) < 4 || uint64(binary.BigEndian.Uint32(payload)) != uint64(len(payload)-4) {
			return fmt.Errorf("invalid SFTP data length")
		}
		p.event.Bytes = binary.BigEndian.Uint32(payload)
	case 104, 105, 201:
	default:
		return fmt.Errorf("unsupported SFTP response type %d", packet[0])
	}
	delete(d.pending, id)
	if d.Emit != nil {
		d.Emit(p.event)
	}
	return nil
}

func takeString(payload *[]byte) (string, error) {
	if len(*payload) < 4 {
		return "", fmt.Errorf("incomplete SFTP string length")
	}
	n := uint64(binary.BigEndian.Uint32(*payload))
	rest := (*payload)[4:]
	if n > uint64(len(rest)) {
		return "", fmt.Errorf("incomplete SFTP string")
	}
	value := string(rest[:int(n)])
	*payload = rest[int(n):]
	return value, nil
}
