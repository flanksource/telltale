package deploy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/flanksource/telltale/pkg/agentclient"
)

// Store keeps applied deployers with the status their last deploy reported,
// so a later process connects to their targets without deploying again.
type Store struct{ Dir string }

// deployerName is a Kubernetes object name, which is also a file name.
var deployerName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

func (s Store) path(name string) (string, error) {
	if s.Dir == "" {
		return "", errors.New("deployer store: a directory is required")
	}
	if !deployerName.MatchString(name) {
		return "", fmt.Errorf("invalid deployer name %q", name)
	}
	return filepath.Join(s.Dir, name+".yaml"), nil
}

// Save writes deployer, replacing the one stored under its name.
func (s Store) Save(deployer Deployer) error {
	path, err := s.path(deployer.Name)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(deployer)
	if err != nil {
		return fmt.Errorf("save deployer %s: %w", deployer.Name, err)
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return fmt.Errorf("save deployer %s: %w", deployer.Name, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save deployer %s: %w", deployer.Name, err)
	}
	return nil
}

// Load is the deployer stored under name.
func (s Store) Load(name string) (*Deployer, error) {
	path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("deployer %q has not been deployed: run telltale jvm deploy -f <deployer.yaml>", name)
	}
	if err != nil {
		return nil, err
	}
	deployer, err := Decode(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if deployer.Name != name {
		return nil, fmt.Errorf("%s holds deployer %q, not %q", path, deployer.Name, name)
	}
	return deployer, nil
}

// List is every stored deployer, by name.
func (s Store) List() ([]Deployer, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var deployers []Deployer
	for _, entry := range entries {
		name, isDeployer := strings.CutSuffix(entry.Name(), ".yaml")
		if entry.IsDir() || !isDeployer {
			continue
		}
		deployer, err := s.Load(name)
		if err != nil {
			return nil, err
		}
		deployers = append(deployers, *deployer)
	}
	slices.SortFunc(deployers, func(a, b Deployer) int { return strings.Compare(a.Name, b.Name) })
	return deployers, nil
}

// Known is every deployed target of the stored deployers, as agents a lookup
// can find by deployer, pod or namespace/pod before they are connected.
func (s Store) Known() ([]agentclient.Known, error) {
	deployers, err := s.List()
	if err != nil {
		return nil, err
	}
	var known []agentclient.Known
	for _, deployer := range deployers {
		for _, target := range deployer.Status.Targets {
			if target.State != StateDeployed {
				continue
			}
			known = append(known, agentclient.Known{ID: target.Target, Runtime: "java", Names: []string{deployer.Name, target.Pod, target.Namespace + "/" + target.Pod},
				Resource: targetResource(deployer.Name, target), State: string(target.State), Endpoint: target.Endpoint})
		}
	}
	return known, nil
}
