package telltale

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

var ErrUnknownMessage = errors.New("unknown message")
var ErrUnknownSession = errors.New("unknown session")
var ErrCheckpointUnsupported = errors.New("checkpoint unsupported")

// Options configures the local agent listener and its advertised identity.
type Options struct {
	Service, Version, Listen, Dial, Token string
	ConfigDir                             string
	Labels                                map[string]string
	DetectCloud, AllowMutation            bool
	Probes                                []string
	// BlockProfileRate is the agent-owned baseline; applications must not set it directly.
	BlockProfileRate int
}

// Agent serves probe sessions over local connections.
type Agent struct {
	options     Options
	endpoint    protocol.Endpoint
	listener    net.Listener
	portFile    string
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	serving     sync.WaitGroup
	dialDone    chan struct{}
	dialStop    chan struct{}
	dialID      string
	closed      bool
	warnings    []string
	readTimeout time.Duration
	authTimeout time.Duration
}

// Start starts a local listener and writes its private discovery file.
func Start(options Options) (*Agent, error) {
	if options.BlockProfileRate < 0 {
		return nil, fmt.Errorf("block profile rate must be non-negative")
	}
	if options.Dial == "" {
		options.Dial = os.Getenv("TELLTALE_URL")
	}
	dialToken := options.Token
	dialAddress := ""
	if dialToken == "" {
		dialToken = os.Getenv("TELLTALE_TOKEN")
	}
	if options.Dial != "" {
		var err error
		if dialAddress, err = hubURL(options.Dial); err != nil {
			return nil, err
		}
		if dialToken == "" {
			return nil, errors.New("agent dial token is required")
		}
		options.Token = ""
	}
	if options.Token == "" {
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			return nil, fmt.Errorf("agent token: %w", err)
		}
		options.Token = hex.EncodeToString(token[:])
	}
	options.AllowMutation = options.AllowMutation || os.Getenv("TELLTALE_ALLOW_MUTATION") == "1"
	options.DetectCloud = options.DetectCloud || os.Getenv("TELLTALE_DETECT_CLOUD") == "1"
	listener, portFile, err := openListener(options.Listen, options.ConfigDir)
	if err != nil {
		return nil, err
	}
	agent := newAgent(options)
	if options.Dial != "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			_ = listener.Close()
			return nil, fmt.Errorf("agent identity: %w", err)
		}
		agent.dialID = "agent-" + hex.EncodeToString(id[:])
	}
	agent.listener = listener
	agent.endpoint.Addr = listener.Addr().String()
	agent.portFile = portFile
	if err := agent.writePortFile(); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("write agent discovery file: %w", err)
	}
	profileMu.Lock()
	if blockSessions != 0 {
		profileMu.Unlock()
		_ = listener.Close()
		_ = os.Remove(agent.portFile)
		return nil, fmt.Errorf("cannot start agent while block profiling is armed")
	}
	blockBaseline = options.BlockProfileRate
	runtime.SetBlockProfileRate(blockBaseline)
	profileMu.Unlock()
	go agent.accept()
	if options.Dial != "" {
		agent.dialDone = make(chan struct{})
		agent.dialStop = make(chan struct{})
		go agent.dial(dialAddress, dialToken)
	}
	return agent, nil
}

func openListener(listen, config string) (net.Listener, string, error) {
	if config == "" {
		var err error
		config, err = os.UserConfigDir()
		if err != nil {
			return nil, "", fmt.Errorf("agent config directory: %w", err)
		}
	}
	dir := filepath.Join(config, "telltale", "agents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, "", fmt.Errorf("agent directory: %w", err)
	}
	network, address := "unix", filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
	if listen == "tcp" {
		network, address = "tcp", "127.0.0.1:0"
	} else if listen != "" {
		return nil, "", fmt.Errorf("agent listen %q: expected tcp or empty", listen)
	}
	portFile := filepath.Join(dir, strconv.Itoa(os.Getpid())+".json")
	if err := removeStaleFile(portFile); err != nil {
		return nil, "", err
	}
	if network == "unix" {
		if err := removeStaleSocket(address); err != nil {
			return nil, "", err
		}
	}
	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, "", fmt.Errorf("agent listen: %w", err)
	}
	return listener, portFile, nil
}

func (a *Agent) writePortFile() error {
	data, err := json.Marshal(a.endpoint)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(a.portFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	err = errors.Join(err, file.Close())
	if err != nil {
		_ = os.Remove(a.portFile)
	}
	return err
}

func newAgent(options Options) *Agent {
	resource, warning := resourceWithCloud(os.Getenv, os.ReadFile, options.DetectCloud, 250*time.Millisecond)
	if options.Service != "" {
		resource["service.name"] = options.Service
	}
	for key, value := range options.Labels {
		resource[key] = value
	}
	agent := &Agent{options: options, endpoint: protocol.Endpoint{PID: os.Getpid(), Token: options.Token, Service: options.Service, Runtime: protocol.Runtime{Lang: "go", Version: runtime.Version(), Vendor: "go"}, Resource: resource, Started: time.Now()}, connections: make(map[net.Conn]struct{}), readTimeout: 30 * time.Second, authTimeout: 5 * time.Second}
	if warning != "" {
		agent.warnings = append(agent.warnings, warning)
	}
	return agent
}

// Loopback creates an in-memory agent without a discovery file.
func Loopback() *Agent {
	return newAgent(Options{AllowMutation: os.Getenv("TELLTALE_ALLOW_MUTATION") == "1"})
}

// Connect opens an in-memory protocol connection to an agent.
func (a *Agent) Connect() net.Conn { client, server := net.Pipe(); go a.serve(server); return client }

func (a *Agent) accept() {
	delay := time.Duration(0)
	for {
		conn, err := a.listener.Accept()
		if err != nil {
			a.mu.Lock()
			closed := a.closed
			a.mu.Unlock()
			if closed {
				return
			}
			if temporary, ok := err.(net.Error); ok && temporary.Temporary() {
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else {
					delay *= 2
				}
				if delay > time.Second {
					delay = time.Second
				}
				time.Sleep(delay)
				continue
			}
			log.Printf("agent listener failed: %v", err)
			if removeErr := os.Remove(a.portFile); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				log.Printf("remove agent discovery file: %v", removeErr)
			}
			return
		}
		delay = 0
		go a.serve(conn)
	}
}

// Close stops the listener and disarms sessions on all connections.
func (a *Agent) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	if a.dialStop != nil {
		close(a.dialStop)
	}
	for conn := range a.connections {
		_ = conn.Close()
	}
	a.mu.Unlock()
	if a.dialDone != nil {
		<-a.dialDone
	}
	a.serving.Wait()
	var errs []error
	if a.listener != nil {
		errs = append(errs, a.listener.Close())
	}
	if a.portFile != "" {
		if err := os.Remove(a.portFile); !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func send(conn *protocol.Conn, kind string, value any, blob []byte) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	envelope := protocol.Envelope{Type: kind, Body: body}
	switch value := value.(type) {
	case protocol.Records:
		envelope.ID, envelope.Seq = value.Session, value.Seq
	case protocol.Blob:
		envelope.ID, envelope.Seq = value.Session, value.Seq
		if envelope.ID == "" {
			envelope.ID = value.CallID
		}
	case protocol.Armed:
		envelope.ID = value.Session
	case protocol.Checkpointed:
		envelope.ID = value.Session
	case protocol.Ended:
		envelope.ID = value.Session
	case protocol.Warn:
		envelope.ID = value.Session
	case protocol.Report:
		envelope.ID = value.Session
	case protocol.Result:
		envelope.ID = value.CallID
	}
	return conn.Write(envelope, blob)
}

func (a *Agent) serve(stream net.Conn) { a.serveWithToken(stream, a.endpoint.Token, false) }

func (a *Agent) serveWithToken(stream net.Conn, token string, remote bool) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		_ = stream.Close()
		return
	}
	a.connections[stream] = struct{}{}
	a.serving.Add(1)
	a.mu.Unlock()
	defer func() {
		_ = stream.Close()
		a.mu.Lock()
		delete(a.connections, stream)
		a.mu.Unlock()
		a.serving.Done()
	}()
	conn := protocol.NewConn(stream)
	if a.handshake(stream, conn, token, remote) != nil {
		return
	}
	sessions := &sessionMap{values: make(map[string]*session)}
	defer func() {
		for _, session := range sessions.snapshot() {
			session.stop("")
		}
	}()
	for {
		if err := stream.SetReadDeadline(time.Now().Add(a.readTimeout)); err != nil {
			return
		}
		envelope, _, err := conn.Read()
		if err != nil {
			return
		}
		if !a.dispatch(conn, sessions, envelope) {
			return
		}
	}
}

func (a *Agent) handshake(stream net.Conn, conn *protocol.Conn, token string, remote bool) error {
	if err := stream.SetReadDeadline(time.Now().Add(a.authTimeout)); err != nil {
		return err
	}
	envelope, _, err := conn.ReadLimit(4096)
	if err != nil || envelope.Type != "welcome" {
		return fmt.Errorf("authentication frame: %v", err)
	}
	var welcome protocol.Welcome
	if json.Unmarshal(envelope.Body, &welcome) != nil || !welcome.Accepted || subtle.ConstantTimeCompare([]byte(welcome.Token), []byte(token)) != 1 {
		_ = send(conn, "auth", protocol.Welcome{Error: "invalid credentials"}, nil)
		return protocol.ErrAuthRejected
	}
	if err := send(conn, "auth", protocol.Welcome{Accepted: true}, nil); err != nil {
		return err
	}
	if err := stream.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	return a.sendHello(conn, remote)
}

func (a *Agent) sendHello(conn *protocol.Conn, remote bool) error {
	catalog.RLock()
	defer catalog.RUnlock()
	hello := protocol.Hello{AgentID: strconv.Itoa(a.endpoint.PID), Service: a.options.Service, Version: a.options.Version, SDKVersion: "dev", Protocol: protocol.ProtocolVersion, Started: a.endpoint.Started, Runtime: a.endpoint.Runtime, Resource: a.endpoint.Resource, Labels: a.options.Labels, Warnings: a.warnings}
	if remote {
		hello.AgentID = a.dialID
	}
	for _, probe := range catalog.probes {
		spec, err := probeSpec(probe)
		if err != nil {
			_ = send(conn, "error", protocol.Warn{Message: err.Error()}, nil)
			return err
		}
		if a.probeAllowed(spec.Name) {
			hello.Probes = append(hello.Probes, spec)
		}
	}
	for _, op := range catalog.ops {
		spec, err := opSpec(op)
		if err != nil {
			_ = send(conn, "error", protocol.Warn{Message: err.Error()}, nil)
			return err
		}
		hello.Ops = append(hello.Ops, spec)
	}
	return send(conn, "hello", hello, nil)
}

func (a *Agent) dispatch(conn *protocol.Conn, sessions *sessionMap, envelope protocol.Envelope) bool {
	switch envelope.Type {
	case "arm":
		a.arm(conn, sessions, envelope.Body)
	case "checkpoint":
		var request protocol.Checkpoint
		if json.Unmarshal(envelope.Body, &request) != nil {
			return false
		}
		return checkpoint(conn, sessions, request)
	case "disarm":
		var request protocol.Disarm
		if json.Unmarshal(envelope.Body, &request) != nil {
			return false
		}
		if session := sessions.get(request.Session); session != nil {
			session.stop("")
		}
	case "ping":
		if send(conn, "pong", protocol.Pong{}, nil) != nil {
			return false
		}
	case "call":
		return a.call(conn, envelope.Body)
	default:
		if send(conn, "error", protocol.Warn{Message: fmt.Sprintf("%s: %s", ErrUnknownMessage, envelope.Type)}, nil) != nil {
			return false
		}
	}
	return true
}

func (a *Agent) call(conn *protocol.Conn, body json.RawMessage) bool {
	var request protocol.Call
	if err := json.Unmarshal(body, &request); err != nil {
		return send(conn, "result", protocol.Result{Error: err.Error()}, nil) == nil
	}
	catalog.RLock()
	op := catalog.ops[request.Op]
	catalog.RUnlock()
	result := protocol.Result{CallID: request.CallID}
	var spec protocol.OpSpec
	var specErr error
	if op != nil {
		spec, specErr = opSpec(op)
	}
	if op == nil {
		result.Error = "unknown operation: " + request.Op
	} else if specErr != nil {
		result.Error = specErr.Error()
	} else if spec.Mutates && !a.options.AllowMutation {
		result.Error = ErrAgentMutationDisabled.Error()
	} else if spec.Mutates && !request.AllowMutation {
		result.Error = ErrCallerMutationDisabled.Error()
	} else {
		value, blobs, err := callOp(op, request.Params)
		if len(blobs) != 0 {
			err = errors.Join(err, errors.New("operation blobs require data"))
		}
		if err == nil {
			result.Value, err = json.Marshal(value)
		}
		if err != nil {
			result.Error = err.Error()
		}
	}
	return send(conn, "result", result, nil) == nil
}

func (a *Agent) probeAllowed(name string) bool {
	if len(a.options.Probes) == 0 {
		return true
	}
	for _, allowed := range a.options.Probes {
		if allowed == name {
			return true
		}
	}
	return false
}
