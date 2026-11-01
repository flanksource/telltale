package prometheus

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

// session names the run and stream a capture's targets are labelled with.
type session struct{ run, stream string }

// targetGroup is one file_sd target group.
type targetGroup struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// targetGroups renders params as the file_sd target groups of s: one per
// endpoint, carrying its scheme, path, query params, scrape interval and
// timeout as the labels Prometheus reads them from, then the params' labels
// and telltale's own.
func targetGroups(params Params, s session) ([]byte, error) {
	interval, timeout, err := params.timing()
	if err != nil {
		return nil, err
	}
	groups := make([]targetGroup, 0, len(params.Endpoints))
	for _, raw := range params.Endpoints {
		e, err := parseEndpoint(raw)
		if err != nil {
			return nil, err
		}
		labels := maps.Clone(params.Labels)
		if labels == nil {
			labels = map[string]string{}
		}
		maps.Copy(labels, map[string]string{
			"__scheme__": e.scheme, "__metrics_path__": e.path,
			"__scrape_interval__": promDuration(interval), "__scrape_timeout__": promDuration(timeout),
			LabelEndpoint: e.url, LabelRun: s.run, LabelStream: s.stream,
		})
		for name, value := range e.params {
			labels["__param_"+name] = value
		}
		groups = append(groups, targetGroup{Targets: []string{e.address}, Labels: labels})
	}
	rendered, err := json.MarshalIndent(groups, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render file_sd target groups: %w", err)
	}
	return append(rendered, '\n'), nil
}

// writeTargets writes a stream's target groups to <dir>/<stream>.json through
// a temp file Prometheus's *.json glob never matches, so it never reads half a
// file.
func writeTargets(dir, stream string, groups []byte) (string, error) {
	temp, err := os.CreateTemp(dir, "."+stream+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("write file_sd targets: %w", err)
	}
	_, writeErr := temp.Write(groups)
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return "", errors.Join(fmt.Errorf("write file_sd targets %s: %w", temp.Name(), err), os.Remove(temp.Name()))
	}
	path := filepath.Join(dir, stream+".json")
	if err := os.Rename(temp.Name(), path); err != nil {
		return "", errors.Join(fmt.Errorf("write file_sd targets %s: %w", path, err), os.Remove(temp.Name()))
	}
	return path, nil
}
