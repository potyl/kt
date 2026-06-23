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
	"syscall"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var nodeWatchInterval float64

var nodeCmd = &cobra.Command{
	Use:   "node <name>",
	Short: "Show node info and running pods",
	Args:  cobra.ExactArgs(1),
	RunE:  runNode,
}

func init() {
	rootCmd.AddCommand(nodeCmd)
	nodeCmd.Flags().Float64VarP(&nodeWatchInterval, "watch", "w", 0, "refresh interval in seconds (0 = run once)")
}

func runNode(_ *cobra.Command, args []string) error {
	nodeName := args[0]

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

	if nodeWatchInterval <= 0 {
		return displayNode(clientSet, nodeName, os.Stdout)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var lastOutput []byte
	for {
		var buf bytes.Buffer
		if err := displayNode(clientSet, nodeName, &buf); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			lastOutput = buf.Bytes()
		}
		fmt.Print("\033[2J\033[H")
		ctxName := resolveContextName()
		fmt.Printf("kt node %s context: %s\n\n", nodeName, colorGreen(ctxName))
		os.Stdout.Write(lastOutput)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(float64(time.Second) * nodeWatchInterval)):
		}
	}
}

func displayNode(clientSet *kubernetes.Clientset, nodeName string, out io.Writer) error {
	node, err := clientSet.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node %q: %w", nodeName, err)
	}

	pods, err := clientSet.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return fmt.Errorf("failed to list pods on node %q: %w", nodeName, err)
	}

	events, err := clientSet.CoreV1().Events(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + nodeName + ",involvedObject.kind=Node",
	})
	if err != nil {
		return fmt.Errorf("failed to list events for node %q: %w", nodeName, err)
	}

	internalIP := ""
	hostname := ""
	for _, addr := range node.Status.Addresses {
		switch addr.Type {
		case corev1.NodeInternalIP:
			internalIP = addr.Address
		case corev1.NodeHostName:
			hostname = addr.Address
		}
	}

	label := func(s string) string {
		return colorBlue(fmt.Sprintf("%-24s", s))
	}
	fmt.Fprintf(out, "%s %s\n", label("Name:"), node.Name)
	fmt.Fprintf(out, "%s %s\n", label("Hostname:"), hostname)
	fmt.Fprintf(out, "%s %s\n", label("InternalIP:"), internalIP)
	fmt.Fprintf(out, "%s %s\n", label("Operating System:"), node.Status.NodeInfo.OperatingSystem)
	fmt.Fprintf(out, "%s %s\n", label("Architecture:"), node.Labels["kubernetes.io/arch"])
	fmt.Fprintf(out, "%s %s\n", label("OS Image:"), node.Status.NodeInfo.OSImage)
	fmt.Fprintf(out, "%s %s\n", label("Kernel Version:"), node.Status.NodeInfo.KernelVersion)
	fmt.Fprintf(out, "%s %s\n", label("Container Runtime:"), node.Status.NodeInfo.ContainerRuntimeVersion)
	fmt.Fprintf(out, "%s %s\n", label("Kubelet Version:"), node.Status.NodeInfo.KubeletVersion)

	if len(node.Spec.Taints) == 0 {
		fmt.Fprintf(out, "%s %s\n", label("Taints:"), "<none>")
	} else {
		for i, t := range node.Spec.Taints {
			var taintStr string
			if t.Value != "" {
				taintStr = fmt.Sprintf("%s=%s:%s", t.Key, t.Value, t.Effect)
			} else {
				taintStr = fmt.Sprintf("%s:%s", t.Key, t.Effect)
			}
			if i == 0 {
				fmt.Fprintf(out, "%s %s\n", label("Taints:"), taintStr)
			} else {
				fmt.Fprintf(out, "%s %s\n", label(""), taintStr)
			}
		}
	}
	fmt.Fprintln(out)

	sort.Slice(pods.Items, func(i, j int) bool {
		if pods.Items[i].Namespace != pods.Items[j].Namespace {
			return pods.Items[i].Namespace < pods.Items[j].Namespace
		}
		return pods.Items[i].Name < pods.Items[j].Name
	})

	type podRow struct {
		namespace, name, kind, ready, status, restarts, age, arch, nodepool, instance string
	}

	arch := node.Labels["kubernetes.io/arch"]
	nodepool := node.Labels["karpenter.sh/nodepool"]
	instance := node.Labels["node.kubernetes.io/instance-type"]

	rows := make([]podRow, 0, len(pods.Items))
	for _, pod := range pods.Items {
		rows = append(rows, podRow{
			namespace: pod.Namespace,
			name:      pod.Name,
			kind:      podKind(pod),
			ready:     podReady(pod),
			status:    podStatus(pod),
			restarts:  podRestarts(pod),
			age:       humanDuration(time.Since(pod.CreationTimestamp.Time)),
			arch:      arch,
			nodepool:  nodepool,
			instance:  instance,
		})
	}

	if len(rows) == 0 {
		fmt.Fprintln(out, "No pods running on this node.")
		return nil
	}

	w := [10]int{len("NAMESPACE"), len("POD"), len("KIND"), len("READY"), len("ARCH"), len("NODEPOOL"), len("INSTANCE"), len("STATUS"), len("RESTARTS"), len("AGE")}
	for _, r := range rows {
		w[0] = max(w[0], len(r.namespace))
		w[1] = max(w[1], len(r.name))
		w[2] = max(w[2], len(r.kind))
		w[3] = max(w[3], len(r.ready))
		w[4] = max(w[4], len(r.arch))
		w[5] = max(w[5], len(r.nodepool))
		w[6] = max(w[6], len(r.instance))
		w[7] = max(w[7], len(r.status))
		w[8] = max(w[8], len(r.restarts))
		w[9] = max(w[9], len(r.age))
	}

	rowFmt := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%-%ds  %%s  %%s  %%-%ds  %%s  %%-%ds  %%s\n", w[0], w[1], w[2], w[3], w[6], w[8])

	fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s  %s  %s  %s  %s\n",
		colorBlue(fmt.Sprintf("%-*s", w[0], "NAMESPACE")),
		colorBlue(fmt.Sprintf("%-*s", w[1], "POD")),
		colorBlue(fmt.Sprintf("%-*s", w[2], "KIND")),
		colorBlue(fmt.Sprintf("%-*s", w[3], "READY")),
		colorBlue(fmt.Sprintf("%-*s", w[4], "ARCH")),
		colorBlue(fmt.Sprintf("%-*s", w[5], "NODEPOOL")),
		colorBlue(fmt.Sprintf("%-*s", w[6], "INSTANCE")),
		colorBlue(fmt.Sprintf("%-*s", w[7], "STATUS")),
		colorBlue(fmt.Sprintf("%-*s", w[8], "RESTARTS")),
		colorBlue("AGE"),
	)
	for _, r := range rows {
		fmt.Fprintf(out,
			rowFmt, r.namespace, r.name, r.kind, r.ready,
			archColor(r.arch, w[4]),
			nodepoolColor(r.nodepool, w[5]),
			r.instance,
			statusColor(r.status, w[7]),
			r.restarts, r.age,
		)
	}

	// Events section
	fmt.Fprintln(out)

	sort.Slice(events.Items, func(i, j int) bool {
		return events.Items[i].LastTimestamp.Before(&events.Items[j].LastTimestamp)
	})

	type eventRow struct {
		lastSeen, evType, reason, from, message string
	}

	erows := make([]eventRow, 0, len(events.Items))
	for _, e := range events.Items {
		from := e.Source.Component
		if from == "" {
			from = e.ReportingController
		}
		msg := e.Message
		if len(msg) > 80 {
			msg = msg[:77] + "..."
		}
		lastSeen := "<unknown>"
		if !e.LastTimestamp.IsZero() {
			lastSeen = humanDuration(time.Since(e.LastTimestamp.Time))
		}
		erows = append(erows, eventRow{
			lastSeen: lastSeen,
			evType:   e.Type,
			reason:   e.Reason,
			from:     from,
			message:  msg,
		})
	}

	if len(erows) == 0 {
		fmt.Fprintln(out, "No events for this node.")
		return nil
	}

	ew := [5]int{len("LAST SEEN"), len("TYPE"), len("REASON"), len("FROM"), len("MESSAGE")}
	for _, r := range erows {
		ew[0] = max(ew[0], len(r.lastSeen))
		ew[1] = max(ew[1], len(r.evType))
		ew[2] = max(ew[2], len(r.reason))
		ew[3] = max(ew[3], len(r.from))
		ew[4] = max(ew[4], len(r.message))
	}

	eRowFmt := fmt.Sprintf("%%s  %%-%ds  %%-%ds  %%-%ds  %%s\n", ew[2], ew[3], ew[0])

	fmt.Fprintf(out, "%s  %s  %s  %s  %s\n",
		colorBlue(fmt.Sprintf("%-*s", ew[1], "TYPE")),
		colorBlue(fmt.Sprintf("%-*s", ew[2], "REASON")),
		colorBlue(fmt.Sprintf("%-*s", ew[3], "FROM")),
		colorBlue(fmt.Sprintf("%-*s", ew[0], "LAST SEEN")),
		colorBlue("MESSAGE"),
	)
	for _, r := range erows {
		fmt.Fprintf(out, eRowFmt,
			eventTypeColor(r.evType, ew[1]),
			r.reason,
			r.from,
			r.lastSeen,
			r.message,
		)
	}

	return nil
}

func eventTypeColor(evType string, width int) string {
	padded := fmt.Sprintf("%-*s", width, evType)
	if evType == "Warning" {
		return colorRed(padded)
	}
	return padded
}
