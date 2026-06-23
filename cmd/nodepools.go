package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var nodepoolsWatchInterval float64

var nodepoolsCmd = &cobra.Command{
	Use:   "nodepools",
	Short: "List nodepools",
	RunE:  runNodepools,
}

func init() {
	rootCmd.AddCommand(nodepoolsCmd)
	nodepoolsCmd.Flags().Float64VarP(&nodepoolsWatchInterval, "watch", "w", 0, "refresh interval in seconds (0 = run once)")
}

func runNodepools(_ *cobra.Command, _ []string) error {
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

	render := func() error {
		nodes, err := clientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list nodes: %w", err)
		}

		nodepoolCounts := map[string]int{}
		nodepoolCPUs := map[string]int64{}
		nodepoolMemBytes := map[string]int64{}
		nodepoolPods := map[string]int64{}
		for _, n := range nodes.Items {
			np := n.Labels["karpenter.sh/nodepool"]
			if np == "" {
				np = "<none>"
			}
			nodepoolCounts[np]++
			nodepoolCPUs[np] += n.Status.Capacity.Cpu().Value()
			nodepoolMemBytes[np] += n.Status.Capacity.Memory().Value()
			nodepoolPods[np] += n.Status.Capacity.Pods().Value()
		}

		return displayNodepools(dynamicClient, nodepoolCounts, nodepoolCPUs, nodepoolMemBytes, nodepoolPods, os.Stdout)
	}

	if nodepoolsWatchInterval <= 0 {
		return render()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var lastOutput []byte
	for {
		var buf bytes.Buffer
		nodes, err := clientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		} else {
			nodepoolCounts := map[string]int{}
			nodepoolCPUs := map[string]int64{}
			nodepoolMemBytes := map[string]int64{}
			nodepoolPods := map[string]int64{}
			for _, n := range nodes.Items {
				np := n.Labels["karpenter.sh/nodepool"]
				if np == "" {
					np = "<none>"
				}
				nodepoolCounts[np]++
				nodepoolCPUs[np] += n.Status.Capacity.Cpu().Value()
				nodepoolMemBytes[np] += n.Status.Capacity.Memory().Value()
				nodepoolPods[np] += n.Status.Capacity.Pods().Value()
			}
			if err := displayNodepools(dynamicClient, nodepoolCounts, nodepoolCPUs, nodepoolMemBytes, nodepoolPods, &buf); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			} else {
				lastOutput = buf.Bytes()
			}
		}
		fmt.Print("\033[2J\033[H")
		fmt.Printf("kt nodepools  context: %s\n\n", colorGreen(resolveContextName()))
		os.Stdout.Write(lastOutput)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(float64(time.Second) * nodepoolsWatchInterval)):
		}
	}
}
