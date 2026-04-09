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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gpuv1alpha1 "github.com/anex-sh/anex/api/v1alpha1"
)

// TestVirtualServiceBasicLifecycle tests the basic lifecycle of a VirtualService:
// create, verify ready state, verify generated service, verify HAProxy config, delete, verify cleanup.
func TestVirtualServiceBasicLifecycle(t *testing.T) {
	// Setup test environment
	te := setupTestEnv(t)
	defer te.teardown(t)

	vsName := "test-vs"
	vsNamespace := testNamespace

	// Define a single port for the VirtualService
	ports := []gpuv1alpha1.ServicePort{
		{
			Name:       "http",
			Port:       80,
			TargetPort: 8080,
			Protocol:   "TCP",
		},
	}
	selector := map[string]string{
		"app": "my-app",
	}

	// Step 1: Create a VirtualService with a single port
	t.Log("Creating VirtualService...")
	te.createVirtualService(t, vsName, vsNamespace, ports, selector)

	// Step 2: Wait for and verify finalizer is added
	t.Log("Waiting for finalizer...")
	te.waitForFinalizer(t, vsName, vsNamespace)

	// Step 3: Wait for Ready=True condition
	t.Log("Waiting for Ready=True condition...")
	te.waitForCondition(t, vsName, vsNamespace, gpuv1alpha1.ConditionTypeReady, metav1.ConditionTrue)

	// Step 4: Verify status has allocated ports
	t.Log("Verifying allocated ports...")
	vs := te.getVirtualService(t, vsName, vsNamespace)

	if len(vs.Status.AllocatedPorts) != 1 {
		t.Fatalf("Expected 1 allocated port, got %d", len(vs.Status.AllocatedPorts))
	}

	allocatedPort := vs.Status.AllocatedPorts[0]
	if allocatedPort.ServicePort != 80 {
		t.Errorf("Expected ServicePort=80, got %d", allocatedPort.ServicePort)
	}
	if allocatedPort.TargetPort != 8080 {
		t.Errorf("Expected TargetPort=8080, got %d", allocatedPort.TargetPort)
	}
	if allocatedPort.GatewayPort < DefaultPortRangeStart || allocatedPort.GatewayPort > DefaultPortRangeEnd {
		t.Errorf("GatewayPort %d is outside expected range [%d, %d]",
			allocatedPort.GatewayPort, DefaultPortRangeStart, DefaultPortRangeEnd)
	}

	// Step 5: Verify the Ready condition has Reason=Reconciled
	for _, cond := range vs.Status.Conditions {
		if cond.Type == gpuv1alpha1.ConditionTypeReady {
			if cond.Reason != gpuv1alpha1.ReasonReconciled {
				t.Errorf("Expected Ready condition Reason=%s, got %s", gpuv1alpha1.ReasonReconciled, cond.Reason)
			}
			break
		}
	}

	// Step 6: Verify generated Service is created
	t.Log("Verifying generated Service...")
	svc := te.waitForService(t, vsName, vsNamespace)

	// Verify Service properties
	if svc.Spec.Type != "ClusterIP" {
		t.Errorf("Expected Service type=ClusterIP, got %s", svc.Spec.Type)
	}

	// Verify Service has owner reference pointing to VirtualService
	hasOwnerRef := false
	for _, ownerRef := range svc.OwnerReferences {
		if ownerRef.Kind == "VirtualService" && ownerRef.Name == vsName {
			hasOwnerRef = true
			if ownerRef.Controller == nil || !*ownerRef.Controller {
				t.Error("Expected OwnerReference.Controller=true")
			}
			break
		}
	}
	if !hasOwnerRef {
		t.Error("Generated Service does not have OwnerReference to VirtualService")
	}

	// Verify Service has managed-by label
	if svc.Labels[AnnotationManagedBy] != AnnotationManagedByValue {
		t.Errorf("Expected Service label %s=%s, got %s",
			AnnotationManagedBy, AnnotationManagedByValue, svc.Labels[AnnotationManagedBy])
	}

	// Verify Service is headless (no selector, ClusterIP=None)
	if len(svc.Spec.Selector) != 0 {
		t.Errorf("Expected Service to have no selector, got %v", svc.Spec.Selector)
	}
	if svc.Spec.ClusterIP != "None" {
		t.Errorf("Expected Service ClusterIP=None (headless), got %s", svc.Spec.ClusterIP)
	}

	// Verify Service port mapping
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("Expected 1 Service port, got %d", len(svc.Spec.Ports))
	}
	svcPort := svc.Spec.Ports[0]
	if svcPort.Port != 80 {
		t.Errorf("Expected Service port=80, got %d", svcPort.Port)
	}
	if svcPort.TargetPort.IntVal != allocatedPort.GatewayPort {
		t.Errorf("Expected Service targetPort=%d (gatewayPort), got %d",
			allocatedPort.GatewayPort, svcPort.TargetPort.IntVal)
	}

	// Step 6b: Verify EndpointSlice is created with gateway IP
	t.Log("Verifying EndpointSlice...")
	eps := te.waitForEndpointSlice(t, vsName, vsNamespace)
	if len(eps.Endpoints) != 1 || len(eps.Endpoints[0].Addresses) != 1 || eps.Endpoints[0].Addresses[0] != te.gatewayIP {
		t.Errorf("Expected EndpointSlice to have gateway IP %s, got %v", te.gatewayIP, eps.Endpoints)
	}
	if len(eps.Ports) != 1 || *eps.Ports[0].Port != allocatedPort.GatewayPort {
		t.Errorf("Expected EndpointSlice port=%d, got %v", allocatedPort.GatewayPort, eps.Ports)
	}

	// Step 7: Verify HAProxy mock received Configure() call
	t.Log("Verifying HAProxy configuration...")
	haproxyConfigs := te.mockHAProxy.GetConfigs(vsNamespace + "/" + vsName)
	if haproxyConfigs == nil {
		t.Fatal("HAProxy mock did not receive Configure() call")
	}
	if len(haproxyConfigs) != 1 {
		t.Errorf("Expected 1 HAProxy listener config, got %d", len(haproxyConfigs))
	}
	if len(haproxyConfigs) > 0 {
		listenerConfig := haproxyConfigs[0]
		if listenerConfig.Port != int(allocatedPort.GatewayPort) {
			t.Errorf("Expected HAProxy listener port=%d, got %d",
				allocatedPort.GatewayPort, listenerConfig.Port)
		}
	}

	// Step 8: Delete the VirtualService
	t.Log("Deleting VirtualService...")
	te.deleteVirtualService(t, vsName, vsNamespace)

	// Step 9: Wait for VirtualService to be fully deleted
	t.Log("Waiting for VirtualService to be deleted...")
	te.waitForVirtualServiceDeleted(t, vsName, vsNamespace)

	// Step 10: Verify HAProxy mock received Remove() call
	t.Log("Verifying HAProxy Remove() was called...")
	removeCalls := te.mockHAProxy.RemoveCalls
	found := false
	for _, key := range removeCalls {
		if key == vsNamespace+"/"+vsName {
			found = true
			break
		}
	}
	if !found {
		t.Error("HAProxy mock did not receive Remove() call for VirtualService")
	}

	// Step 11: Verify generated Service and EndpointSlice are deleted
	t.Log("Verifying generated Service is deleted...")
	te.waitForServiceDeleted(t, vsName, vsNamespace)
	t.Log("Verifying EndpointSlice is deleted...")
	te.waitForEndpointSliceDeleted(t, vsName, vsNamespace)

	t.Log("TestVirtualServiceBasicLifecycle completed successfully")
}

// TestVirtualServiceMultiplePorts tests that VirtualServices with multiple ports allocate unique gateway ports for each.
func TestVirtualServiceMultiplePorts(t *testing.T) {
	// Setup test environment
	te := setupTestEnv(t)
	defer te.teardown(t)

	vsName := "test-vs-multiport"
	vsNamespace := testNamespace

	// Define 3 ports for the VirtualService
	ports := []gpuv1alpha1.ServicePort{
		{
			Name:       "http",
			Port:       80,
			TargetPort: 8080,
			Protocol:   "TCP",
		},
		{
			Name:       "https",
			Port:       443,
			TargetPort: 8443,
			Protocol:   "TCP",
		},
		{
			Name:       "metrics",
			Port:       9090,
			TargetPort: 9090,
			Protocol:   "TCP",
		},
	}
	selector := map[string]string{
		"app": "my-app",
	}

	// Step 1: Create a VirtualService with 3 ports
	t.Log("Creating VirtualService with 3 ports...")
	te.createVirtualService(t, vsName, vsNamespace, ports, selector)

	// Step 2: Wait for Ready condition
	t.Log("Waiting for Ready=True condition...")
	te.waitForCondition(t, vsName, vsNamespace, gpuv1alpha1.ConditionTypeReady, metav1.ConditionTrue)

	// Step 3: Verify status has 3 allocated ports
	t.Log("Verifying allocated ports...")
	vs := te.getVirtualService(t, vsName, vsNamespace)

	if len(vs.Status.AllocatedPorts) != 3 {
		t.Fatalf("Expected 3 allocated ports, got %d", len(vs.Status.AllocatedPorts))
	}

	// Verify each gatewayPort is unique
	seenPorts := make(map[int32]bool)
	for _, allocPort := range vs.Status.AllocatedPorts {
		if seenPorts[allocPort.GatewayPort] {
			t.Errorf("Duplicate gateway port found: %d", allocPort.GatewayPort)
		}
		seenPorts[allocPort.GatewayPort] = true

		// Verify port is in valid range
		if allocPort.GatewayPort < DefaultPortRangeStart || allocPort.GatewayPort > DefaultPortRangeEnd {
			t.Errorf("GatewayPort %d is outside expected range [%d, %d]",
				allocPort.GatewayPort, DefaultPortRangeStart, DefaultPortRangeEnd)
		}
	}

	// Step 4: Verify generated Service has 3 ports
	t.Log("Verifying generated Service...")
	svc := te.waitForService(t, vsName, vsNamespace)

	if len(svc.Spec.Ports) != 3 {
		t.Fatalf("Expected 3 Service ports, got %d", len(svc.Spec.Ports))
	}

	// Verify each Service port maps to the correct gateway port
	for _, svcPort := range svc.Spec.Ports {
		found := false
		for _, allocPort := range vs.Status.AllocatedPorts {
			if allocPort.ServicePort == svcPort.Port {
				if svcPort.TargetPort.IntVal != allocPort.GatewayPort {
					t.Errorf("Service port %d targetPort=%d does not match allocated gatewayPort=%d",
						svcPort.Port, svcPort.TargetPort.IntVal, allocPort.GatewayPort)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Service port %d not found in allocated ports", svcPort.Port)
		}
	}

	// Step 5: Verify HAProxy mock received config with 3 listeners
	t.Log("Verifying HAProxy configuration...")
	haproxyConfigs := te.mockHAProxy.GetConfigs(vsNamespace + "/" + vsName)
	if len(haproxyConfigs) != 3 {
		t.Errorf("Expected 3 HAProxy listener configs, got %d", len(haproxyConfigs))
	}

	t.Log("TestVirtualServiceMultiplePorts completed successfully")
}

// TestPortAllocationStability tests that port allocations are stable across reconciliation cycles.
func TestPortAllocationStability(t *testing.T) {
	// Setup test environment
	te := setupTestEnv(t)
	defer te.teardown(t)

	vsName := "test-vs-stability"
	vsNamespace := testNamespace

	// Define 2 ports for the VirtualService
	ports := []gpuv1alpha1.ServicePort{
		{
			Name:       "http",
			Port:       80,
			TargetPort: 8080,
			Protocol:   "TCP",
		},
		{
			Name:       "https",
			Port:       443,
			TargetPort: 8443,
			Protocol:   "TCP",
		},
	}
	selector := map[string]string{
		"app": "my-app",
	}

	// Step 1: Create a VirtualService with 2 ports
	t.Log("Creating VirtualService with 2 ports...")
	te.createVirtualService(t, vsName, vsNamespace, ports, selector)

	// Step 2: Wait for Ready condition
	t.Log("Waiting for Ready=True condition...")
	te.waitForCondition(t, vsName, vsNamespace, gpuv1alpha1.ConditionTypeReady, metav1.ConditionTrue)

	// Step 3: Record the allocated gateway ports
	vs := te.getVirtualService(t, vsName, vsNamespace)
	originalAllocations := make(map[int32]int32) // ServicePort -> GatewayPort
	for _, allocPort := range vs.Status.AllocatedPorts {
		originalAllocations[allocPort.ServicePort] = allocPort.GatewayPort
	}
	t.Logf("Original allocations: %v", originalAllocations)

	// Step 4: Trigger a reconcile by adding an annotation
	t.Log("Triggering reconcile with annotation...")
	te.addAnnotationToVirtualService(t, vsName, vsNamespace, "test-trigger", "reconcile-1")

	// Wait a bit for reconcile to complete
	te.waitForCondition(t, vsName, vsNamespace, gpuv1alpha1.ConditionTypeReady, metav1.ConditionTrue)

	// Step 5: Verify gateway ports remain the same
	t.Log("Verifying port allocations are stable...")
	vs = te.getVirtualService(t, vsName, vsNamespace)
	for _, allocPort := range vs.Status.AllocatedPorts {
		originalGatewayPort, exists := originalAllocations[allocPort.ServicePort]
		if !exists {
			t.Errorf("Service port %d not found in original allocations", allocPort.ServicePort)
			continue
		}
		if allocPort.GatewayPort != originalGatewayPort {
			t.Errorf("Gateway port changed for service port %d: %d -> %d",
				allocPort.ServicePort, originalGatewayPort, allocPort.GatewayPort)
		}
	}

	t.Log("TestPortAllocationStability completed successfully")
}

// TestGeneratedServiceOwnership tests that the generated Service has correct OwnerReference and labels.
func TestGeneratedServiceOwnership(t *testing.T) {
	// Setup test environment
	te := setupTestEnv(t)
	defer te.teardown(t)

	vsName := "test-vs-ownership"
	vsNamespace := testNamespace

	ports := []gpuv1alpha1.ServicePort{
		{
			Name:       "http",
			Port:       80,
			TargetPort: 8080,
			Protocol:   "TCP",
		},
	}
	selector := map[string]string{
		"app": "my-app",
	}

	// Step 1: Create a VirtualService
	t.Log("Creating VirtualService...")
	vs := te.createVirtualService(t, vsName, vsNamespace, ports, selector)

	// Step 2: Wait for the generated Service to appear
	t.Log("Waiting for generated Service...")
	svc := te.waitForService(t, vsName, vsNamespace)

	// Step 3: Verify Service name matches VirtualService name
	if svc.Name != vs.Name {
		t.Errorf("Expected Service name=%s, got %s", vs.Name, svc.Name)
	}

	// Step 4: Verify Service namespace matches VirtualService namespace
	if svc.Namespace != vs.Namespace {
		t.Errorf("Expected Service namespace=%s, got %s", vs.Namespace, svc.Namespace)
	}

	// Step 5: Verify Service has OwnerReference pointing to VirtualService with controller=true
	hasOwnerRef := false
	for _, ownerRef := range svc.OwnerReferences {
		if ownerRef.Kind == "VirtualService" && ownerRef.Name == vs.Name {
			hasOwnerRef = true
			if ownerRef.Controller == nil || !*ownerRef.Controller {
				t.Error("Expected OwnerReference.Controller=true")
			}
			if ownerRef.UID != vs.UID {
				t.Errorf("Expected OwnerReference.UID=%s, got %s", vs.UID, ownerRef.UID)
			}
			break
		}
	}
	if !hasOwnerRef {
		t.Error("Generated Service does not have OwnerReference to VirtualService")
	}

	// Step 6: Verify Service has managed-by label
	if svc.Labels[AnnotationManagedBy] != AnnotationManagedByValue {
		t.Errorf("Expected Service label %s=%s, got %s",
			AnnotationManagedBy, AnnotationManagedByValue, svc.Labels[AnnotationManagedBy])
	}

	// Step 7: Verify Service is headless with no selector
	if svc.Spec.Type != "ClusterIP" {
		t.Errorf("Expected Service type=ClusterIP, got %s", svc.Spec.Type)
	}
	if svc.Spec.ClusterIP != "None" {
		t.Errorf("Expected Service ClusterIP=None (headless), got %s", svc.Spec.ClusterIP)
	}
	if len(svc.Spec.Selector) != 0 {
		t.Errorf("Expected Service to have no selector, got %v", svc.Spec.Selector)
	}

	// Step 8: Verify EndpointSlice is created with gateway IP
	t.Log("Verifying EndpointSlice...")
	eps := te.waitForEndpointSlice(t, vsName, vsNamespace)
	if len(eps.Endpoints) != 1 || len(eps.Endpoints[0].Addresses) != 1 || eps.Endpoints[0].Addresses[0] != te.gatewayIP {
		t.Errorf("Expected EndpointSlice to have gateway IP %s, got %v", te.gatewayIP, eps.Endpoints)
	}

	t.Log("TestGeneratedServiceOwnership completed successfully")
}

// TestServiceConflictDetection tests that if a Service with the same name already exists
// (not owned by VirtualService), the controller sets a conflict condition.
func TestServiceConflictDetection(t *testing.T) {
	// Setup test environment
	te := setupTestEnv(t)
	defer te.teardown(t)

	vsName := "test-vs-conflict"
	vsNamespace := testNamespace

	// Step 1: Pre-create a Service with the same name (without VirtualService ownership)
	t.Log("Pre-creating Service...")
	te.createService(t, vsName, vsNamespace, []corev1.ServicePort{
		{
			Name:     "existing",
			Port:     8080,
			Protocol: corev1.ProtocolTCP,
		},
	})

	// Step 2: Create a VirtualService with the same name
	t.Log("Creating VirtualService with conflicting name...")
	ports := []gpuv1alpha1.ServicePort{
		{
			Name:       "http",
			Port:       80,
			TargetPort: 8080,
			Protocol:   "TCP",
		},
	}
	selector := map[string]string{
		"app": "my-app",
	}
	te.createVirtualService(t, vsName, vsNamespace, ports, selector)

	// Step 3: Wait for and verify Ready=False with Reason=ServiceConflict
	t.Log("Waiting for conflict condition...")
	te.waitForCondition(t, vsName, vsNamespace, gpuv1alpha1.ConditionTypeReady, metav1.ConditionFalse)

	vs := te.getVirtualService(t, vsName, vsNamespace)
	for _, cond := range vs.Status.Conditions {
		if cond.Type == gpuv1alpha1.ConditionTypeReady {
			if cond.Reason != gpuv1alpha1.ReasonServiceConflict {
				t.Errorf("Expected Ready condition Reason=%s, got %s",
					gpuv1alpha1.ReasonServiceConflict, cond.Reason)
			}
			if cond.Message == "" {
				t.Error("Expected Ready condition to have a message")
			}
			break
		}
	}

	// Step 4: Delete the pre-existing Service
	t.Log("Deleting conflicting Service...")
	te.deleteService(t, vsName, vsNamespace)

	// Step 5: Trigger reconcile by adding annotation
	t.Log("Triggering reconcile...")
	te.addAnnotationToVirtualService(t, vsName, vsNamespace, "test-trigger", "after-conflict-resolved")

	// Step 6: Verify VirtualService becomes Ready=True
	t.Log("Waiting for Ready=True after conflict resolved...")
	te.waitForCondition(t, vsName, vsNamespace, gpuv1alpha1.ConditionTypeReady, metav1.ConditionTrue)

	// Step 7: Verify generated Service is now created with correct ownership
	svc := te.waitForService(t, vsName, vsNamespace)
	hasOwnerRef := false
	for _, ownerRef := range svc.OwnerReferences {
		if ownerRef.Kind == "VirtualService" && ownerRef.Name == vsName {
			hasOwnerRef = true
			break
		}
	}
	if !hasOwnerRef {
		t.Error("Generated Service should have OwnerReference to VirtualService after conflict resolved")
	}

	t.Log("TestServiceConflictDetection completed successfully")
}

// TestVirtualPodMatching tests that only pods with `virtual: "true"` annotation
// and matching labels are selected as backends.
func TestVirtualPodMatching(t *testing.T) {
	// Setup test environment
	te := setupTestEnv(t)
	defer te.teardown(t)

	vsName := "test-vs-matching"
	vsNamespace := testNamespace

	// Create VirtualService with selector app: my-app
	ports := []gpuv1alpha1.ServicePort{
		{
			Name:       "http",
			Port:       80,
			TargetPort: 8080,
			Protocol:   "TCP",
		},
	}
	selector := map[string]string{
		"app": "my-app",
	}

	// Step 1: Create VirtualService first
	t.Log("Creating VirtualService with selector app=my-app...")
	te.createVirtualService(t, vsName, vsNamespace, ports, selector)
	te.waitForCondition(t, vsName, vsNamespace, gpuv1alpha1.ConditionTypeReady, metav1.ConditionTrue)

	// Step 2: Create Pod A - has matching labels but NO virtual annotation
	t.Log("Creating Pod A (matching labels, NO virtual annotation)...")
	te.createRegularPod(t, "pod-a", vsNamespace, map[string]string{"app": "my-app"})

	// Step 3: Create Pod B - has matching labels AND virtual: "true" + proxy slot
	t.Log("Creating Pod B (matching labels + virtual annotation, slot=5)...")
	te.createVirtualPod(t, "pod-b", vsNamespace, map[string]string{"app": "my-app"}, 5, []int32{8080})

	// Step 4: Create Pod C - has virtual annotation but different labels
	t.Log("Creating Pod C (different labels + virtual annotation)...")
	te.createVirtualPod(t, "pod-c", vsNamespace, map[string]string{"app": "other-app"}, 3, []int32{8080})

	// Step 5: Trigger reconcile and wait for HAProxy to be updated
	t.Log("Triggering reconcile...")
	te.addAnnotationToVirtualService(t, vsName, vsNamespace, "test-trigger", "after-pods-created")
	te.waitForCondition(t, vsName, vsNamespace, gpuv1alpha1.ConditionTypeReady, metav1.ConditionTrue)

	// Give the controller time to process pod events
	te.waitForHAProxyBackends(t, vsNamespace+"/"+vsName, 0, 1)

	// Step 6: Verify HAProxy config includes only Pod B as a backend
	t.Log("Verifying HAProxy configuration...")
	haproxyConfigs := te.mockHAProxy.GetConfigs(vsNamespace + "/" + vsName)
	if len(haproxyConfigs) != 1 {
		t.Fatalf("Expected 1 HAProxy listener config, got %d", len(haproxyConfigs))
	}

	listenerConfig := haproxyConfigs[0]
	if len(listenerConfig.Backends) != 1 {
		t.Fatalf("Expected 1 backend, got %d", len(listenerConfig.Backends))
	}

	backend := listenerConfig.Backends[0]

	// Step 7: Verify backend has correct Wireguard IP: 10.254.254.16 (11 + 5 = 16)
	expectedWgIP := "10.254.254.16" // WireguardSlotOffset (11) + proxySlotID (5)
	if backend.Address != expectedWgIP {
		t.Errorf("Expected backend address=%s, got %s", expectedWgIP, backend.Address)
	}

	// Verify the backend name contains pod-b
	if backend.Name != vsNamespace+"-pod-b" {
		t.Errorf("Expected backend name to contain 'pod-b', got %s", backend.Name)
	}

	t.Log("TestVirtualPodMatching completed successfully")
}
