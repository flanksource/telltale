package telltale

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var containerID = regexp.MustCompile(`(?:^|[^a-f0-9])([a-f0-9]{64})(?:[^a-f0-9]|$)`)

// Resource detects local process, container, Kubernetes and cloud attributes.
func Resource() map[string]string { return detectResource(os.Getenv, os.ReadFile) }

func resourceWithCloud(getenv func(string) string, readFile func(string) ([]byte, error), enabled bool, timeout time.Duration) (map[string]string, string) {
	resource := detectResource(getenv, readFile)
	if !enabled {
		return resource, ""
	}
	client := &http.Client{Timeout: timeout, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	if getenv("ECS_CONTAINER_METADATA_URI_V4") == "" {
		if resource["cloud.provider"] == "aws" {
			return awsMetadata(resource, client, timeout)
		}
		return otherMetadata(resource, client)
	}
	return ecsMetadata(resource, client, getenv("ECS_CONTAINER_METADATA_URI_V4"))
}

func otherMetadata(resource map[string]string, client *http.Client) (map[string]string, string) {
	var endpoint, header string
	switch resource["cloud.provider"] {
	case "gcp":
		endpoint, header = "http://metadata.google.internal/computeMetadata/v1/project/project-id", "Metadata-Flavor"
	case "azure":
		endpoint, header = "http://169.254.169.254/metadata/instance/compute?api-version=2021-02-01", "Metadata"
	default:
		return resource, ""
	}
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	if header == "Metadata" {
		request.Header.Set(header, "true")
	} else {
		request.Header.Set(header, "Google")
	}
	response, err := client.Do(request)
	if err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return resource, fmt.Sprintf("cloud metadata: HTTP %s", response.Status)
	}
	if header == "Metadata-Flavor" {
		data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return resource, fmt.Sprintf("cloud metadata: %v", err)
		}
		resource["cloud.account.id"] = strings.TrimSpace(string(data))
	} else {
		var compute struct {
			Location       string `json:"location"`
			SubscriptionID string `json:"subscriptionId"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&compute); err != nil {
			return resource, fmt.Sprintf("cloud metadata: %v", err)
		}
		if compute.Location != "" {
			resource["cloud.region"] = compute.Location
		}
		if compute.SubscriptionID != "" {
			resource["cloud.account.id"] = compute.SubscriptionID
		}
	}
	return resource, ""
}

func awsMetadata(resource map[string]string, client *http.Client, timeout time.Duration) (map[string]string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://169.254.169.254/latest/api/token", nil)
	if err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	request.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
	response, err := client.Do(request)
	if err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	token, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		return resource, fmt.Sprintf("cloud metadata: token request failed: %v (%s)", err, response.Status)
	}
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, "http://169.254.169.254/latest/dynamic/instance-identity/document", nil)
	if err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	request.Header.Set("X-aws-ec2-metadata-token", string(token))
	response, err = client.Do(request)
	if err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return resource, fmt.Sprintf("cloud metadata: HTTP %s", response.Status)
	}
	var identity struct {
		Region    string `json:"region"`
		AccountID string `json:"accountId"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&identity); err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	if identity.Region != "" {
		resource["cloud.region"] = identity.Region
	}
	if identity.AccountID != "" {
		resource["cloud.account.id"] = identity.AccountID
	}
	return resource, ""
}

func ecsMetadata(resource map[string]string, client *http.Client, address string) (map[string]string, string) {
	response, err := client.Get(strings.TrimRight(address, "/") + "/task")
	if err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return resource, fmt.Sprintf("cloud metadata: HTTP %s", response.Status)
	}
	var task struct {
		Cluster string
		TaskARN string
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&task); err != nil {
		return resource, fmt.Sprintf("cloud metadata: %v", err)
	}
	if task.Cluster != "" {
		resource["aws.ecs.cluster.name"] = task.Cluster
	}
	if task.TaskARN != "" {
		resource["aws.ecs.task.arn"] = task.TaskARN
		parts := strings.Split(task.TaskARN, ":")
		if len(parts) > 4 {
			resource["cloud.region"], resource["cloud.account.id"] = parts[3], parts[4]
		}
	}
	return resource, ""
}

func detectResource(getenv func(string) string, readFile func(string) ([]byte, error)) map[string]string {
	resource := map[string]string{"process.pid": strconv.Itoa(os.Getpid()), "process.runtime.name": "go", "process.runtime.version": runtime.Version()}
	if host, err := os.Hostname(); err == nil {
		resource["host.name"] = host
	}
	keys := map[string]string{
		"OTEL_SERVICE_NAME": "service.name", "POD_NAME": "k8s.pod.name", "POD_UID": "k8s.pod.uid", "POD_NAMESPACE": "k8s.namespace.name", "NODE_NAME": "k8s.node.name", "CONTAINER_NAME": "k8s.container.name", "AWS_REGION": "cloud.region", "AWS_ACCOUNT_ID": "cloud.account.id",
	}
	for key, attr := range keys {
		if value := getenv(key); value != "" {
			resource[attr] = value
		}
	}
	if resource["k8s.namespace.name"] == "" {
		if data, err := readFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil && strings.TrimSpace(string(data)) != "" {
			resource["k8s.namespace.name"] = strings.TrimSpace(string(data))
		}
	}
	if data, err := readFile("/proc/self/cgroup"); err == nil {
		if match := containerID.FindStringSubmatch(string(data)); len(match) == 2 {
			resource["container.id"] = match[1]
		}
	}
	switch {
	case getenv("ECS_CONTAINER_METADATA_URI_V4") != "":
		resource["cloud.provider"], resource["cloud.platform"] = "aws", "aws_ecs"
	case getenv("AWS_REGION") != "":
		resource["cloud.provider"] = "aws"
	case getenv("K_SERVICE") != "":
		resource["cloud.provider"], resource["cloud.platform"] = "gcp", "gcp_cloud_run"
		if project := getenv("GOOGLE_CLOUD_PROJECT"); project != "" {
			resource["cloud.account.id"] = project
		}
	case getenv("GOOGLE_CLOUD_PROJECT") != "":
		resource["cloud.provider"], resource["cloud.account.id"] = "gcp", getenv("GOOGLE_CLOUD_PROJECT")
	case getenv("WEBSITE_SITE_NAME") != "":
		resource["cloud.provider"], resource["cloud.platform"] = "azure", "azure_app_service"
	}
	return resource
}
