package glami

import (
	"context"
	"os"
	"path/filepath"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// newClusterClient returns a Kubernetes client that works both in-cluster (ServiceAccount)
// and out-of-cluster (developer machine with kubeconfig). It tries in-cluster first,
// then falls back to KUBECONFIG or default ~/.kube/config.
func newClusterClient() (*kubernetes.Clientset, error) {
	// 1) Try in-cluster config (ServiceAccount)
	if cfg, err := rest.InClusterConfig(); err == nil {
		return kubernetes.NewForConfig(cfg)
	}

	// 2) Fall back to kubeconfig (for local/dev usage). Respect KUBECONFIG if set.
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func listPodsForNode(ctx context.Context, cs *kubernetes.Clientset, nodeName string) (*v1.PodList, error) {
	selectorByNode := fields.OneTermEqualSelector("spec.nodeName", nodeName).String()

	pods, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: selectorByNode + ",status.phase!=Succeeded,status.phase!=Failed",
	})
	if err != nil {
		return nil, err
	}

	return pods, nil
}
