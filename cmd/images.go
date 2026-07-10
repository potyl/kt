package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	imagesNS       string
	imagesLabels   []string
	imagesAll      bool
	imagesNode     string
	imagesNodepool string
)

var imagesCmd = &cobra.Command{
	Use:   "images [pod|svc/service]",
	Short: "List images used by pods and their supported architectures",
	RunE:  runImages,
}

func init() {
	rootCmd.AddCommand(imagesCmd)
	imagesCmd.Flags().StringVarP(&imagesNS, "namespace", "n", "", "namespace (default: all namespaces)")
	imagesCmd.Flags().StringArrayVarP(&imagesLabels, "label", "l", nil, "label selector; can be repeated (ANDed together)")
	imagesCmd.Flags().BoolVarP(&imagesAll, "all", "a", false, "list images for all pods")
	imagesCmd.Flags().StringVarP(&imagesNode, "node", "N", "", "only pods running on this node (exact name or prefix)")
	imagesCmd.Flags().StringVarP(&imagesNodepool, "nodepool", "p", "", "only pods running on nodes of this Karpenter nodepool")
}

func runImages(cmd *cobra.Command, args []string) error {
	if !imagesAll && len(imagesLabels) == 0 && len(args) == 0 && imagesNode == "" && imagesNodepool == "" {
		return fmt.Errorf("specify a pod name, svc/<service>, labels with -l, a node with -N, a nodepool with -p, or use -a for all pods")
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

	pods, err := selectPodsForImages(clientSet, args)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		fmt.Fprintln(os.Stdout, "no pods found")
		return nil
	}

	seen := map[string]bool{}
	var images []string
	for _, pod := range pods {
		init := append([]corev1.Container(nil), pod.Spec.InitContainers...)
		sort.Slice(init, func(i, j int) bool { return init[i].Name < init[j].Name })

		regular := append([]corev1.Container(nil), pod.Spec.Containers...)
		sort.Slice(regular, func(i, j int) bool { return regular[i].Name < regular[j].Name })

		ephemeral := append([]corev1.EphemeralContainer(nil), pod.Spec.EphemeralContainers...)
		sort.Slice(ephemeral, func(i, j int) bool { return ephemeral[i].Name < ephemeral[j].Name })

		for _, c := range init {
			if !seen[c.Image] {
				seen[c.Image] = true
				images = append(images, c.Image)
			}
		}
		for _, c := range regular {
			if !seen[c.Image] {
				seen[c.Image] = true
				images = append(images, c.Image)
			}
		}
		for _, c := range ephemeral {
			if !seen[c.Image] {
				seen[c.Image] = true
				images = append(images, c.Image)
			}
		}
	}
	sort.Strings(images)

	type result struct {
		image string
		archs []string
		err   error
	}
	results := make([]result, len(images))
	var wg sync.WaitGroup
	for i, img := range images {
		wg.Add(1)
		go func(i int, img string) {
			defer wg.Done()
			archs, err := imageArchitectures(img)
			results[i] = result{image: img, archs: archs, err: err}
		}(i, img)
	}
	wg.Wait()

	w := len("IMAGE")
	for _, r := range results {
		if len(r.image) > w {
			w = len(r.image)
		}
	}

	fmt.Fprintf(os.Stdout, "%s  %s\n",
		colorBlue(fmt.Sprintf("%-*s", w, "IMAGE")),
		colorBlue("ARCHITECTURES"),
	)
	for _, r := range results {
		if r.err != nil {
			fmt.Fprintf(os.Stdout, "%-*s  %s\n", w, r.image, colorRed("error: "+r.err.Error()))
			continue
		}
		fmt.Fprintf(os.Stdout, "%-*s  %s\n", w, r.image, joinArchs(r.archs))
	}

	return nil
}

func selectPodsForImages(clientSet *kubernetes.Clientset, args []string) ([]corev1.Pod, error) {
	ctx := context.TODO()

	pods, err := selectPodsBase(clientSet, ctx, args)
	if err != nil {
		return nil, err
	}

	return filterPodsByNode(clientSet, ctx, pods)
}

func selectPodsBase(clientSet *kubernetes.Clientset, ctx context.Context, args []string) ([]corev1.Pod, error) {
	if imagesAll || (len(args) == 0 && len(imagesLabels) == 0) {
		list, err := clientSet.CoreV1().Pods(imagesNS).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list pods: %w", err)
		}
		return list.Items, nil
	}

	if len(imagesLabels) > 0 {
		selector := strings.Join(imagesLabels, ",")
		list, err := clientSet.CoreV1().Pods(imagesNS).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("failed to list pods: %w", err)
		}
		return list.Items, nil
	}

	arg := args[0]

	if svcName, ok := strings.CutPrefix(arg, "svc/"); ok {
		return selectPodsForService(clientSet, ctx, svcName)
	}

	return selectPodsByName(clientSet, ctx, arg)
}

// filterPodsByNode narrows pods to those running on the node given with -N
// and/or on nodes belonging to the Karpenter nodepool given with -p.
func filterPodsByNode(clientSet *kubernetes.Clientset, ctx context.Context, pods []corev1.Pod) ([]corev1.Pod, error) {
	if imagesNode == "" && imagesNodepool == "" {
		return pods, nil
	}

	var poolNodes map[string]bool
	if imagesNodepool != "" {
		list, err := clientSet.CoreV1().Nodes().List(ctx, metav1.ListOptions{
			LabelSelector: "karpenter.sh/nodepool=" + imagesNodepool,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list nodes for nodepool %q: %w", imagesNodepool, err)
		}
		if len(list.Items) == 0 {
			return nil, fmt.Errorf("no nodes found for nodepool %q", imagesNodepool)
		}
		poolNodes = make(map[string]bool, len(list.Items))
		for _, n := range list.Items {
			poolNodes[n.Name] = true
		}
	}

	filtered := pods[:0]
	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}
		if imagesNode != "" && nodeName != imagesNode && !strings.HasPrefix(nodeName, imagesNode) {
			continue
		}
		if poolNodes != nil && !poolNodes[nodeName] {
			continue
		}
		filtered = append(filtered, pod)
	}
	return filtered, nil
}

func selectPodsForService(clientSet *kubernetes.Clientset, ctx context.Context, svcName string) ([]corev1.Pod, error) {
	var selector map[string]string

	if imagesNS != "" {
		svc, err := clientSet.CoreV1().Services(imagesNS).Get(ctx, svcName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get service %q: %w", svcName, err)
		}
		selector = svc.Spec.Selector
	} else {
		list, err := clientSet.CoreV1().Services("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list services: %w", err)
		}
		var found *corev1.Service
		for i, s := range list.Items {
			if s.Name == svcName {
				if found != nil {
					return nil, fmt.Errorf("multiple services named %q found; use -n to specify a namespace", svcName)
				}
				found = &list.Items[i]
			}
		}
		if found == nil {
			return nil, fmt.Errorf("no service named %q found", svcName)
		}
		selector = found.Spec.Selector
	}

	if len(selector) == 0 {
		return nil, fmt.Errorf("service %q has no pod selector", svcName)
	}

	parts := make([]string, 0, len(selector))
	for k, v := range selector {
		parts = append(parts, k+"="+v)
	}
	list, err := clientSet.CoreV1().Pods(imagesNS).List(ctx, metav1.ListOptions{LabelSelector: strings.Join(parts, ",")})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods for service %q: %w", svcName, err)
	}
	return list.Items, nil
}

func selectPodsByName(clientSet *kubernetes.Clientset, ctx context.Context, podName string) ([]corev1.Pod, error) {
	if imagesNS != "" {
		pod, err := clientSet.CoreV1().Pods(imagesNS).Get(ctx, podName, metav1.GetOptions{})
		if err == nil {
			return []corev1.Pod{*pod}, nil
		}
	}

	list, err := clientSet.CoreV1().Pods(imagesNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	for _, pod := range list.Items {
		if pod.Name == podName {
			return []corev1.Pod{pod}, nil
		}
	}

	var matches []corev1.Pod
	for _, pod := range list.Items {
		if strings.HasPrefix(pod.Name, podName) {
			matches = append(matches, pod)
		}
	}
	if len(matches) == 0 {
		if imagesNS != "" {
			return nil, fmt.Errorf("no pod matching %q found in namespace %q", podName, imagesNS)
		}
		return nil, fmt.Errorf("no pod matching %q found", podName)
	}
	return matches, nil
}

func imageArchitectures(imageRef string) ([]string, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	desc, err := remote.Get(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}

	if idx, err := desc.ImageIndex(); err == nil {
		manifest, err := idx.IndexManifest()
		if err != nil {
			return nil, fmt.Errorf("read index: %w", err)
		}
		seen := map[string]bool{}
		var archs []string
		for _, m := range manifest.Manifests {
			if m.Platform == nil || m.Platform.Architecture == "" || m.Platform.Architecture == "unknown" {
				continue
			}
			arch := m.Platform.Architecture
			if !seen[arch] {
				seen[arch] = true
				archs = append(archs, arch)
			}
		}
		sort.Strings(archs)
		return archs, nil
	}

	img, err := desc.Image()
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return []string{cfg.Architecture}, nil
}

func joinArchs(archs []string) string {
	parts := make([]string, len(archs))
	for i, arch := range archs {
		switch arch {
		case "arm64":
			parts[i] = colorGreen(arch)
		case "amd64":
			parts[i] = colorCyan(arch)
		default:
			parts[i] = arch
		}
	}
	return strings.Join(parts, ", ")
}
