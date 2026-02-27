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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gpuv1alpha1 "gitlab.devklarka.cz/ai/gpu-provider/api/v1alpha1"
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

	// Verify Service selector matches gateway labels
	for k, v := range te.gatewayLabels {
		if svc.Spec.Selector[k] != v {
			t.Errorf("Service selector mismatch: expected %s=%s, got %s", k, v, svc.Spec.Selector[k])
		}
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

	// Step 11: Verify generated Service is deleted
	t.Log("Verifying generated Service is deleted...")
	te.waitForServiceDeleted(t, vsName, vsNamespace)

	t.Log("TestVirtualServiceBasicLifecycle completed successfully")
}
