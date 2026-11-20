package deploy

import (
	"errors"
	"fmt"
	"strings"
	"text/template"
)

// PodData is what a URL template sees of the selected pod.
type PodData struct {
	Name      string
	Namespace string
	Labels    map[string]string
	IP        string
}

// templateData binds .Pod, .Container and .Port "<name>".
type templateData struct {
	Pod       PodData
	Container string
	target    Target
}

// Port is the container port named name, an error when there is none.
func (d templateData) Port(name string) (int32, error) {
	for _, port := range d.target.Container.Ports {
		if port.Name == name {
			return port.ContainerPort, nil
		}
	}
	return 0, fmt.Errorf("container %q of pod %s/%s has no port named %q", d.Container, d.Pod.Namespace, d.Pod.Name, name)
}

// RenderURL renders a javaContainer URL template for target. A binding the
// target lacks is an error, never `<no value>`.
func RenderURL(text string, target Target) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("url template is required")
	}
	parsed, err := template.New("url").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse url template %q: %w", text, err)
	}
	pod := target.Pod
	data := templateData{Pod: PodData{Name: pod.Name, Namespace: pod.Namespace, Labels: pod.Labels, IP: pod.Status.PodIP}, Container: target.Container.Name, target: target}
	var out strings.Builder
	if err := parsed.Execute(&out, data); err != nil {
		return "", fmt.Errorf("render url template %q for %s: %w", text, target.ID, err)
	}
	return out.String(), nil
}
