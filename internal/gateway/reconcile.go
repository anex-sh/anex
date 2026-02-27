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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"

	gpuv1alpha1 "gitlab.devklarka.cz/ai/gpu-provider/api/v1alpha1"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/gateway/haproxy"
)

func (c *Controller) reconcileVirtualService(ctx context.Context, vs *gpuv1alpha1.VirtualService) error {
	klog.V(4).Infof("Reconciling VirtualService %s/%s", vs.Namespace, vs.Name)

	// Step 1: Validate the VirtualService spec
	if err := c.validateVirtualService(vs); err != nil {
		return c.setConditionAndUpdate(ctx, vs, metav1.Condition{
			Type:               gpuv1alpha1.ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             gpuv1alpha1.ReasonUnsupportedSpec,
			Message:            err.Error(),
			ObservedGeneration: vs.Generation,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})
	}

	// Step 2: Ensure port allocations exist and are stable
	if err := c.ensurePortAllocations(vs); err != nil {
		return c.setConditionAndUpdate(ctx, vs, metav1.Condition{
			Type:               gpuv1alpha1.ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             gpuv1alpha1.ReasonPortAllocationErr,
			Message:            fmt.Sprintf("Failed to allocate ports: %v", err),
			ObservedGeneration: vs.Generation,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})
	}

	// Step 3: Get matching virtual pods
	matchingPods := c.getMatchingPods(vs)

	// Step 4: Ensure generated Kubernetes Service exists
	if err := c.ensureGeneratedService(ctx, vs); err != nil {
		if errors.IsAlreadyExists(err) {
			return c.setConditionAndUpdate(ctx, vs, metav1.Condition{
				Type:               gpuv1alpha1.ConditionTypeReady,
				Status:             metav1.ConditionFalse,
				Reason:             gpuv1alpha1.ReasonServiceConflict,
				Message:            fmt.Sprintf("Service with name %s already exists and is not owned by this VirtualService", vs.Name),
				ObservedGeneration: vs.Generation,
				LastTransitionTime: metav1.NewTime(time.Now()),
			})
		}
		return c.setConditionAndUpdate(ctx, vs, metav1.Condition{
			Type:               gpuv1alpha1.ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             gpuv1alpha1.ReasonReconcileError,
			Message:            fmt.Sprintf("Failed to create/update Service: %v", err),
			ObservedGeneration: vs.Generation,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})
	}

	// Step 5: Configure HAProxy listeners and backends
	if err := c.configureHAProxy(ctx, vs, matchingPods); err != nil {
		return c.setConditionAndUpdate(ctx, vs, metav1.Condition{
			Type:               gpuv1alpha1.ConditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             gpuv1alpha1.ReasonReconcileError,
			Message:            fmt.Sprintf("Failed to configure HAProxy: %v", err),
			ObservedGeneration: vs.Generation,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})
	}

	// Step 6: Set Ready condition to True
	return c.setConditionAndUpdate(ctx, vs, metav1.Condition{
		Type:               gpuv1alpha1.ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             gpuv1alpha1.ReasonReconciled,
		Message:            fmt.Sprintf("Service and proxy configured with %d backends", len(matchingPods)),
		ObservedGeneration: vs.Generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	})
}

func (c *Controller) validateVirtualService(vs *gpuv1alpha1.VirtualService) error {
	// Validate ports
	for i, port := range vs.Spec.Service.Ports {
		// Protocol must be TCP or empty
		if port.Protocol != "" && port.Protocol != "TCP" {
			return fmt.Errorf("port[%d]: protocol must be TCP (got %s)", i, port.Protocol)
		}

		// Port and TargetPort must be valid
		if port.Port < 1 || port.Port > 65535 {
			return fmt.Errorf("port[%d]: port must be between 1 and 65535 (got %d)", i, port.Port)
		}
		if port.TargetPort < 1 || port.TargetPort > 65535 {
			return fmt.Errorf("port[%d]: targetPort must be between 1 and 65535 (got %d)", i, port.TargetPort)
		}
	}

	return nil
}

func (c *Controller) ensurePortAllocations(vs *gpuv1alpha1.VirtualService) error {
	// Check if allocations already exist
	if len(vs.Status.AllocatedPorts) == len(vs.Spec.Service.Ports) {
		// Verify they match the spec
		allMatch := true
		for i, specPort := range vs.Spec.Service.Ports {
			found := false
			for _, allocPort := range vs.Status.AllocatedPorts {
				if allocPort.ServicePort == specPort.Port &&
					allocPort.TargetPort == specPort.TargetPort &&
					allocPort.Name == specPort.Name {
					found = true
					break
				}
			}
			if !found {
				allMatch = false
				break
			}
			_ = i
		}
		if allMatch {
			// Allocations are stable and match, reuse them
			klog.V(4).Infof("Reusing existing port allocations for VirtualService %s/%s", vs.Namespace, vs.Name)
			return nil
		}
	}

	// Need to allocate ports
	allocatedPorts := []gpuv1alpha1.AllocatedPort{}
	vsKey := fmt.Sprintf("%s/%s", vs.Namespace, vs.Name)

	// Release old allocations if any
	if len(vs.Status.AllocatedPorts) > 0 {
		for _, oldAlloc := range vs.Status.AllocatedPorts {
			c.portAllocator.Release(vsKey, int(oldAlloc.GatewayPort))
		}
	}

	// Allocate new ports
	for _, specPort := range vs.Spec.Service.Ports {
		gatewayPort, err := c.portAllocator.Allocate(vsKey)
		if err != nil {
			// Rollback allocations
			for _, alloc := range allocatedPorts {
				c.portAllocator.Release(vsKey, int(alloc.GatewayPort))
			}
			return fmt.Errorf("failed to allocate gateway port: %w", err)
		}

		protocol := specPort.Protocol
		if protocol == "" {
			protocol = "TCP"
		}

		allocatedPorts = append(allocatedPorts, gpuv1alpha1.AllocatedPort{
			Name:        specPort.Name,
			ServicePort: specPort.Port,
			// TODO: This is not correct, should be the port from the pod list - lookup: targetPort key to a GW 10000+X allocation
			TargetPort:  specPort.TargetPort,
			GatewayPort: int32(gatewayPort),
			Protocol:    protocol,
		})
	}

	vs.Status.AllocatedPorts = allocatedPorts
	return nil
}

func (c *Controller) getMatchingPods(vs *gpuv1alpha1.VirtualService) []*VirtualPodInfo {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	selector := labels.SelectorFromSet(vs.Spec.Service.Selector)
	matchingPods := []*VirtualPodInfo{}

	for _, pod := range c.virtualPods {
		if pod.Namespace == vs.Namespace && selector.Matches(labels.Set(pod.Labels)) {
			matchingPods = append(matchingPods, pod)
		}
	}

	return matchingPods
}

func (c *Controller) ensureGeneratedService(ctx context.Context, vs *gpuv1alpha1.VirtualService) error {
	serviceName := vs.Name
	namespace := vs.Namespace

	// Build desired Service
	desiredService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				AnnotationManagedBy:    AnnotationManagedByValue,
				AnnotationOwnerService: vs.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(vs, gpuv1alpha1.GroupVersion.WithKind("VirtualService")),
			},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: c.gatewayLabels, // Selects the gateway pod
			Ports:    []corev1.ServicePort{},
		},
	}

	// Build ports - map user-facing port to gateway port
	for _, allocPort := range vs.Status.AllocatedPorts {
		protocol := corev1.ProtocolTCP
		if allocPort.Protocol != "" {
			protocol = corev1.Protocol(allocPort.Protocol)
		}

		servicePort := corev1.ServicePort{
			Name:       allocPort.Name,
			Port:       allocPort.ServicePort,
			TargetPort: intstr.FromInt(int(allocPort.GatewayPort)),
			Protocol:   protocol,
		}
		desiredService.Spec.Ports = append(desiredService.Spec.Ports, servicePort)
	}

	// Check if Service exists
	existingService, err := c.serviceLister.Services(namespace).Get(serviceName)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new Service
			_, err := c.kubeClient.CoreV1().Services(namespace).Create(ctx, desiredService, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create Service: %w", err)
			}
			klog.Infof("Created Service %s/%s for VirtualService", namespace, serviceName)
			return nil
		}
		return fmt.Errorf("failed to get Service: %w", err)
	}

	// Check if existing Service is owned by this VirtualService
	if !isOwnedBy(existingService, vs) {
		return errors.NewAlreadyExists(corev1.Resource("service"), serviceName)
	}

	// Check if Service needs updating
	needsUpdate := false
	if !equality.Semantic.DeepEqual(existingService.Labels, desiredService.Labels) {
		needsUpdate = true
	}
	if !equality.Semantic.DeepEqual(existingService.Spec.Selector, desiredService.Spec.Selector) {
		needsUpdate = true
	}
	if !equality.Semantic.DeepEqual(existingService.Spec.Ports, desiredService.Spec.Ports) {
		needsUpdate = true
	}

	if !needsUpdate {
		klog.V(5).Infof("Service %s/%s is up-to-date, skipping update", namespace, serviceName)
		return nil
	}

	// Update existing Service
	existingService.Spec.Selector = desiredService.Spec.Selector
	existingService.Spec.Ports = desiredService.Spec.Ports
	existingService.Labels = desiredService.Labels

	_, err = c.kubeClient.CoreV1().Services(namespace).Update(ctx, existingService, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update Service: %w", err)
	}

	klog.V(4).Infof("Updated Service %s/%s for VirtualService", namespace, serviceName)
	return nil
}

func (c *Controller) configureHAProxy(ctx context.Context, vs *gpuv1alpha1.VirtualService, pods []*VirtualPodInfo) error {
	vsKey := fmt.Sprintf("%s/%s", vs.Namespace, vs.Name)

	// Build HAProxy configuration for this VirtualService
	configs := []haproxy.ListenerConfig{}

	for _, allocPort := range vs.Status.AllocatedPorts {
		// Build backend servers from matching pods
		backends := []haproxy.Backend{}
		for _, pod := range pods {
			// Calculate the backend port using the Wireproxy tunnel formula:
			// ListenPort = 10000 + proxySlotID * 100 + portID
			// where portID is the index of the target port in the sorted container ports
			backendPort, err := calculateBackendPort(pod, allocPort.TargetPort)
			if err != nil {
				klog.Warningf("Failed to calculate backend port for pod %s/%s, target port %d: %v. Skipping pod.",
					pod.Namespace, pod.Name, allocPort.TargetPort, err)
				continue
			}

			backends = append(backends, haproxy.Backend{
				Name:    fmt.Sprintf("%s-%s", pod.Namespace, pod.Name),
				Address: pod.WireguardIP,
				Port:    backendPort,
			})
		}

		config := haproxy.ListenerConfig{
			Name:     fmt.Sprintf("%s-port-%d", vsKey, allocPort.ServicePort),
			Port:     int(allocPort.GatewayPort),
			Backends: backends,
		}
		configs = append(configs, config)
	}

	// Apply configuration to HAProxy
	if err := c.haproxyManager.Configure(vsKey, configs); err != nil {
		return fmt.Errorf("failed to configure HAProxy: %w", err)
	}

	klog.V(4).Infof("Configured HAProxy for VirtualService %s with %d listeners", vsKey, len(configs))
	return nil
}

// calculateBackendPort calculates the Wireproxy tunnel listen port for a given target port
// Formula: ListenPort = 10000 + proxySlotID * 100 + portID
// where portID is the index of the target port in the sorted container ports
func calculateBackendPort(pod *VirtualPodInfo, targetPort int32) (int, error) {
	// Find the portID (index) of the targetPort in the sorted container ports
	portID := -1
	for i, port := range pod.ContainerPorts {
		if port == targetPort {
			portID = i
			break
		}
	}

	if portID == -1 {
		return 0, fmt.Errorf("target port %d not found in pod's container ports %v", targetPort, pod.ContainerPorts)
	}

	// Apply the formula: ListenPort = 10000 + proxySlotID * 100 + portID + 1
	listenPort := 10000 + pod.ProxySlotID*100 + portID + 1

	return listenPort, nil
}

func (c *Controller) handleVirtualServiceFinalization(ctx context.Context, vs *gpuv1alpha1.VirtualService) error {
	klog.Infof("Finalizing VirtualService %s/%s", vs.Namespace, vs.Name)

	if !containsString(vs.Finalizers, VirtualServiceFinalizer) {
		return nil
	}

	vsKey := fmt.Sprintf("%s/%s", vs.Namespace, vs.Name)

	// Remove HAProxy configuration
	if err := c.haproxyManager.Remove(vsKey); err != nil {
		klog.Errorf("Failed to remove HAProxy config for %s: %v", vsKey, err)
		// Continue with cleanup
	}

	// Release allocated ports
	for _, allocPort := range vs.Status.AllocatedPorts {
		c.portAllocator.Release(vsKey, int(allocPort.GatewayPort))
	}

	// Delete generated Service (if owned)
	serviceName := vs.Name
	existingService, err := c.serviceLister.Services(vs.Namespace).Get(serviceName)
	if err == nil && isOwnedBy(existingService, vs) {
		err := c.kubeClient.CoreV1().Services(vs.Namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			klog.Errorf("Failed to delete Service %s/%s: %v", vs.Namespace, serviceName, err)
		}
	}

	// Remove finalizer
	vs.Finalizers = removeString(vs.Finalizers, VirtualServiceFinalizer)
	_, err = c.updateVirtualService(ctx, vs)
	if err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}

	klog.Infof("Finalized VirtualService %s/%s", vs.Namespace, vs.Name)
	return nil
}

func (c *Controller) setConditionAndUpdate(ctx context.Context, vs *gpuv1alpha1.VirtualService, condition metav1.Condition) error {
	// Keep a copy of the original status to check if anything changed
	originalStatus := vs.Status.DeepCopy()

	// Update or add condition
	found := false
	for i, existingCondition := range vs.Status.Conditions {
		if existingCondition.Type == condition.Type {
			found = true
			// Only update if status or reason changed
			if existingCondition.Status != condition.Status || existingCondition.Reason != condition.Reason {
				vs.Status.Conditions[i] = condition
			}
			break
		}
	}

	if !found {
		vs.Status.Conditions = append(vs.Status.Conditions, condition)
	}

	vs.Status.ObservedGeneration = vs.Generation

	// Only update status if it actually changed
	if equality.Semantic.DeepEqual(originalStatus, &vs.Status) {
		klog.V(5).Infof("VirtualService %s/%s status unchanged, skipping update", vs.Namespace, vs.Name)
		return nil
	}

	// Update status
	return c.updateVirtualServiceStatus(ctx, vs)
}

func isOwnedBy(service *corev1.Service, vs *gpuv1alpha1.VirtualService) bool {
	for _, ownerRef := range service.OwnerReferences {
		if ownerRef.UID == vs.UID {
			return true
		}
	}
	return false
}
