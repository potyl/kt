package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fatih/color"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var healthyStatuses = map[string]bool{
	"ContainerCreating": true,
	"Pending":           true,
	"PodInitializing":   true,
	"Running":           true,
	"Succeeded":         true,
	"Terminating":       true,
	"Completed":         true,
}

const usage = `kt - list pods across all namespaces

Usage:
  kt [flags]

Flags:
  --context string        kubectl context to use (default: current context)
  -n, --namespace string  namespace to list pods from (default: all namespaces)
  -h, --help              show this help message
`

func main() {
	var kubeContext, namespace string
	args := os.Args[1:]
	for i, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Print(usage)
			os.Exit(0)
		case "--context":
			if i+1 < len(args) {
				kubeContext = args[i+1]
			}
		case "-n", "--namespace":
			if i+1 < len(args) {
				namespace = args[i+1]
			}
		}
	}

	kubeConfig := filepath.Join(homeDir(), ".kube", "config")
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeConfig}
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		panic(err.Error())
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	pods, err := clientSet.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	red := color.New(color.FgRed, color.Bold).SprintFunc()

	type podRow struct {
		namespace, name, ready, status, restarts, age string
	}

	statusCounts := map[string]int{}
	var rows []podRow
	for _, pod := range pods.Items {
		status := podStatus(pod)
		statusCounts[status]++
		if !healthyStatuses[status] {
			rows = append(rows, podRow{
				namespace: pod.Namespace,
				name:      pod.Name,
				ready:     podReady(pod),
				status:    status,
				restarts:  podRestarts(pod),
				age:       humanDuration(time.Since(pod.CreationTimestamp.Time)),
			})
		}
	}

	if len(rows) > 0 {
		// compute max width per column, seeded with header lengths
		w := [5]int{len("NAMESPACE"), len("NAME"), len("READY"), len("STATUS"), len("RESTARTS")}
		for _, r := range rows {
			w[0] = max(w[0], len(r.namespace))
			w[1] = max(w[1], len(r.name))
			w[2] = max(w[2], len(r.ready))
			w[3] = max(w[3], len(r.status))
			w[4] = max(w[4], len(r.restarts))
		}

		headerFmt := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds  %%-%ds  %%s\n", w[0], w[1], w[2], w[3], w[4])
		rowFmt := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%s  %%-%ds  %%s\n", w[0], w[1], w[2], w[4])

		fmt.Printf(headerFmt, "NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "AGE")
		for _, r := range rows {
			fmt.Printf(
				rowFmt, r.namespace, r.name, r.ready,
				red(fmt.Sprintf("%-*s", w[3], r.status)),
				r.restarts, r.age,
			)
		}
		fmt.Println()
	}

	statuses := make([]string, 0, len(statusCounts))
	for s := range statusCounts {
		statuses = append(statuses, s)
	}
	sort.Strings(statuses)

	fmt.Println("Pods")
	for _, s := range statuses {
		fmt.Printf("  %6d %s\n", statusCounts[s], s)
	}
}

func podReady(pod corev1.Pod) string {
	ready := 0
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, len(pod.Spec.Containers))
}

func podStatus(pod corev1.Pod) string {
	if pod.DeletionTimestamp != nil {
		return "Terminating"
	}
	for i, cs := range pod.Status.InitContainerStatuses {
		if cs.Ready {
			continue
		}
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			return fmt.Sprintf("Init:ExitCode:%d", cs.State.Terminated.ExitCode)
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing" {
			return fmt.Sprintf("Init:%s", cs.State.Waiting.Reason)
		}
		return fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil {
			if cs.State.Terminated.Reason != "" {
				return cs.State.Terminated.Reason
			}
			if cs.State.Terminated.ExitCode != 0 {
				return "Error"
			}
		}
	}
	if pod.Status.Phase != "" {
		return string(pod.Status.Phase)
	}
	return "Unknown"
}

func podRestarts(pod corev1.Pod) string {
	var total int32
	var lastRestart *time.Time
	for _, cs := range pod.Status.ContainerStatuses {
		total += cs.RestartCount
		if cs.LastTerminationState.Terminated != nil {
			t := cs.LastTerminationState.Terminated.FinishedAt.Time
			if lastRestart == nil || t.After(*lastRestart) {
				lastRestart = &t
			}
		}
	}
	if total > 0 && lastRestart != nil {
		return fmt.Sprintf("%d (%s ago)", total, humanDuration(time.Since(*lastRestart)))
	}
	return fmt.Sprintf("%d", total)
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := int(d.Minutes())
	if m < 60 {
		if rem := s % 60; rem != 0 {
			return fmt.Sprintf("%dm%ds", m, rem)
		}
		return fmt.Sprintf("%dm", m)
	}
	h := int(d.Hours())
	if h < 24 {
		if rem := m % 60; rem != 0 {
			return fmt.Sprintf("%dh%dm", h, rem)
		}
		return fmt.Sprintf("%dh", h)
	}
	days := int(d.Hours() / 24)
	if days < 365 {
		if rem := h % 24; rem != 0 {
			return fmt.Sprintf("%dd%dh", days, rem)
		}
		return fmt.Sprintf("%dd", days)
	}
	years := days / 365
	if rem := days % 365; rem != 0 {
		return fmt.Sprintf("%dy%dd", years, rem)
	}
	return fmt.Sprintf("%dy", years)
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic("could not determine home directory")
	}
	return home
}
