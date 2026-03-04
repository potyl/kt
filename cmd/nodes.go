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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var nodesWatchInterval float64

var nodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "List nodes",
	RunE:  runNodes,
}

func init() {
	rootCmd.AddCommand(nodesCmd)
	nodesCmd.Flags().Float64VarP(&nodesWatchInterval, "watch", "w", 0, "refresh interval in seconds (0 = run once)")
}

func runNodes(_ *cobra.Command, _ []string) error {
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

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	if nodesWatchInterval <= 0 {
		return displayNodes(clientSet, dynamicClient, os.Stdout)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var lastOutput []byte
	for {
		var buf bytes.Buffer
		if err := displayNodes(clientSet, dynamicClient, &buf); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			lastOutput = buf.Bytes()
		}
		fmt.Print("\033[2J\033[H")
		fmt.Printf("Every %.1fs: kt nodes    %s\n\n", nodesWatchInterval, time.Now().Format("Mon Jan 2 15:04:05 2006"))
		os.Stdout.Write(lastOutput)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(float64(time.Second) * nodesWatchInterval)):
		}
	}
}

func displayNodes(clientSet *kubernetes.Clientset, dynamicClient dynamic.Interface, out io.Writer) error {
	nodes, err := clientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	sort.Slice(nodes.Items, func(i, j int) bool {
		return nodes.Items[i].Name < nodes.Items[j].Name
	})

	type nodeRow struct {
		name, arch, nodepool, instance, cpus, memory, pods, age, osImage string
	}

	nodepoolCounts := map[string]int{}
	nodepoolCPUs := map[string]int64{}
	nodepoolMemBytes := map[string]int64{}
	nodepoolPods := map[string]int64{}
	rows := make([]nodeRow, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		np := n.Labels["karpenter.sh/nodepool"]
		if np == "" {
			np = "<none>"
		}
		nodepoolCounts[np]++
		nodepoolCPUs[np] += n.Status.Capacity.Cpu().Value()
		nodepoolMemBytes[np] += n.Status.Capacity.Memory().Value()
		nodepoolPods[np] += n.Status.Capacity.Pods().Value()
		memBytes := n.Status.Capacity.Memory().Value()
		rows = append(rows, nodeRow{
			name:     n.Name,
			arch:     n.Labels["kubernetes.io/arch"],
			nodepool: np,
			instance: n.Labels["node.kubernetes.io/instance-type"],
			cpus:     fmt.Sprintf("%d", n.Status.Capacity.Cpu().Value()),
			memory:   fmt.Sprintf("%dGi", memBytes>>30),
			pods:     fmt.Sprintf("%d", n.Status.Capacity.Pods().Value()),
			age:      humanDuration(time.Since(n.CreationTimestamp.Time)),
			osImage:  n.Status.NodeInfo.OSImage,
		})
	}

	if len(rows) > 0 {
		w := [9]int{len("NODE"), len("ARCH"), len("NODEPOOL"), len("INSTANCE"), len("CPUS"), len("MEMORY"), len("PODS"), len("AGE"), len("OS IMAGE")}
		for _, r := range rows {
			w[0] = max(w[0], len(r.name))
			w[1] = max(w[1], len(r.arch))
			w[2] = max(w[2], len(r.nodepool))
			w[3] = max(w[3], len(r.instance))
			w[4] = max(w[4], len(r.cpus))
			w[5] = max(w[5], len(r.memory))
			w[6] = max(w[6], len(r.pods))
			w[7] = max(w[7], len(r.age))
			w[8] = max(w[8], len(r.osImage))
		}

		rowFmt := fmt.Sprintf("%%-%ds  %%s  %%s  %%-%ds  %%%ds  %%%ds  %%%ds  %%-%ds  %%s\n", w[0], w[3], w[4], w[5], w[6], w[7])

		fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s  %s  %s  %s\n",
			colorBlue(fmt.Sprintf("%-*s", w[0], "NODE")),
			colorBlue(fmt.Sprintf("%-*s", w[1], "ARCH")),
			colorBlue(fmt.Sprintf("%-*s", w[2], "NODEPOOL")),
			colorBlue(fmt.Sprintf("%-*s", w[3], "INSTANCE")),
			colorBlue(fmt.Sprintf("%*s", w[4], "CPUS")),
			colorBlue(fmt.Sprintf("%*s", w[5], "MEMORY")),
			colorBlue(fmt.Sprintf("%*s", w[6], "PODS")),
			colorBlue(fmt.Sprintf("%-*s", w[7], "AGE")),
			colorBlue("OS IMAGE"),
		)
		for _, r := range rows {
			fmt.Fprintf(out, rowFmt,
				r.name,
				archColor(r.arch, w[1]),
				nodepoolColor(r.nodepool, w[2]),
				r.instance, r.cpus, r.memory, r.pods, r.age, r.osImage,
			)
		}
		fmt.Fprintln(out)
	}

	// Nodepools section via Karpenter API
	nodepoolGVR := schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}
	npList, err := dynamicClient.Resource(nodepoolGVR).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		// Karpenter may not be installed; skip gracefully
		return nil
	}

	type npRow struct {
		name, nodeclass, nodes, cpus, memory, pods, ready, age string
	}

	npRows := make([]npRow, 0, len(npList.Items))
	for _, np := range npList.Items {
		name := np.GetName()

		nodeclass := ""
		if spec, ok := np.Object["spec"].(map[string]any); ok {
			if tmpl, ok := spec["template"].(map[string]any); ok {
				if s, ok := tmpl["spec"].(map[string]any); ok {
					if ref, ok := s["nodeClassRef"].(map[string]any); ok {
						nodeclass, _ = ref["name"].(string)
					}
				}
			}
		}

		ready := "False"
		if status, ok := np.Object["status"].(map[string]any); ok {
			if conditions, ok := status["conditions"].([]any); ok {
				for _, c := range conditions {
					cm, ok := c.(map[string]any)
					if !ok {
						continue
					}
					if cm["type"] == "Ready" {
						if s, ok := cm["status"].(string); ok {
							ready = s
						}
					}
				}
			}
		}

		npRows = append(npRows, npRow{
			name:      name,
			nodeclass: nodeclass,
			nodes:     fmt.Sprintf("%d", nodepoolCounts[name]),
			cpus:      fmt.Sprintf("%d", nodepoolCPUs[name]),
			memory:    fmt.Sprintf("%dGi", nodepoolMemBytes[name]>>30),
			pods:      fmt.Sprintf("%d", nodepoolPods[name]),
			ready:     ready,
			age:       humanDuration(time.Since(np.GetCreationTimestamp().Time)),
		})
	}

	sort.Slice(npRows, func(i, j int) bool {
		return npRows[i].name < npRows[j].name
	})

	if len(npRows) > 0 {
		wn := [8]int{len("NODEPOOL"), len("NODECLASS"), len("NODES"), len("CPUS"), len("MEMORY"), len("PODS"), len("READY"), len("AGE")}
		for _, r := range npRows {
			wn[0] = max(wn[0], len(r.name))
			wn[1] = max(wn[1], len(r.nodeclass))
			wn[2] = max(wn[2], len(r.nodes))
			wn[3] = max(wn[3], len(r.cpus))
			wn[4] = max(wn[4], len(r.memory))
			wn[5] = max(wn[5], len(r.pods))
			wn[6] = max(wn[6], len(r.ready))
			wn[7] = max(wn[7], len(r.age))
		}

		rowFmt := fmt.Sprintf("%%s  %%-%ds  %%%ds  %%%ds  %%%ds  %%%ds  %%-%ds  %%s\n", wn[1], wn[2], wn[3], wn[4], wn[5], wn[6])

		fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s  %s  %s\n",
			colorBlue(fmt.Sprintf("%-*s", wn[0], "NODEPOOL")),
			colorBlue(fmt.Sprintf("%-*s", wn[1], "NODECLASS")),
			colorBlue(fmt.Sprintf("%*s", wn[2], "NODES")),
			colorBlue(fmt.Sprintf("%*s", wn[3], "CPUS")),
			colorBlue(fmt.Sprintf("%*s", wn[4], "MEMORY")),
			colorBlue(fmt.Sprintf("%*s", wn[5], "PODS")),
			colorBlue(fmt.Sprintf("%-*s", wn[6], "READY")),
			colorBlue("AGE"),
		)
		for _, r := range npRows {
			fmt.Fprintf(out, rowFmt,
				nodepoolColor(r.name, wn[0]),
				r.nodeclass, r.nodes, r.cpus, r.memory, r.pods, r.ready, r.age,
			)
		}
	}

	return nil
}
