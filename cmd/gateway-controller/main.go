/*
Copyright 2026 GLAMI ML.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	gpuv1alpha1 "gitlab.devklarka.cz/ai/gpu-provider/api/v1alpha1"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/gateway"
)

var (
	kubeconfig       string
	gatewayPodName   string
	gatewayNamespace string
	haproxySocket    string
	workers          int
)

func init() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (optional, uses in-cluster config if not provided)")
	flag.StringVar(&gatewayPodName, "gateway-pod-name", "", "Name of the gateway pod")
	flag.StringVar(&gatewayNamespace, "gateway-namespace", "", "Namespace of the gateway pod")
	flag.StringVar(&haproxySocket, "haproxy-socket", "http://127.0.0.1:5555", "HAProxy Data Plane endpoint (http[s]://host:port or unix socket path)")
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

	klog.Infof("Starting Gateway Controller for pod %s/%s", gatewayNamespace, gatewayPodName)

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
	informerFactory := informers.NewSharedInformerFactory(kubeClient, 30*time.Second)

	// Create dynamic informer for VirtualService
	gvr := schema.GroupVersionResource{
		Group:    "gpu-provider.glami-ml.com",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}
	
	dynamicInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 30*time.Second)
	vsInformer := dynamicInformerFactory.ForResource(gvr).Informer()
	vsLister := dynamicInformerFactory.ForResource(gvr).Lister()

	// Get gateway pod to determine labels
	gatewayPod, err := kubeClient.CoreV1().Pods(gatewayNamespace).Get(context.Background(), gatewayPodName, v1.GetOptions{})
	if err != nil {
		klog.Fatalf("Failed to get gateway pod: %v", err)
	}

	gatewayLabels := gatewayPod.Labels
	if gatewayLabels == nil {
		gatewayLabels = make(map[string]string)
	}

	klog.Infof("Gateway labels: %v", gatewayLabels)

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
		gatewayLabels,
		haproxySocket,
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
