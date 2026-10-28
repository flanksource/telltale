package agentinstall

import (
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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flanksource/telltale/sdk/protocol"
)

type Options struct {
	Destination Destination `json:"destination"`
	PID         int         `json:"pid"`
	Directory   string      `json:"directory"`
	JavaHome    string      `json:"javaHome,omitempty"`
	HubURL      string      `json:"hubUrl,omitempty"`
	TokenFile   string      `json:"tokenFile,omitempty"`
	Service     string      `json:"service"`
	DryRun      bool        `json:"dryRun,omitempty"`
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
	if o.PID <= 0 {
		return fmt.Errorf("a positive target JVM pid is required")
	}
	if !strings.HasPrefix(o.Directory, "/") || strings.ContainsAny(o.Directory, "\x00\n\r") {
		return fmt.Errorf("an absolute installation directory is required")
	}
	if o.Destination.Kind != "local" && (o.HubURL == "" || o.TokenFile == "") {
		return fmt.Errorf("remote installation requires hub URL and agent token file")
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
}
type Runtime interface {
	Artifact() (Artifact, error)
	Attach(Attachment) []string
}
type Installer struct {
	Runtime   Runtime
	Transport Transport
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
			if client.Hello.Resource["telltale.artifact.sha256"] != result.SHA256 || client.Hello.Service != o.Service || client.Hello.Resource["telltale.hub.url"] != o.HubURL {
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
	identity, err := transport.Run(ctx, []string{"ps", "-o", "lstart=", "-p", strconv.Itoa(o.PID)}, nil)
	if err != nil {
		return result, fmt.Errorf("inspect target JVM pid %d: %w", o.PID, err)
	}
	if strings.TrimSpace(string(identity)) == "" {
		return result, fmt.Errorf("target JVM pid %d does not exist", o.PID)
	}
	if _, err = transport.Run(ctx, []string{"install", "-d", "-m", "700", dir}, nil); err != nil {
		return result, err
	}
	if err = write(ctx, transport, result.Jar, jar); err != nil {
		return result, err
	}
	platform, err := transport.Run(ctx, []string{"uname", "-s"}, nil)
	if err != nil {
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
	config := map[string]any{"token": secret, "service": o.Service}
	installIdentity := sha256.Sum256(append([]byte(fmt.Sprintf("%+v/%d/", o.Destination, o.PID)), identity...))
	installationID := hex.EncodeToString(installIdentity[:])
	config["resource"] = map[string]string{"telltale.install.id": installationID, "telltale.artifact.sha256": result.SHA256, "telltale.hub.url": o.HubURL}
	if o.HubURL != "" {
		config["url"] = o.HubURL
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
	current, err := transport.Run(ctx, []string{"ps", "-o", "lstart=", "-p", strconv.Itoa(o.PID)}, nil)
	if err != nil {
		return result, err
	}
	if !bytes.Equal(identity, current) {
		return result, fmt.Errorf("target process identity changed before attachment")
	}
	_, err = transport.Run(ctx, i.Runtime.Attach(Attachment{PID: o.PID, Artifact: result.Jar, Config: configPath}), nil)
	if err != nil {
		return result, fmt.Errorf("attach Java agent (requires compatible JDK Attach API and permission to attach): %w", err)
	}
	return await(ctx, o, result, installationID)
}

func write(ctx context.Context, t Transport, path string, data []byte) error {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	staged := path + ".stage-" + hex.EncodeToString(nonce[:])
	if _, err := t.Run(ctx, []string{"install", "-m", "600", "/dev/null", staged}, nil); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = t.Run(cleanup, []string{"rm", "-f", staged}, nil)
	}()
	if _, err := t.Run(ctx, []string{"dd", "of=" + staged}, bytes.NewReader(data)); err != nil {
		return err
	}
	_, err := t.Run(ctx, []string{"mv", "-f", staged, path}, nil)
	return err
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
