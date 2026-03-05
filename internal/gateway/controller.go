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
	"fmt"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	gpuv1alpha1 "gitlab.devklarka.cz/ai/gpu-provider/api/v1alpha1"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/gateway/haproxy"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/gateway/portalloc"
)

const (
	// Finalizer for VirtualService
	VirtualServiceFinalizer = "gpu-provider.glami-ml.com/virtualservice-finalizer"

	// Default port allocation range for VirtualServices
	DefaultPortRangeStart = 6000
	DefaultPortRangeEnd   = 9999

	// Annotations
	AnnotationVirtualPod     = "virtual"
	AnnotationProxySlotID    = "gpu-provider.glami.cz/proxy-slot-id"
	AnnotationManagedBy      = "gpu-provider.glami-ml.com/managed-by"
	AnnotationManagedByValue = "virtualservice-controller"
	AnnotationOwnerService   = "gpu-provider.glami-ml.com/owner-virtualservice"

	// Wireguard IP calculation
	WireguardSubnetBase = "10.254.254."
	WireguardSlotOffset = 11
)

// Controller manages VirtualService resources and configures the gateway
type Controller struct {
	kubeClient    kubernetes.Interface
	dynamicClient dynamic.Interface
	scheme        *runtime.Scheme

	// Informers and listers
	podInformer     cache.SharedIndexInformer
	podLister       corelisters.PodLister
	serviceInformer cache.SharedIndexInformer
	serviceLister   corelisters.ServiceLister
	vsInformer      cache.SharedIndexInformer
	vsLister        cache.GenericLister

	// Gateway configuration
	gatewayPodName      string
	gatewayPodNamespace string
	gatewayIP           string

	// Port allocator
	portAllocator *portalloc.Allocator

	// HAProxy manager (uses interface for testability)
	haproxyManager haproxy.Configurer

	// Work queue
	queue workqueue.RateLimitingInterface

	// State tracking
	mutex           sync.RWMutex
	virtualServices map[string]*gpuv1alpha1.VirtualService // key: namespace/name
	virtualPods     map[string]*VirtualPodInfo             // key: namespace/name

	// Stop channel
	stopCh <-chan struct{}
}

// VirtualPodInfo holds information about a virtual pod
type VirtualPodInfo struct {
	UID            string
	Namespace      string
	Name           string
	Labels         map[string]string
	ProxySlotID    int
	WireguardIP    string
	Ready          bool
	ContainerPorts []int32 // Sorted container ports from pod spec
}

// NewController creates a new Gateway controller
func NewController(
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	scheme *runtime.Scheme,
	informerFactory informers.SharedInformerFactory,
	vsInformer cache.SharedIndexInformer,
	vsLister cache.GenericLister,
	gatewayPodName string,
	gatewayPodNamespace string,
	gatewayIP string,
	haproxySocketPath string,
	haproxyUsername string,
	haproxyPassword string,
) (*Controller, error) {

	haproxyMgr, err := haproxy.NewManager(haproxySocketPath, haproxyUsername, haproxyPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to create HAProxy manager: %w", err)
	}

	return newController(
		kubeClient,
		dynamicClient,
		scheme,
		informerFactory,
		vsInformer,
		vsLister,
		gatewayPodName,
		gatewayPodNamespace,
		gatewayIP,
		haproxyMgr,
	), nil
}

// NewControllerForTesting creates a new Gateway controller with a custom HAProxy manager.
// This is intended for use in tests where a mock HAProxy manager is needed.
func NewControllerForTesting(
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	scheme *runtime.Scheme,
	informerFactory informers.SharedInformerFactory,
	vsInformer cache.SharedIndexInformer,
	vsLister cache.GenericLister,
	gatewayPodName string,
	gatewayPodNamespace string,
	gatewayIP string,
	haproxyManager haproxy.Configurer,
) *Controller {
	return newController(
		kubeClient,
		dynamicClient,
		scheme,
		informerFactory,
		vsInformer,
		vsLister,
		gatewayPodName,
		gatewayPodNamespace,
		gatewayIP,
		haproxyManager,
	)
}

// newController is the internal constructor that creates the controller with all dependencies
func newController(
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	scheme *runtime.Scheme,
	informerFactory informers.SharedInformerFactory,
	vsInformer cache.SharedIndexInformer,
	vsLister cache.GenericLister,
	gatewayPodName string,
	gatewayPodNamespace string,
	gatewayIP string,
	haproxyManager haproxy.Configurer,
) *Controller {
	portAllocator := portalloc.NewAllocator(DefaultPortRangeStart, DefaultPortRangeEnd)

	c := &Controller{
		kubeClient:          kubeClient,
		dynamicClient:       dynamicClient,
		scheme:              scheme,
		podInformer:         informerFactory.Core().V1().Pods().Informer(),
		podLister:           informerFactory.Core().V1().Pods().Lister(),
		serviceInformer:     informerFactory.Core().V1().Services().Informer(),
		serviceLister:       informerFactory.Core().V1().Services().Lister(),
		vsInformer:          vsInformer,
		vsLister:            vsLister,
		gatewayPodName:      gatewayPodName,
		gatewayPodNamespace: gatewayPodNamespace,
		gatewayIP:           gatewayIP,
		portAllocator:       portAllocator,
		haproxyManager:      haproxyManager,
		queue:               workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		virtualServices:     make(map[string]*gpuv1alpha1.VirtualService),
		virtualPods:         make(map[string]*VirtualPodInfo),
	}

	// Set up event handlers for VirtualService
	vsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.enqueueVirtualService(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.enqueueVirtualService(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			c.enqueueVirtualService(obj)
		},
	})

	// Set up event handlers for Pods
	c.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handlePodEvent(obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			c.handlePodEvent(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			c.handlePodEvent(obj)
		},
	})

	return c
}

// Run starts the controller
func (c *Controller) Run(ctx context.Context, workers int) error {
	defer c.queue.ShutDown()

	klog.Info("Starting VirtualService controller")

	c.stopCh = ctx.Done()

	// Wait for caches to sync
	klog.Info("Waiting for informer caches to sync")
	if !cache.WaitForCacheSync(ctx.Done(), c.podInformer.HasSynced, c.serviceInformer.HasSynced, c.vsInformer.HasSynced) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	klog.Info("Started workers")
	<-ctx.Done()
	klog.Info("Shutting down workers")

	return nil
}

func (c *Controller) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := c.queue.Get()
	if shutdown {
		return false
	}

	defer c.queue.Done(obj)

	key, ok := obj.(string)
	if !ok {
		c.queue.Forget(obj)
		klog.Errorf("expected string in workqueue but got %#v", obj)
		return true
	}

	if err := c.reconcile(ctx, key); err != nil {
		c.queue.AddRateLimited(key)
		klog.Errorf("error reconciling VirtualService %s: %v", key, err)
		return true
	}

	c.queue.Forget(obj)
	return true
}

func (c *Controller) enqueueVirtualService(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.Errorf("failed to get key for object %#v: %v", obj, err)
		return
	}
	c.queue.Add(key)
}

func (c *Controller) handlePodEvent(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}

	// Check if this is a virtual pod
	if pod.Annotations[AnnotationVirtualPod] != "true" {
		return
	}

	// Update our cache
	c.updateVirtualPodCache(pod)

	// Enqueue all VirtualServices that might match this pod
	c.enqueueVirtualServicesForPod(pod)
}

func (c *Controller) updateVirtualPodCache(pod *corev1.Pod) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	if pod.DeletionTimestamp != nil {
		delete(c.virtualPods, key)
		return
	}

	proxySlotID := 0
	if slotStr, ok := pod.Annotations[AnnotationProxySlotID]; ok {
		fmt.Sscanf(slotStr, "%d", &proxySlotID)
	}

	wireguardIP := fmt.Sprintf("%s%d", WireguardSubnetBase, WireguardSlotOffset+proxySlotID)

	// For now, assume all pods are ready (as per requirements)
	ready := pod.Status.Phase == corev1.PodRunning

	// Extract and sort container ports from pod spec
	containerPorts := extractContainerPorts(pod)

	c.virtualPods[key] = &VirtualPodInfo{
		UID:            string(pod.UID),
		Namespace:      pod.Namespace,
		Name:           pod.Name,
		Labels:         pod.Labels,
		ProxySlotID:    proxySlotID,
		WireguardIP:    wireguardIP,
		Ready:          ready,
		ContainerPorts: containerPorts,
	}
}

// extractContainerPorts extracts all container ports from a pod spec and returns them sorted
func extractContainerPorts(pod *corev1.Pod) []int32 {
	portsMap := make(map[int32]bool)

	// Collect all unique ports from all containers
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			portsMap[port.ContainerPort] = true
		}
	}

	// Convert to sorted slice
	ports := make([]int32, 0, len(portsMap))
	for port := range portsMap {
		ports = append(ports, port)
	}
	sort.Slice(ports, func(i, j int) bool {
		return ports[i] < ports[j]
	})

	return ports
}

func (c *Controller) enqueueVirtualServicesForPod(pod *corev1.Pod) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	for _, vs := range c.virtualServices {
		selector := labels.SelectorFromSet(vs.Spec.Service.Selector)
		if selector.Matches(labels.Set(pod.Labels)) {
			key := fmt.Sprintf("%s/%s", vs.Namespace, vs.Name)
			c.queue.Add(key)
		}
	}
}

func (c *Controller) reconcile(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	klog.V(4).Infof("Reconciling VirtualService %s", key)

	// Get VirtualService from lister
	obj, err := c.vsLister.ByNamespace(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			// VirtualService was deleted
			c.mutex.Lock()
			delete(c.virtualServices, key)
			c.mutex.Unlock()
			return c.handleVirtualServiceDeletion(ctx, namespace, name)
		}
		return err
	}

	// Convert from unstructured to typed VirtualService
	unstructuredObj, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("expected *unstructured.Unstructured but got %T", obj)
	}

	vs := &gpuv1alpha1.VirtualService{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, vs)
	if err != nil {
		return fmt.Errorf("failed to convert unstructured to VirtualService: %w", err)
	}

	// Store in cache
	c.mutex.Lock()
	c.virtualServices[key] = vs
	c.mutex.Unlock()

	// Handle finalizer
	if vs.DeletionTimestamp != nil {
		return c.handleVirtualServiceFinalization(ctx, vs)
	}

	// Add finalizer if not present
	if !containsString(vs.Finalizers, VirtualServiceFinalizer) {
		vs.Finalizers = append(vs.Finalizers, VirtualServiceFinalizer)
		_, err := c.updateVirtualService(ctx, vs)
		if err != nil {
			return err
		}
	}

	// Reconcile the VirtualService
	return c.reconcileVirtualService(ctx, vs)
}

func (c *Controller) handleVirtualServiceDeletion(ctx context.Context, namespace, name string) error {
	klog.Infof("Handling deletion of VirtualService %s/%s", namespace, name)
	// Cleanup is handled in finalization, nothing to do here
	return nil
}

func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	result := []string{}
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

// Helper to update VirtualService
func (c *Controller) updateVirtualService(ctx context.Context, vs *gpuv1alpha1.VirtualService) (*gpuv1alpha1.VirtualService, error) {
	// Use dynamic client to update VirtualService
	gvr := schema.GroupVersionResource{
		Group:    "gpu-provider.glami-ml.com",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vs)
	if err != nil {
		return nil, fmt.Errorf("failed to convert VirtualService to unstructured: %w", err)
	}

	unstructuredVS := &unstructured.Unstructured{Object: unstructuredObj}

	updated, err := c.dynamicClient.Resource(gvr).Namespace(vs.Namespace).Update(ctx, unstructuredVS, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	updatedVS := &gpuv1alpha1.VirtualService{}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(updated.Object, updatedVS)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to VirtualService: %w", err)
	}

	return updatedVS, nil
}

// Helper to update VirtualService status
func (c *Controller) updateVirtualServiceStatus(ctx context.Context, vs *gpuv1alpha1.VirtualService) error {
	// Use dynamic client to update VirtualService status subresource
	gvr := schema.GroupVersionResource{
		Group:    "gpu-provider.glami-ml.com",
		Version:  "v1alpha1",
		Resource: "virtualservices",
	}

	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(vs)
	if err != nil {
		return fmt.Errorf("failed to convert VirtualService to unstructured: %w", err)
	}

	unstructuredVS := &unstructured.Unstructured{Object: unstructuredObj}

	_, err = c.dynamicClient.Resource(gvr).Namespace(vs.Namespace).UpdateStatus(ctx, unstructuredVS, metav1.UpdateOptions{})
	return err
}
