package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
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

	gpuv1alpha1 "github.com/anex-sh/anex/api/v1alpha1"
	"github.com/anex-sh/anex/internal/gateway/haproxy"
)

const (
	testNamespace       = "test-ns"
	gatewayPodName      = "test-gateway"
	gatewayPodNamespace = "gateway-ns"
	defaultTimeout      = 10 * time.Second
	pollInterval        = 100 * time.Millisecond
)

const gatewayTestIP = "10.100.0.1"

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
	gatewayIP       string
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

	// Create gateway pod (kept for realism; IP is passed directly to controller)
	gatewayPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gatewayPodName,
			Namespace: gatewayPodNamespace,
			Labels: map[string]string{
				"app": "gpu-provider-gateway",
			},
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
		Group:    "anex.sh",
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
		gatewayTestIP,
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
		gatewayIP:       gatewayTestIP,
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
			APIVersion: "anex.sh/v1alpha1",
			Kind:       "VirtualService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: gpuv1alpha1.VirtualServiceSpec{
			Gateway: gpuv1alpha1.GatewaySelector{
				Selector: map[string]string{"app": "gpu-provider-gateway"},
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
		Group:    "anex.sh",
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
		Group:    "anex.sh",
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
		Group:    "anex.sh",
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
		Group:    "anex.sh",
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

// updateVirtualService updates a VirtualService with the given ports
func (te *testEnv) updateVirtualService(t *testing.T, name, namespace string, ports []gpuv1alpha1.ServicePort, selector map[string]string) *gpuv1alpha1.VirtualService {
	t.Helper()

	gvr := schema.GroupVersionResource{
		Group:    "anex.sh",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	// Get current VirtualService
	obj, err := te.dynamicClient.Resource(gvr).Namespace(namespace).Get(te.ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get VirtualService for update: %v", err)
	}

	vs := &gpuv1alpha1.VirtualService{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, vs); err != nil {
		t.Fatalf("Failed to convert unstructured to VirtualService: %v", err)
	}

	// Update spec
	vs.Spec.Service.Ports = ports
	if selector != nil {
		vs.Spec.Service.Selector = selector
	}

	// Convert back to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vs)
	if err != nil {
		t.Fatalf("Failed to convert VirtualService to unstructured: %v", err)
	}

	// Update via dynamic client
	updated, err := te.dynamicClient.Resource(gvr).Namespace(namespace).Update(
		te.ctx,
		&unstructured.Unstructured{Object: unstructuredObj},
		metav1.UpdateOptions{},
	)
	if err != nil {
		t.Fatalf("Failed to update VirtualService: %v", err)
	}

	result := &gpuv1alpha1.VirtualService{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(updated.Object, result); err != nil {
		t.Fatalf("Failed to convert unstructured to VirtualService: %v", err)
	}

	return result
}

// addAnnotationToVirtualService adds an annotation to trigger a reconcile
func (te *testEnv) addAnnotationToVirtualService(t *testing.T, name, namespace, key, value string) {
	t.Helper()

	gvr := schema.GroupVersionResource{
		Group:    "anex.sh",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	// Get current VirtualService
	obj, err := te.dynamicClient.Resource(gvr).Namespace(namespace).Get(te.ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get VirtualService for annotation update: %v", err)
	}

	// Add annotation
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[key] = value
	obj.SetAnnotations(annotations)

	// Update via dynamic client
	_, err = te.dynamicClient.Resource(gvr).Namespace(namespace).Update(
		te.ctx,
		obj,
		metav1.UpdateOptions{},
	)
	if err != nil {
		t.Fatalf("Failed to add annotation to VirtualService: %v", err)
	}
}

// createVirtualPod creates a pod with virtual annotation and proxy slot
func (te *testEnv) createVirtualPod(t *testing.T, name, namespace string, labels map[string]string, proxySlotID int, containerPorts []int32) *corev1.Pod {
	t.Helper()

	// Build container ports
	ports := []corev1.ContainerPort{}
	for _, p := range containerPorts {
		ports = append(ports, corev1.ContainerPort{
			ContainerPort: p,
			Protocol:      corev1.ProtocolTCP,
		})
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"virtual":                             "true",
				"anex.sh/proxy-slot-id": fmt.Sprintf("%d", proxySlotID),
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "fake:latest",
					Ports: ports,
				},
			},
		},
	}

	created, err := te.kubeClient.CoreV1().Pods(namespace).Create(te.ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create virtual pod: %v", err)
	}

	return created
}

// createRegularPod creates a regular pod without the virtual annotation
func (te *testEnv) createRegularPod(t *testing.T, name, namespace string, labels map[string]string) *corev1.Pod {
	t.Helper()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "fake:latest",
				},
			},
		},
	}

	created, err := te.kubeClient.CoreV1().Pods(namespace).Create(te.ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create regular pod: %v", err)
	}

	return created
}

// createService creates a regular Service (not owned by VirtualService)
func (te *testEnv) createService(t *testing.T, name, namespace string, ports []corev1.ServicePort) *corev1.Service {
	t.Helper()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app": "some-other-app",
			},
			Ports: ports,
		},
	}

	created, err := te.kubeClient.CoreV1().Services(namespace).Create(te.ctx, svc, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create Service: %v", err)
	}

	return created
}

// deleteService deletes a Service
func (te *testEnv) deleteService(t *testing.T, name, namespace string) {
	t.Helper()

	err := te.kubeClient.CoreV1().Services(namespace).Delete(te.ctx, name, metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Failed to delete Service: %v", err)
	}
}

// waitForAllocatedPorts waits for a VirtualService to have a specific number of allocated ports
func (te *testEnv) waitForAllocatedPorts(t *testing.T, name, namespace string, count int) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		vs := te.getVirtualService(t, name, namespace)
		if len(vs.Status.AllocatedPorts) == count {
			return
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for VirtualService %s/%s to have %d allocated ports", namespace, name, count)
}

// waitForHAProxyConfig waits for HAProxy mock to have a specific number of listener configs for a VirtualService
func (te *testEnv) waitForHAProxyConfig(t *testing.T, ownerKey string, expectedListeners int) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		configs := te.mockHAProxy.GetConfigs(ownerKey)
		if len(configs) == expectedListeners {
			return
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for HAProxy to have %d listeners for %s", expectedListeners, ownerKey)
}

// waitForHAProxyBackends waits for HAProxy mock to have expected backends for a specific listener
func (te *testEnv) waitForHAProxyBackends(t *testing.T, ownerKey string, listenerIndex, expectedBackends int) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		configs := te.mockHAProxy.GetConfigs(ownerKey)
		if len(configs) > listenerIndex && len(configs[listenerIndex].Backends) == expectedBackends {
			return
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for HAProxy listener %d to have %d backends for %s", listenerIndex, expectedBackends, ownerKey)
}

// waitForEndpointSlice waits for an EndpointSlice to exist and returns it
func (te *testEnv) waitForEndpointSlice(t *testing.T, name, namespace string) *discoveryv1.EndpointSlice {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		eps, err := te.kubeClient.DiscoveryV1().EndpointSlices(namespace).Get(te.ctx, name, metav1.GetOptions{})
		if err == nil {
			return eps
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for EndpointSlice %s/%s to exist", namespace, name)
	return nil
}

// waitForEndpointSliceDeleted waits for an EndpointSlice to be deleted
func (te *testEnv) waitForEndpointSliceDeleted(t *testing.T, name, namespace string) {
	t.Helper()

	deadline := time.Now().Add(defaultTimeout)
	for time.Now().Before(deadline) {
		_, err := te.kubeClient.DiscoveryV1().EndpointSlices(namespace).Get(te.ctx, name, metav1.GetOptions{})
		if err != nil {
			return
		}
		time.Sleep(pollInterval)
	}

	t.Fatalf("Timeout waiting for EndpointSlice %s/%s to be deleted", namespace, name)
}
