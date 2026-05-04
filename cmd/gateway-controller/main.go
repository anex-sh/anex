package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	gpuv1alpha1 "github.com/anex-sh/anex/api/v1alpha1"
	"github.com/anex-sh/anex/internal/gateway"
)

var (
	kubeconfig       string
	gatewayPodName   string
	gatewayNamespace string
	gatewayPodIP     string
	haproxySocket    string
	haproxyUsername  string
	haproxyPassword  string
	workers          int
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (optional, uses in-cluster config if not provided)")
	flag.StringVar(&gatewayPodName, "gateway-pod-name", "", "Name of the gateway pod")
	flag.StringVar(&gatewayNamespace, "gateway-namespace", "", "Namespace of the gateway pod")
	flag.StringVar(&gatewayPodIP, "gateway-pod-ip", "", "IP of the gateway pod")
	flag.StringVar(&haproxySocket, "haproxy-socket", "http://127.0.0.1:5555", "HAProxy Data Plane endpoint (http[s]://host:port or unix socket path)")
	flag.StringVar(&haproxyUsername, "haproxy-username", "admin", "Username for HAProxy Data Plane API basic auth")
	flag.StringVar(&haproxyPassword, "haproxy-password", "admin", "Password for HAProxy Data Plane API basic auth")
	flag.IntVar(&workers, "workers", 2, "Number of worker threads for the controller")
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	// Validate required flags
	if gatewayPodName == "" {
		gatewayPodName = os.Getenv("POD_NAME")
		if gatewayPodName == "" {
			klog.Fatal("--gateway-pod-name flag or POD_NAME env var is required")
		}
	}

	if gatewayNamespace == "" {
		gatewayNamespace = os.Getenv("POD_NAMESPACE")
		if gatewayNamespace == "" {
			klog.Fatal("--gateway-namespace flag or POD_NAMESPACE env var is required")
		}
	}

	if gatewayPodIP == "" {
		gatewayPodIP = os.Getenv("POD_IP")
		if gatewayPodIP == "" {
			klog.Fatal("--gateway-pod-ip flag or POD_IP env var is required")
		}
	}

	klog.Infof("Starting Gateway Controller for pod %s/%s (IP: %s)", gatewayNamespace, gatewayPodName, gatewayPodIP)

	// Build Kubernetes config
	config, err := buildConfig()
	if err != nil {
		klog.Fatalf("Failed to build kubeconfig: %v", err)
	}

	// Create Kubernetes clientset
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Create dynamic client for VirtualService
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create dynamic client: %v", err)
	}

	// Create scheme and add VirtualService types
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		klog.Fatalf("Failed to add clientgo scheme: %v", err)
	}
	if err := gpuv1alpha1.AddToScheme(scheme); err != nil {
		klog.Fatalf("Failed to add gpuv1alpha1 scheme: %v", err)
	}

	// Create informer factory for core resources (Pods, Services)
	// Resync period set to 0 to disable periodic resyncs that cause unnecessary reconciliations
	informerFactory := informers.NewSharedInformerFactory(kubeClient, 0)

	// Create dynamic informer for VirtualService
	gvr := schema.GroupVersionResource{
		Group:    "anex.sh",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	// Resync period set to 0 to disable periodic resyncs that cause unnecessary reconciliations
	dynamicInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)
	vsInformer := dynamicInformerFactory.ForResource(gvr).Informer()
	vsLister := dynamicInformerFactory.ForResource(gvr).Lister()

	// Create controller
	controller, err := gateway.NewController(
		kubeClient,
		dynamicClient,
		scheme,
		informerFactory,
		vsInformer,
		vsLister,
		gatewayPodName,
		gatewayNamespace,
		gatewayPodIP,
		haproxySocket,
		haproxyUsername,
		haproxyPassword,
	)
	if err != nil {
		klog.Fatalf("Failed to create controller: %v", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		klog.Infof("Received signal %v, initiating shutdown", sig)
		cancel()
	}()

	// Start informers
	informerFactory.Start(ctx.Done())
	dynamicInformerFactory.Start(ctx.Done())

	// Run controller
	klog.Info("Starting controller")
	if err := controller.Run(ctx, workers); err != nil {
		klog.Fatalf("Error running controller: %v", err)
	}

	klog.Info("Controller stopped")
}

func buildConfig() (*rest.Config, error) {
	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err == nil {
		klog.Info("Using in-cluster kubeconfig")
		return config, nil
	}

	// Fall back to kubeconfig file
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	if kubeconfig == "" {
		return nil, fmt.Errorf("neither in-cluster config nor kubeconfig file available")
	}

	klog.Infof("Using kubeconfig from %s", kubeconfig)
	config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}

	return config, nil
}
