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

	"github.com/dlclark/regexp2"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var nodesWatchInterval float64
var nodesGrepPattern string

var nodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "List nodes",
	RunE:  runNodes,
}

func init() {
	rootCmd.AddCommand(nodesCmd)
	nodesCmd.Flags().Float64VarP(&nodesWatchInterval, "watch", "w", 0, "refresh interval in seconds (0 = run once)")
	nodesCmd.Flags().StringVarP(&nodesGrepPattern, "grep", "g", "", "filter rows by Perl-compatible regexp (matched against the full rendered row)")
}

func runNodes(_ *cobra.Command, _ []string) error {
	var grep *regexp2.Regexp
	if nodesGrepPattern != "" {
		var err error
		grep, err = regexp2.Compile(nodesGrepPattern, regexp2.None)
		if err != nil {
			return fmt.Errorf("invalid --grep pattern: %w", err)
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
		return displayNodes(clientSet, dynamicClient, os.Stdout, grep)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var lastOutput []byte
	for {
		var buf bytes.Buffer
		if err := displayNodes(clientSet, dynamicClient, &buf, grep); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			lastOutput = buf.Bytes()
		}
		fmt.Print("\033[2J\033[H")
		ctxName := resolveContextName()
		fmt.Printf("Every %.1fs: kt nodes context: %s    %s\n\n", nodesWatchInterval, colorGreen(ctxName), time.Now().Format("Mon Jan 2 15:04:05 2006"))
		os.Stdout.Write(lastOutput)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(float64(time.Second) * nodesWatchInterval)):
		}
	}
}

func displayNodes(clientSet *kubernetes.Clientset, dynamicClient dynamic.Interface, out io.Writer, grep *regexp2.Regexp) error {
	nodes, err := clientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	sort.Slice(nodes.Items, func(i, j int) bool {
		return nodes.Items[i].Name < nodes.Items[j].Name
	})

	type nodeRow struct {
		name, arch, autoscaler, nodepool, instance, cpus, memory, pods, age, osImage string
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

		autoscaler := "-"
		if n.Labels["karpenter.sh/nodepool"] != "" {
			autoscaler = "karpenter"
		} else if n.Labels["eks.amazonaws.com/nodegroup"] != "" {
			autoscaler = "managed"
		} else {
			for k := range n.Annotations {
				if strings.HasPrefix(k, "cluster-autoscaler.kubernetes.io/") {
					autoscaler = "autoscaler"
					break
				}
			}
		}

		rows = append(rows, nodeRow{
			name:       n.Name,
			arch:       n.Labels["kubernetes.io/arch"],
			autoscaler: autoscaler,
			nodepool:   np,
			instance:   n.Labels["node.kubernetes.io/instance-type"],
			cpus:       fmt.Sprintf("%d", n.Status.Capacity.Cpu().Value()),
			memory:     fmt.Sprintf("%dGi", memBytes>>30),
			pods:       fmt.Sprintf("%d", n.Status.Capacity.Pods().Value()),
			age:        humanDuration(time.Since(n.CreationTimestamp.Time)),
			osImage:    n.Status.NodeInfo.OSImage,
		})
	}

	if grep != nil {
		filtered := rows[:0]
		for _, r := range rows {
			plain := strings.Join([]string{r.name, r.arch, r.autoscaler, r.nodepool, r.instance, r.cpus, r.memory, r.pods, r.age, r.osImage}, " ")
			if ok, _ := grep.MatchString(plain); ok {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	if len(rows) > 0 {
		w := [10]int{len("NODE"), len("ARCH"), len("AUTOSCALER"), len("NODEPOOL"), len("INSTANCE"), len("CPUS"), len("MEMORY"), len("PODS"), len("AGE"), len("OS IMAGE")}
		for _, r := range rows {
			w[0] = max(w[0], len(r.name))
			w[1] = max(w[1], len(r.arch))
			w[2] = max(w[2], len(r.autoscaler))
			w[3] = max(w[3], len(r.nodepool))
			w[4] = max(w[4], len(r.instance))
			w[5] = max(w[5], len(r.cpus))
			w[6] = max(w[6], len(r.memory))
			w[7] = max(w[7], len(r.pods))
			w[8] = max(w[8], len(r.age))
			w[9] = max(w[9], len(r.osImage))
		}

		rowFmt := fmt.Sprintf("%%-%ds  %%s  %%-%ds  %%s  %%-%ds  %%%ds  %%%ds  %%%ds  %%-%ds  %%s\n", w[0], w[2], w[4], w[5], w[6], w[7], w[8])

		fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s  %s  %s  %s  %s\n",
			colorBlue(fmt.Sprintf("%-*s", w[0], "NODE")),
			colorBlue(fmt.Sprintf("%-*s", w[1], "ARCH")),
			colorBlue(fmt.Sprintf("%-*s", w[2], "AUTOSCALER")),
			colorBlue(fmt.Sprintf("%-*s", w[3], "NODEPOOL")),
			colorBlue(fmt.Sprintf("%-*s", w[4], "INSTANCE")),
			colorBlue(fmt.Sprintf("%*s", w[5], "CPUS")),
			colorBlue(fmt.Sprintf("%*s", w[6], "MEMORY")),
			colorBlue(fmt.Sprintf("%*s", w[7], "PODS")),
			colorBlue(fmt.Sprintf("%-*s", w[8], "AGE")),
			colorBlue("OS IMAGE"),
		)
		for _, r := range rows {
			fmt.Fprintf(out, rowFmt,
				r.name,
				archColor(r.arch, w[1]),
				autoscalerColor(r.autoscaler, w[2]),
				nodepoolColor(r.nodepool, w[3]),
				r.instance, r.cpus, r.memory, r.pods, r.age, r.osImage,
			)
		}
		fmt.Fprintln(out)
	}

	return displayNodepools(dynamicClient, nodepoolCounts, nodepoolCPUs, nodepoolMemBytes, nodepoolPods, out, grep)
}

// displayNodepools renders the Karpenter nodepool summary table, annotated with
// per-pool node counts and resource totals derived from the live node list.
func displayNodepools(dynamicClient dynamic.Interface, nodepoolCounts map[string]int, nodepoolCPUs, nodepoolMemBytes, nodepoolPods map[string]int64, out io.Writer, grep *regexp2.Regexp) error {
	nodepoolGVR := schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}
	npList, err := dynamicClient.Resource(nodepoolGVR).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		// Karpenter may not be installed; skip gracefully
		return nil
	}

	type npRow struct {
		name, nodeclass, arch, os, capacityType, instanceType string
		instanceCategory, instanceGeneration, instanceCPU     string
		noSchedule                                            string
		nodes, cpus, memory, pods, ready, age                 string
	}

	npRows := make([]npRow, 0, len(npList.Items))
	for _, np := range npList.Items {
		name := np.GetName()

		var nodeclass, arch, osVal, capacityType, instanceType string
		var instanceCategory, instanceGeneration, instanceCPU string
		var noScheduleTaints []string

		if spec, ok := np.Object["spec"].(map[string]any); ok {
			if tmpl, ok := spec["template"].(map[string]any); ok {
				if s, ok := tmpl["spec"].(map[string]any); ok {
					if ref, ok := s["nodeClassRef"].(map[string]any); ok {
						nodeclass, _ = ref["name"].(string)
					}
					if reqs, ok := s["requirements"].([]any); ok {
						for _, r := range reqs {
							rm, ok := r.(map[string]any)
							if !ok {
								continue
							}
							key, _ := rm["key"].(string)
							vals := joinRequirementValues(rm["values"])
							switch key {
							case "kubernetes.io/arch":
								arch = vals
							case "kubernetes.io/os":
								osVal = vals
							case "karpenter.sh/capacity-type":
								capacityType = vals
							case "node.kubernetes.io/instance-type":
								instanceType = vals
							case "karpenter.k8s.aws/instance-category":
								instanceCategory = vals
							case "karpenter.k8s.aws/instance-generation":
								instanceGeneration = vals
							case "karpenter.k8s.aws/instance-cpu":
								instanceCPU = vals
							}
						}
					}
					if taints, ok := s["taints"].([]any); ok {
						for _, t := range taints {
							tm, ok := t.(map[string]any)
							if !ok {
								continue
							}
							if tm["effect"] == "NoSchedule" {
								if v, ok := tm["value"].(string); ok && v != "" {
									noScheduleTaints = append(noScheduleTaints, v)
								}
							}
						}
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
			name:               name,
			nodeclass:          nodeclass,
			arch:               arch,
			os:                 osVal,
			capacityType:       capacityType,
			instanceType:       instanceType,
			instanceCategory:   instanceCategory,
			instanceGeneration: instanceGeneration,
			instanceCPU:        instanceCPU,
			noSchedule:         strings.Join(noScheduleTaints, ","),
			nodes:              fmt.Sprintf("%d", nodepoolCounts[name]),
			cpus:               fmt.Sprintf("%d", nodepoolCPUs[name]),
			memory:             fmt.Sprintf("%dGi", nodepoolMemBytes[name]>>30),
			pods:               fmt.Sprintf("%d", nodepoolPods[name]),
			ready:              ready,
			age:                humanDuration(time.Since(np.GetCreationTimestamp().Time)),
		})
	}

	sort.Slice(npRows, func(i, j int) bool {
		return npRows[i].name < npRows[j].name
	})

	if grep != nil {
		filtered := npRows[:0]
		for _, r := range npRows {
			plain := strings.Join([]string{r.name, r.nodeclass, r.arch, r.os, r.capacityType, r.instanceType, r.instanceCategory, r.instanceGeneration, r.instanceCPU, r.noSchedule, r.nodes, r.cpus, r.memory, r.pods, r.ready, r.age}, " ")
			if ok, _ := grep.MatchString(plain); ok {
				filtered = append(filtered, r)
			}
		}
		npRows = filtered
	}

	if len(npRows) > 0 {
		wn := [16]int{
			len("NODEPOOL"), len("NODECLASS"), len("NODES"), len("ARCH"), len("OS"),
			len("CAPACITY-TYPE"), len("INSTANCE-TYPE"),
			len("INSTANCE-CATEGORY"), len("INSTANCE-GENERATION"), len("INSTANCE-CPU"),
			len("NO-SCHEDULE"),
			len("CPUS"), len("MEMORY"), len("PODS"), len("READY"), len("AGE"),
		}
		for _, r := range npRows {
			wn[0] = max(wn[0], len(r.name))
			wn[1] = max(wn[1], len(r.nodeclass))
			wn[2] = max(wn[2], len(r.nodes))
			wn[3] = max(wn[3], len(r.arch))
			wn[4] = max(wn[4], len(r.os))
			wn[5] = max(wn[5], len(r.capacityType))
			wn[6] = max(wn[6], len(r.instanceType))
			wn[7] = max(wn[7], len(r.instanceCategory))
			wn[8] = max(wn[8], len(r.instanceGeneration))
			wn[9] = max(wn[9], len(r.instanceCPU))
			wn[10] = max(wn[10], len(r.noSchedule))
			wn[11] = max(wn[11], len(r.cpus))
			wn[12] = max(wn[12], len(r.memory))
			wn[13] = max(wn[13], len(r.pods))
			wn[14] = max(wn[14], len(r.ready))
			wn[15] = max(wn[15], len(r.age))
		}

		rowFmt := fmt.Sprintf(
			"%%s  %%-%ds  %%%ds  %%s  %%-%ds  %%-%ds  %%-%ds  %%-%ds  %%-%ds  %%-%ds  %%s  %%%ds  %%%ds  %%%ds  %%-%ds  %%s\n",
			wn[1], wn[2], wn[4], wn[5], wn[6], wn[7], wn[8], wn[9], wn[11], wn[12], wn[13], wn[14],
		)

		fmt.Fprintf(out, "%s  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s  %s\n",
			colorBlue(fmt.Sprintf("%-*s", wn[0], "NODEPOOL")),
			colorBlue(fmt.Sprintf("%-*s", wn[1], "NODECLASS")),
			colorBlue(fmt.Sprintf("%*s", wn[2], "NODES")),
			colorBlue(fmt.Sprintf("%-*s", wn[3], "ARCH")),
			colorBlue(fmt.Sprintf("%-*s", wn[4], "OS")),
			colorBlue(fmt.Sprintf("%-*s", wn[5], "CAPACITY-TYPE")),
			colorBlue(fmt.Sprintf("%-*s", wn[6], "INSTANCE-TYPE")),
			colorBlue(fmt.Sprintf("%-*s", wn[7], "INSTANCE-CATEGORY")),
			colorBlue(fmt.Sprintf("%-*s", wn[8], "INSTANCE-GENERATION")),
			colorBlue(fmt.Sprintf("%-*s", wn[9], "INSTANCE-CPU")),
			colorBlue(fmt.Sprintf("%-*s", wn[10], "NO-SCHEDULE")),
			colorBlue(fmt.Sprintf("%*s", wn[11], "CPUS")),
			colorBlue(fmt.Sprintf("%*s", wn[12], "MEMORY")),
			colorBlue(fmt.Sprintf("%*s", wn[13], "PODS")),
			colorBlue(fmt.Sprintf("%-*s", wn[14], "READY")),
			colorBlue("AGE"),
		)
		for _, r := range npRows {
			fmt.Fprintf(out, rowFmt,
				nodepoolColor(r.name, wn[0]),
				r.nodeclass, r.nodes, archColor(r.arch, wn[3]), r.os, r.capacityType, r.instanceType,
				r.instanceCategory, r.instanceGeneration, r.instanceCPU,
				nodepoolColor(r.noSchedule, wn[10]),
				r.cpus, r.memory, r.pods, r.ready, r.age,
			)
		}
	}

	return nil
}

func joinRequirementValues(v any) string {
	vals, ok := v.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(vals))
	for _, val := range vals {
		if s, ok := val.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ",")
}
