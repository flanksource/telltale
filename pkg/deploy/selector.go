package deploy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// Target is one selected pod's JVM container.
type Target struct {
	// ID names the target to its agent: the audience of its tokens and the
	// identity a capture selects it by.
	ID        string
	Pod       *corev1.Pod
	Container corev1.Container
}

// TargetID is the target id of a pod a deployer selected.
func TargetID(deployer, namespace, pod string) string {
	return "deployer/" + deployer + "/" + namespace + "/" + pod
}

func (s Selector) validate() error {
	switch {
	case s.Namespace == "":
		return errors.New("selector namespace is required")
	case len(s.Labels) == 0:
		return errors.New("selector labels are required")
	case s.Container == "":
		return errors.New("selector container is required")
	}
	return nil
}

// Resolve is the running pods selector picks, sorted by name.
func Resolve(ctx context.Context, client kubernetes.Interface, deployer string, selector Selector) ([]Target, error) {
	if err := selector.validate(); err != nil {
		return nil, err
	}
	podSelectors, err := podSelectors(ctx, client, selector)
	if err != nil {
		return nil, err
	}
	pods := map[string]*corev1.Pod{}
	for _, podSelector := range podSelectors {
		list, err := client.CoreV1().Pods(selector.Namespace).List(ctx, metav1.ListOptions{LabelSelector: podSelector})
		if err != nil {
			return nil, fmt.Errorf("list pods in %s matching %s: %w", selector.Namespace, podSelector, err)
		}
		for i := range list.Items {
			if list.Items[i].Status.Phase == corev1.PodRunning {
				pods[list.Items[i].Name] = &list.Items[i]
			}
		}
	}
	if len(pods) == 0 {
		return nil, fmt.Errorf("selector %s %s in %s matched no running pods", selector.Kind, labels.SelectorFromSet(selector.Labels), selector.Namespace)
	}
	return targets(deployer, selector.Container, pods)
}

func targets(deployer, container string, pods map[string]*corev1.Pod) ([]Target, error) {
	var result []Target
	for _, name := range slices.Sorted(maps.Keys(pods)) {
		pod := pods[name]
		index := slices.IndexFunc(pod.Spec.Containers, func(c corev1.Container) bool { return c.Name == container })
		if index < 0 {
			return nil, fmt.Errorf("pod %s/%s has no container %q", pod.Namespace, pod.Name, container)
		}
		result = append(result, Target{ID: TargetID(deployer, pod.Namespace, pod.Name), Pod: pod, Container: pod.Spec.Containers[index]})
	}
	return result, nil
}

// podSelectors are the label selectors of the pods the workloads own.
func podSelectors(ctx context.Context, client kubernetes.Interface, selector Selector) ([]string, error) {
	workloads := metav1.ListOptions{LabelSelector: labels.SelectorFromSet(selector.Labels).String()}
	switch selector.Kind {
	case WorkloadPod:
		return []string{workloads.LabelSelector}, nil
	case WorkloadDeployment:
		list, err := client.AppsV1().Deployments(selector.Namespace).List(ctx, workloads)
		if err != nil {
			return nil, fmt.Errorf("list deployments: %w", err)
		}
		return workloadSelectors(len(list.Items), func(i int) (string, *metav1.LabelSelector) { return list.Items[i].Name, list.Items[i].Spec.Selector })
	case WorkloadStatefulSet:
		list, err := client.AppsV1().StatefulSets(selector.Namespace).List(ctx, workloads)
		if err != nil {
			return nil, fmt.Errorf("list statefulsets: %w", err)
		}
		return workloadSelectors(len(list.Items), func(i int) (string, *metav1.LabelSelector) { return list.Items[i].Name, list.Items[i].Spec.Selector })
	default:
		return nil, fmt.Errorf("unsupported selector kind %q (supported: %s)", selector.Kind, strings.Join([]string{string(WorkloadDeployment), string(WorkloadStatefulSet), string(WorkloadPod)}, ", "))
	}
}

func workloadSelectors(count int, item func(int) (string, *metav1.LabelSelector)) ([]string, error) {
	result := make([]string, 0, count)
	for i := range count {
		name, selector := item(i)
		parsed, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			return nil, fmt.Errorf("workload %s pod selector: %w", name, err)
		}
		if parsed.Empty() {
			return nil, fmt.Errorf("workload %s has an empty pod selector", name)
		}
		result = append(result, parsed.String())
	}
	return result, nil
}
