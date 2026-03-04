package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
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

var namespace string
var allPods bool
var watchInterval float64

var podsCmd = &cobra.Command{
	Use:   "pods",
	Short: "List unhealthy pods",
	RunE:  runPods,
}

func init() {
	rootCmd.AddCommand(podsCmd)
	podsCmd.Flags().StringVarP(&namespace, "namespace", "n", "", "namespace to list pods from (default: all namespaces)")
	podsCmd.Flags().BoolVarP(&allPods, "all", "a", false, "list all pods, not just unhealthy ones")
	podsCmd.Flags().Float64VarP(&watchInterval, "watch", "w", 0, "refresh interval in seconds (0 = run once)")
}

func runPods(_ *cobra.Command, _ []string) error {
	kubeConfig := filepath.Join(homeDir(), ".kube", "config")
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeConfig}
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	if watchInterval <= 0 {
		return displayPods(clientSet, os.Stdout)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var lastOutput []byte
	for {
		var buf bytes.Buffer
		if err := displayPods(clientSet, &buf); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			lastOutput = buf.Bytes()
		}
		fmt.Print("\033[2J\033[H")
		fmt.Printf("Every %.1fs: kt pods    %s\n\n", watchInterval, time.Now().Format("Mon Jan 2 15:04:05 2006"))
		os.Stdout.Write(lastOutput)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(float64(time.Second) * watchInterval)):
		}
	}
}

func displayPods(clientSet *kubernetes.Clientset, out io.Writer) error {
	type podListResult struct {
		list *corev1.PodList
		err  error
	}
	type nodeListResult struct {
		list *corev1.NodeList
		err  error
	}

	podsCh := make(chan podListResult, 1)
	nodesCh := make(chan nodeListResult, 1)

	go func() {
		list, err := clientSet.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
		podsCh <- podListResult{list, err}
	}()
	go func() {
		list, err := clientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		nodesCh <- nodeListResult{list, err}
	}()

	podsResult := <-podsCh
	nodesResult := <-nodesCh

	if podsResult.err != nil {
		return fmt.Errorf("failed to list pods: %w", podsResult.err)
	}
	if nodesResult.err != nil {
		return fmt.Errorf("failed to list nodes: %w", nodesResult.err)
	}

	pods := podsResult.list
	nodes := nodesResult.list

	nodeArch := make(map[string]string, len(nodes.Items))
	nodePool := make(map[string]string, len(nodes.Items))
	for _, node := range nodes.Items {
		nodeArch[node.Name] = node.Labels["kubernetes.io/arch"]
		nodePool[node.Name] = node.Labels["karpenter.sh/nodepool"]
	}

	type podRow struct {
		namespace, name, kind, ready, status, restarts, age, arch, nodepool string
	}

	statusCounts := map[string]int{}
	var rows []podRow
	for _, pod := range pods.Items {
		status := podStatus(pod)
		statusCounts[status]++
		if allPods || !healthyStatuses[status] {
			rows = append(rows, podRow{
				namespace: pod.Namespace,
				name:      pod.Name,
				kind:      podKind(pod),
				ready:     podReady(pod),
				status:    status,
				restarts:  podRestarts(pod),
				age:       humanDuration(time.Since(pod.CreationTimestamp.Time)),
				arch:      nodeArch[pod.Spec.NodeName],
				nodepool:  nodePool[pod.Spec.NodeName],
			})
		}
	}

	if len(rows) > 0 {
		w := [9]int{len("NAMESPACE"), len("POD"), len("KIND"), len("READY"), len("ARCH"), len("NODEPOOL"), len("STATUS"), len("RESTARTS"), len("AGE")}
		for _, r := range rows {
			w[0] = max(w[0], len(r.namespace))
			w[1] = max(w[1], len(r.name))
			w[2] = max(w[2], len(r.kind))
			w[3] = max(w[3], len(r.ready))
			w[4] = max(w[4], len(r.arch))
			w[5] = max(w[5], len(r.nodepool))
			w[6] = max(w[6], len(r.status))
			w[7] = max(w[7], len(r.restarts))
			w[8] = max(w[8], len(r.age))
		}

		rowFmt := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds  %%s  %%s  %%s  %%-%ds  %%s\n", w[0], w[1], w[2], w[3], w[7])

		fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s  %s  %s  %s\n",
			colorBlue(fmt.Sprintf("%-*s", w[0], "NAMESPACE")),
			colorBlue(fmt.Sprintf("%-*s", w[1], "POD")),
			colorBlue(fmt.Sprintf("%-*s", w[2], "KIND")),
			colorBlue(fmt.Sprintf("%-*s", w[3], "READY")),
			colorBlue(fmt.Sprintf("%-*s", w[4], "ARCH")),
			colorBlue(fmt.Sprintf("%-*s", w[5], "NODEPOOL")),
			colorBlue(fmt.Sprintf("%-*s", w[6], "STATUS")),
			colorBlue(fmt.Sprintf("%-*s", w[7], "RESTARTS")),
			colorBlue("AGE"),
		)
		for _, r := range rows {
			fmt.Fprintf(out,
				rowFmt, r.namespace, r.name, r.kind, r.ready,
				archColor(r.arch, w[4]),
				nodepoolColor(r.nodepool, w[5]),
				statusColor(r.status, w[6]),
				r.restarts, r.age,
			)
		}
		fmt.Fprintln(out)
	}

	// Summary
	statuses := make([]string, 0, len(statusCounts))
	for s := range statusCounts {
		statuses = append(statuses, s)
	}
	sort.Strings(statuses)

	parts := make([]string, 0, len(statuses))
	for _, s := range statuses {
		name := colorGreen(s)
		if !healthyStatuses[s] {
			name = colorRed(s)
		}
		parts = append(parts, fmt.Sprintf("%s: %d", name, statusCounts[s]))
	}
	fmt.Fprintf(out, "%s\n", strings.Join(parts, ", "))

	return nil
}

func statusColor(status string, width int) string {
	padded := fmt.Sprintf("%-*s", width, status)
	if !healthyStatuses[status] {
		return colorRed(padded)
	}
	return padded
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

func podKind(pod corev1.Pod) string {
	if len(pod.OwnerReferences) == 0 {
		return "Pod"
	}
	switch pod.OwnerReferences[0].Kind {
	case "ReplicaSet":
		return "Deployment"
	default:
		return pod.OwnerReferences[0].Kind
	}
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
