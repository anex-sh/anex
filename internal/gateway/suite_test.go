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

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicinformer "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	gpuv1alpha1 "gitlab.devklarka.cz/ai/gpu-provider/api/v1alpha1"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/gateway/haproxy"
)

const (
	testNamespace        = "test-ns"
	gatewayPodName       = "test-gateway"
	gatewayPodNamespace  = "gateway-ns"
	defaultTimeout       = 10 * time.Second
	pollInterval         = 100 * time.Millisecond
)

// testEnv holds all test environment resources
type testEnv struct {
	env             *envtest.Environment
	kubeClient      kubernetes.Interface
	dynamicClient   dynamic.Interface
	scheme          *runtime.Scheme
	informerFactory informers.SharedInformerFactory
	vsInformer      cache.SharedIndexInformer
	vsLister        cache.GenericLister
	mockHAProxy     *haproxy.MockManager
	controller      *Controller
	ctx             context.Context
	cancel          context.CancelFunc
	gatewayLabels   map[string]string
}

// setupTestEnv sets up the test environment with envtest
func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// Create scheme
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add client-go scheme: %v", err)
	}
	if err := gpuv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add gpu-provider scheme: %v", err)
	}

	// Check for KUBEBUILDER_ASSETS environment variable
	// If not set, try common locations
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		possiblePaths := []string{
			"/usr/local/kubebuilder/bin",
			filepath.Join(os.Getenv("HOME"), ".local", "kubebuilder", "bin"),
			filepath.Join(os.Getenv("HOME"), ".kubebuilder", "bin"),
		}
		for _, p := range possiblePaths {
			if _, err := os.Stat(filepath.Join(p, "kube-apiserver")); err == nil {
				os.Setenv("KUBEBUILDER_ASSETS", p)
				t.Logf("Using KUBEBUILDER_ASSETS=%s", p)
				break
			}
		}
	}

	// Start envtest
	testEnvInstance := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "deploy", "chart", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnvInstance.Start()
	if err != nil {
		t.Fatalf("Failed to start envtest: %v", err)
	}

	// Create clients
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		testEnvInstance.Stop()
		t.Fatalf("Failed to create kubernetes client: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		testEnvInstance.Stop()
		t.Fatalf("Failed to create dynamic client: %v", err)
	}

	// Create context with cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Create namespaces
	if err := createNamespace(ctx, kubeClient, testNamespace); err != nil {
		cancel()
		testEnvInstance.Stop()
		t.Fatalf("Failed to create test namespace: %v", err)
	}
	if err := createNamespace(ctx, kubeClient, gatewayPodNamespace); err != nil {
		cancel()
		testEnvInstance.Stop()
		t.Fatalf("Failed to create gateway namespace: %v", err)
	}

	// Create gateway pod
	gatewayLabels := map[string]string{
		"app": "gpu-provider-gateway",
	}
	gatewayPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gatewayPodName,
			Namespace: gatewayPodNamespace,
			Labels:    gatewayLabels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "gateway",
				Image: "fake:latest",
			}},
		},
	}
	if _, err := kubeClient.CoreV1().Pods(gatewayPodNamespace).Create(ctx, gatewayPod, metav1.CreateOptions{}); err != nil {
		cancel()
		testEnvInstance.Stop()
		t.Fatalf("Failed to create gateway pod: %v", err)
	}

	// Create informer factory
	informerFactory := informers.NewSharedInformerFactory(kubeClient, 0)

	// Create dynamic informer for VirtualService
	gvr := schema.GroupVersionResource{
		Group:    "gpu-provider.glami-ml.com",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}
	dynamicInformerFactory := dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, 0)
	vsInformer := dynamicInformerFactory.ForResource(gvr).Informer()
	vsLister := dynamicInformerFactory.ForResource(gvr).Lister()

	// Create mock HAProxy manager
	mockHAProxy := haproxy.NewMockManager()

	// Create controller
	controller := NewControllerForTesting(
		kubeClient,
		dynamicClient,
		scheme,
		informerFactory,
		vsInformer,
		vsLister,
		gatewayPodName,
		gatewayPodNamespace,
		gatewayLabels,
		mockHAProxy,
	)

	// Start informers
	informerFactory.Start(ctx.Done())
	dynamicInformerFactory.Start(ctx.Done())

	// Wait for cache sync
	informerFactory.WaitForCacheSync(ctx.Done())
	cache.WaitForCacheSync(ctx.Done(), vsInformer.HasSynced)

	// Start controller in background
	go func() {
		if err := controller.Run(ctx, 1); err != nil {
			t.Logf("Controller stopped: %v", err)
		}
	}()

	return &testEnv{
		env:             testEnvInstance,
		kubeClient:      kubeClient,
		dynamicClient:   dynamicClient,
		scheme:          scheme,
		informerFactory: informerFactory,
		vsInformer:      vsInformer,
		vsLister:        vsLister,
		mockHAProxy:     mockHAProxy,
		controller:      controller,
		ctx:             ctx,
		cancel:          cancel,
		gatewayLabels:   gatewayLabels,
	}
}

// teardownTestEnv tears down the test environment
func (te *testEnv) teardown(t *testing.T) {
	t.Helper()

	// Cancel context to stop controller
	te.cancel()

	// Stop envtest
	if err := te.env.Stop(); err != nil {
		t.Errorf("Failed to stop envtest: %v", err)
	}
}

// createNamespace creates a namespace if it doesn't exist
func createNamespace(ctx context.Context, client kubernetes.Interface, name string) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	_, err := client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	return err
}

// createVirtualService creates a VirtualService and returns it
func (te *testEnv) createVirtualService(t *testing.T, name, namespace string, ports []gpuv1alpha1.ServicePort, selector map[string]string) *gpuv1alpha1.VirtualService {
	t.Helper()

	vs := &gpuv1alpha1.VirtualService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gpu-provider.glami-ml.com/v1alpha1",
			Kind:       "VirtualService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: gpuv1alpha1.VirtualServiceSpec{
			Gateway: gpuv1alpha1.GatewaySelector{
				Selector: te.gatewayLabels,
			},
			Service: gpuv1alpha1.ServiceSpec{
				Selector: selector,
				Ports:    ports,
			},
		},
	}

	// Convert to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vs)
	if err != nil {
		t.Fatalf("Failed to convert VirtualService to unstructured: %v", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "gpu-provider.glami-ml.com",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	// Create via dynamic client
	created, err := te.dynamicClient.Resource(gvr).Namespace(namespace).Create(
		te.ctx,
		&unstructured.Unstructured{Object: unstructuredObj},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("Failed to create VirtualService: %v", err)
	}

	// Convert back to typed
	result := &gpuv1alpha1.VirtualService{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(created.Object, result); err != nil {
		t.Fatalf("Failed to convert unstructured to VirtualService: %v", err)
	}

	return result
}

// getVirtualService gets a VirtualService by name and namespace
func (te *testEnv) getVirtualService(t *testing.T, name, namespace string) *gpuv1alpha1.VirtualService {
	t.Helper()

	gvr := schema.GroupVersionResource{
		Group:    "gpu-provider.glami-ml.com",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	obj, err := te.dynamicClient.Resource(gvr).Namespace(namespace).Get(te.ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get VirtualService: %v", err)
	}

	result := &gpuv1alpha1.VirtualService{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, result); err != nil {
		t.Fatalf("Failed to convert unstructured to VirtualService: %v", err)
	}

	return result
}

// deleteVirtualService deletes a VirtualService
func (te *testEnv) deleteVirtualService(t *testing.T, name, namespace string) {
	t.Helper()

	gvr := schema.GroupVersionResource{
		Group:    "gpu-provider.glami-ml.com",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	err := te.dynamicClient.Resource(gvr).Namespace(namespace).Delete(te.ctx, name, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete VirtualService: %v", err)
	}
}

// waitForCondition waits for a VirtualService to have a specific condition
func (te *testEnv) waitForCondition(t *testing.T, name, namespace string, conditionType string, status metav1.ConditionStatus) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		vs := te.getVirtualService(t, name, namespace)
		for _, cond := range vs.Status.Conditions {
			if cond.Type == conditionType && cond.Status == status {
				return
			}
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for condition %s=%s on VirtualService %s/%s", conditionType, status, namespace, name)
}

// waitForFinalizer waits for a VirtualService to have the finalizer
func (te *testEnv) waitForFinalizer(t *testing.T, name, namespace string) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		vs := te.getVirtualService(t, name, namespace)
		for _, f := range vs.Finalizers {
			if f == VirtualServiceFinalizer {
				return
			}
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for finalizer on VirtualService %s/%s", namespace, name)
}

// waitForVirtualServiceDeleted waits for a VirtualService to be fully deleted
func (te *testEnv) waitForVirtualServiceDeleted(t *testing.T, name, namespace string) {
	t.Helper()

	gvr := schema.GroupVersionResource{
		Group:    "gpu-provider.glami-ml.com",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		_, err := te.dynamicClient.Resource(gvr).Namespace(namespace).Get(te.ctx, name, metav1.GetOptions{})
		if err != nil {
			// VirtualService is deleted
			return
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for VirtualService %s/%s to be deleted", namespace, name)
}

// getService gets a Service by name and namespace
func (te *testEnv) getService(t *testing.T, name, namespace string) *corev1.Service {
	t.Helper()

	svc, err := te.kubeClient.CoreV1().Services(namespace).Get(te.ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get Service: %v", err)
	}

	return svc
}

// waitForService waits for a Service to exist
func (te *testEnv) waitForService(t *testing.T, name, namespace string) *corev1.Service {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		svc, err := te.kubeClient.CoreV1().Services(namespace).Get(te.ctx, name, metav1.GetOptions{})
		if err == nil {
			return svc
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for Service %s/%s to exist", namespace, name)
	return nil
}

// waitForServiceDeleted waits for a Service to be deleted
func (te *testEnv) waitForServiceDeleted(t *testing.T, name, namespace string) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		_, err := te.kubeClient.CoreV1().Services(namespace).Get(te.ctx, name, metav1.GetOptions{})
		if err != nil {
			// Service is deleted
			return
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for Service %s/%s to be deleted", namespace, name)
}
