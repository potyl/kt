package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const usage = `kt - list pods across all namespaces

Usage:
  kt [flags]

Flags:
  --context string   kubectl context to use (default: current context)
  -h, --help         show this help message
`

func main() {
	var kubeContext string
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

	pods, err := clientSet.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	fmt.Printf("Found %d pods:\n", len(pods.Items))
	fmt.Printf("%-27s %s\n", "NAMESPACE", "NAME")
	for _, pod := range pods.Items {
		fmt.Printf("%-27s %s\n", pod.Namespace, pod.Name)
	}
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic("could not determine home directory")
	}
	return home
}
