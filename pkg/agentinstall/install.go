package agentinstall

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	posixpath "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

type Options struct {
	Destination   Destination `json:"destination"`
	PID           int         `json:"pid"`
	Directory     string      `json:"directory"`
	JavaHome      string      `json:"javaHome,omitempty"`
	HubURL        string      `json:"hubUrl,omitempty"`
	TokenFile     string      `json:"tokenFile,omitempty"`
	Service       string      `json:"service"`
	DryRun        bool        `json:"dryRun,omitempty"`
	AllowMutation bool        `json:"allowMutation,omitempty"`
	ExtensionsDir string      `json:"extensionsDir,omitempty"`
	// HTTP starts the agent's HTTP listener, which executions reach directly
	// with a token the listener verifies; it needs no hub.
	HTTP *HTTPListener `json:"http,omitempty"`
}

// HTTPListener is the attached agent's direct-HTTP endpoint and the key its
// executions are verified with. It carries no shared secret.
type HTTPListener struct {
	Bind         string          `json:"bind"`
	Port         int             `json:"port"`
	PortFile     string          `json:"portFile,omitempty"`
	Issuer       string          `json:"issuer"`
	Audience     string          `json:"audience"`
	JWKS         json.RawMessage `json:"jwks,omitempty"`
	JWKSURL      string          `json:"jwksUrl,omitempty"`
	ConfigDigest string          `json:"configDigest,omitempty"`
}

func (h HTTPListener) validate() error {
	switch {
	case h.Bind == "":
		return fmt.Errorf("agent HTTP listener bind address is required")
	case h.Port < 0 || h.Port > 65535:
		return fmt.Errorf("agent HTTP listener port %d is out of range", h.Port)
	case h.Issuer == "" || h.Audience == "":
		return fmt.Errorf("agent HTTP listener requires an issuer and an audience")
	case len(h.JWKS) == 0 && h.JWKSURL == "":
		return fmt.Errorf("agent HTTP listener requires jwks or jwksUrl")
	}
	return nil
}

type Result struct {
	Destination Destination `json:"destination"`
	PID         int         `json:"pid"`
	SHA256      string      `json:"sha256"`
	Jar         string      `json:"jar"`
	Agent       string      `json:"agent,omitempty"`
	DryRun      bool        `json:"dryRun"`
}

func (o Options) Validate() error {
	if err := o.Destination.Validate(); err != nil {
		return err
	}
	if o.PID < 0 || (o.PID == 0 && o.Destination.Kind != "kubernetes") {
		return fmt.Errorf("a positive target JVM pid is required (Kubernetes targets can discover it from /proc)")
	}
	if !strings.HasPrefix(o.Directory, "/") || strings.ContainsAny(o.Directory, "\x00\n\r") {
		return fmt.Errorf("an absolute installation directory is required")
	}
	if o.HTTP != nil {
		if err := o.HTTP.validate(); err != nil {
			return err
		}
	}
	if o.Destination.Kind != "local" && o.HTTP == nil && (o.HubURL == "" || o.TokenFile == "") {
		return fmt.Errorf("remote installation requires an HTTP listener, or a hub URL and agent token file")
	}
	if o.HubURL != "" {
		u, err := url.Parse(o.HubURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid hub URL")
		}
	}
	return nil
}

type Artifact struct {
	Name string
	Data []byte
}
type Attachment struct {
	PID              int
	Artifact, Config string
	JavaHome         string
}
type Runtime interface {
	Artifact() (Artifact, error)
	Attach(Attachment) []string
}
type Installer struct {
	Runtime     Runtime
	Transport   Transport
	DownloadJDK func(context.Context, JDKRequest) (JDK, error)
}

func (i Installer) Install(ctx context.Context, o Options) (Result, error) {
	if err := o.Validate(); err != nil {
		return Result{}, err
	}
	if i.Runtime == nil {
		return Result{}, fmt.Errorf("agent runtime adapter is required")
	}
	artifact, err := i.Runtime.Artifact()
	if err != nil {
		return Result{}, fmt.Errorf("agent assets unavailable; run make build: %w", err)
	}
	jar := artifact.Data
	sum := sha256.Sum256(jar)
	result := Result{Destination: o.Destination, PID: o.PID, SHA256: hex.EncodeToString(sum[:]), DryRun: o.DryRun}
	dir := o.Directory + "/agent-" + result.SHA256[:16]
	result.Jar = dir + "/" + artifact.Name
	if o.DryRun {
		return result, nil
	}
	if o.Destination.Kind == "local" {
		root, rootErr := os.UserConfigDir()
		if rootErr != nil {
			return result, rootErr
		}
		path := filepath.Join(root, "telltale", "agents", strconv.Itoa(o.PID)+".json")
		if _, statErr := os.Stat(path); statErr == nil {
			endpoint, readErr := protocol.ReadEndpoint(ctx, path)
			if readErr != nil {
				return result, fmt.Errorf("existing agent endpoint: %w", readErr)
			}
			client, dialErr := protocol.Dial(ctx, "tcp", endpoint.Addr, endpoint.Token)
			if dialErr != nil {
				return result, dialErr
			}
			defer func() { _ = client.Close() }()
			if client.Hello.Resource["telltale.artifact.sha256"] != result.SHA256 || client.Hello.Service != o.Service || client.Hello.Resource["telltale.hub.url"] != o.HubURL || client.Hello.Resource["telltale.allowMutation"] != strconv.FormatBool(o.AllowMutation) || client.Hello.Resource["telltale.extensionsDir"] != o.ExtensionsDir {
				return result, fmt.Errorf("an incompatible agent is already installed; restart the JVM before changing its agent")
			}
			result.Agent = client.Hello.AgentID
			return result, nil
		} else if !os.IsNotExist(statErr) {
			return result, statErr
		}
	}
	transport := i.Transport
	if transport == nil {
		transport = CommandTransport{Destination: o.Destination}
	}
	platform, err := transport.Run(ctx, []string{"uname", "-s"}, nil)
	if err != nil {
		return result, err
	}
	linux := strings.TrimSpace(string(platform)) == "Linux"
	if o.PID == 0 {
		if !linux {
			return result, fmt.Errorf("automatic Java PID discovery requires Linux /proc")
		}
		o.PID, err = javaPID(ctx, transport)
		if err != nil {
			return result, err
		}
		result.PID = o.PID
	}
	identity, err := processIdentity(ctx, transport, linux, o.PID)
	if err != nil {
		return result, err
	}
	javaHome := o.JavaHome
	if linux {
		if javaHome, err = i.javaHome(ctx, transport, o); err != nil {
			return result, err
		}
	}
	if _, err = transport.Run(ctx, []string{"install", "-d", "-m", "700", dir}, nil); err != nil {
		return result, err
	}
	if err = write(ctx, transport, result.Jar, jar); err != nil {
		return result, err
	}
	checksumArgs := []string{"sha256sum", result.Jar}
	if strings.TrimSpace(string(platform)) == "Darwin" {
		checksumArgs = []string{"shasum", "-a", "256", result.Jar}
	}
	checksum, err := transport.Run(ctx, checksumArgs, nil)
	if err != nil {
		return result, err
	}
	if !strings.HasPrefix(string(checksum), result.SHA256+" ") {
		return result, fmt.Errorf("staged agent checksum does not match the verified artifact")
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return result, err
	}
	secret := hex.EncodeToString(token)
	if o.TokenFile != "" {
		data, readErr := os.ReadFile(o.TokenFile)
		if readErr != nil {
			return result, readErr
		}
		secret = strings.TrimSpace(string(data))
		if len(secret) < 16 {
			return result, fmt.Errorf("agent token is invalid")
		}
	}
	config := map[string]any{"token": secret, "service": o.Service, "allowMutation": o.AllowMutation}
	if o.ExtensionsDir != "" {
		config["extensionsDir"] = o.ExtensionsDir
	}
	installIdentity := sha256.Sum256(append([]byte(fmt.Sprintf("%+v/%d/", o.Destination, o.PID)), identity...))
	installationID := hex.EncodeToString(installIdentity[:])
	config["resource"] = map[string]string{"telltale.install.id": installationID, "telltale.artifact.sha256": result.SHA256, "telltale.hub.url": o.HubURL, "telltale.allowMutation": strconv.FormatBool(o.AllowMutation), "telltale.extensionsDir": o.ExtensionsDir}
	if o.HubURL != "" {
		config["url"] = o.HubURL
	}
	if o.HTTP != nil {
		config["http"] = o.HTTP
	}
	if o.Destination.Kind == "local" {
		root, rootErr := os.UserConfigDir()
		if rootErr != nil {
			return result, rootErr
		}
		config["discovery"] = filepath.Join(root, "telltale", "agents", strconv.Itoa(o.PID)+".json")
	}
	body, err := json.Marshal(config)
	if err != nil {
		return result, err
	}
	configPath := dir + "/agent-" + strconv.Itoa(o.PID) + ".json"
	if err = write(ctx, transport, configPath, body); err != nil {
		return result, err
	}
	current, err := processIdentity(ctx, transport, linux, o.PID)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(identity, current) {
		return result, fmt.Errorf("target process identity changed before attachment")
	}
	_, err = transport.Run(ctx, i.Runtime.Attach(Attachment{PID: o.PID, Artifact: result.Jar, Config: configPath, JavaHome: javaHome}), nil)
	if err != nil {
		return result, fmt.Errorf("attach Java agent (requires compatible JDK Attach API and permission to attach): %w", err)
	}
	if o.Destination.Kind != "local" && o.HubURL == "" {
		// A hub-less agent is ready when its listener answers, which only the
		// caller can reach: the deployer reads its /health.
		return result, nil
	}
	return await(ctx, o, result, installationID)
}

// processIdentity tells pid's process from a later one reusing the pid: its
// start time. Linux reads it from /proc/<pid>/stat (field 22, counted after
// the parenthesised command, which may hold spaces), since slim JVM images
// ship no ps; elsewhere ps reports it.
func processIdentity(ctx context.Context, transport Transport, linux bool, pid int) ([]byte, error) {
	args := []string{"ps", "-o", "lstart=", "-p", strconv.Itoa(pid)}
	if linux {
		args = []string{"cat", "/proc/" + strconv.Itoa(pid) + "/stat"}
	}
	out, err := transport.Run(ctx, args, nil)
	if err != nil {
		return nil, fmt.Errorf("inspect target JVM pid %d: %w", pid, err)
	}
	if !linux {
		if len(bytes.TrimSpace(out)) == 0 {
			return nil, fmt.Errorf("target JVM pid %d does not exist", pid)
		}
		return out, nil
	}
	end := bytes.LastIndexByte(out, ')')
	// After the command: state is field 3, so start time (22) is the 20th.
	if fields := strings.Fields(string(out[end+1:])); end >= 0 && len(fields) >= 20 {
		return []byte(fields[19]), nil
	}
	return nil, fmt.Errorf("target JVM pid %d: unreadable /proc stat %q", pid, strings.TrimSpace(string(out)))
}

// targetJavaHome is the JDK a Linux JVM runs from, read from its executable,
// so the attach helper is that JDK's own java (and, for Java 8, finds its
// tools.jar). A Java 8 JDK's jre/bin/java belongs to the JDK above jre.
func targetJavaHome(ctx context.Context, transport Transport, pid int) (string, error) {
	out, err := transport.Run(ctx, []string{"readlink", "/proc/" + strconv.Itoa(pid) + "/exe"}, nil)
	if err != nil {
		return "", fmt.Errorf("find the JDK of JVM pid %d (or pass --java-home): %w", pid, err)
	}
	exe := strings.TrimSpace(string(out))
	home, ok := strings.CutSuffix(exe, "/bin/java")
	if !ok || !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("JVM pid %d runs %q, not a JDK's bin/java: pass --java-home", pid, exe)
	}
	return strings.TrimSuffix(home, "/jre"), nil
}

func write(ctx context.Context, t Transport, path string, data []byte) error {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	dir, name := posixpath.Split(path)
	stagedName := name + ".stage-" + hex.EncodeToString(nonce[:])
	staged := dir + stagedName
	archive, err := tarFile(stagedName, data)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = t.Run(cleanup, []string{"rm", "-f", staged}, nil)
	}()
	// A tar stream, as kubectl cp sends one: in a pod, a bare `dd of=` can read
	// end of input before the exec attaches its stdin, staging an empty file.
	if _, err := t.Run(ctx, []string{"tar", "-x", "-m", "--no-same-owner", "-f", "-", "-C", dir}, bytes.NewReader(archive)); err != nil {
		return err
	}
	_, err = t.Run(ctx, []string{"mv", "-f", staged, path}, nil)
	return err
}

// tarFile is an archive of one private file.
func tarFile(name string, data []byte) ([]byte, error) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Now()}); err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

func await(ctx context.Context, o Options, result Result, installationID string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("agent attached but readiness was not verified: %w", ctx.Err())
		case <-ticker.C:
			if o.Destination.Kind == "local" {
				root, err := os.UserConfigDir()
				if err != nil {
					return result, err
				}
				endpoint, err := protocol.ReadEndpoint(ctx, filepath.Join(root, "telltale", "agents", strconv.Itoa(o.PID)+".json"))
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					return result, fmt.Errorf("agent readiness: %w", err)
				}
				result.Agent = strconv.Itoa(endpoint.PID)
				return result, nil
			}
			infos, err := remoteAgents(ctx, o.HubURL)
			if err != nil {
				return result, err
			}
			for _, info := range infos {
				if info.Resource["telltale.install.id"] == installationID && info.Resource["telltale.artifact.sha256"] == result.SHA256 {
					result.Agent = info.AgentID
					return result, nil
				}
			}
		}
	}
}

func remoteAgents(ctx context.Context, address string) ([]protocol.Hello, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(address, "/")+"/api/agents", nil)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv("TELLTALE_API_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent readiness HTTP %d", response.StatusCode)
	}
	var infos []protocol.Hello
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&infos)
	return infos, err
}
