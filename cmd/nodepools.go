package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var nodepoolsCmd = &cobra.Command{
	Use:   "nodepools",
	Short: "List nodepools",
	RunE:  runNodepools,
}

func init() {
	rootCmd.AddCommand(nodepoolsCmd)
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
