package logs

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"

	dbcontext "github.com/flanksource/commons-db/context"
	dblogs "github.com/flanksource/commons-db/logs"
	kubelog "github.com/flanksource/commons-db/logs/k8s"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func followKubernetes(ctx context.Context, client kubernetes.Interface, p Params, emit func(Log) error) error {
	pods, err := client.CoreV1().Pods(p.Namespace).List(ctx, metav1.ListOptions{LabelSelector: p.PodSelector})
	if err != nil {
		return fmt.Errorf("list kubernetes pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods match %q in namespace %q", p.PodSelector, p.Namespace)
	}
	for _, pod := range pods.Items {
		if p.Container != "" {
			found := false
			for _, container := range pod.Spec.Containers {
				if container.Name == p.Container {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("pod %s/%s has no container %q", pod.Namespace, pod.Name, p.Container)
			}
		}
	}
	followCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	group, groupCtx := errgroup.WithContext(followCtx)
	var mu sync.Mutex
	for _, pod := range pods.Items {
		pod := pod
		group.Go(func() error {
			var emitErr error
			err := kubelog.Follow(dbcontext.NewContext(groupCtx), client, kubelog.FollowOptions{Target: kubelog.Target{Pod: pod, Container: p.Container}, Emit: func(result dblogs.LogResult) {
				for _, line := range result.Logs {
					if line == nil || !strings.Contains(line.Message, p.Match) {
						continue
					}
					resource := map[string]string{"k8s.namespace.name": pod.Namespace, "k8s.pod.name": pod.Name, "k8s.pod.uid": string(pod.UID), "k8s.node.name": pod.Spec.NodeName}
					if container, ok := result.Metadata["container"].(string); ok {
						resource["k8s.container.name"] = container
					}
					line.Labels = maps.Clone(line.Labels)
					mu.Lock()
					if emitErr == nil {
						emitErr = emit(Log{LogLine: *line, Resource: resource})
						if emitErr != nil {
							cancel()
						}
					}
					mu.Unlock()
				}
			}})
			if emitErr != nil {
				return emitErr
			}
			if groupCtx.Err() != nil {
				return nil
			}
			return err
		})
	}
	return group.Wait()
}
