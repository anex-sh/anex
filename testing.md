# Gateway Controller Integration Tests Design Document

## Overview

This document outlines the design for integration tests of the Gateway Controller using [envtest](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest). The tests will verify end-to-end behavior of the VirtualService controller, ensuring all features work correctly at a high level.

## Testing Framework

### Technology Stack

- **envtest**: From `sigs.k8s.io/controller-runtime/pkg/envtest` (already a dependency in go.mod)
- **Standard Go testing**: Using `testing` package
- **Fake/Mock HAProxy Manager**: Since we cannot run a real HAProxy in tests, we'll create a mock implementation that records calls

### Test Location

```
internal/gateway/controller_test.go      # Main integration test file
internal/gateway/suite_test.go           # Test suite setup (envtest bootstrap)
internal/gateway/haproxy/mock_manager.go # Mock HAProxy manager for testing
```

## Test Environment Setup

### Suite Setup (`suite_test.go`)

The test suite will:

1. Start envtest (kube-apiserver + etcd)
2. Install the VirtualService CRD
3. Create a fake "gateway pod" in the cluster (required by the controller)
4. Initialize the controller with a mock HAProxy manager
5. Start informers and the controller

```go
// Pseudo-code structure
var (
    testEnv    *envtest.Environment
    k8sClient  client.Client
    ctx        context.Context
    cancel     context.CancelFunc
    controller *gateway.Controller
    mockHAProxy *haproxy.MockManager
)

func TestMain(m *testing.M) {
    // Setup envtest
    // Install CRD from deploy/chart/crds/gpu-provider.glami-ml.com_virtualservices.yaml
    // Create gateway pod
    // Start controller
    // Run tests
    // Teardown
}
```

### Mock HAProxy Manager

Create a mock implementation that:
- Records all `Configure()` and `Remove()` calls
- Stores the listener configurations passed to it
- Allows test assertions on what was configured

```go
type MockManager struct {
    mu          sync.Mutex
    Configs     map[string][]ListenerConfig // ownerKey -> configs
    ConfigCalls []ConfigureCall
    RemoveCalls []string
}

type ConfigureCall struct {
    OwnerKey string
    Configs  []ListenerConfig
}
```

## Test Scenarios

### 1. Basic VirtualService Lifecycle

**Test: `TestVirtualServiceBasicLifecycle`**

**Purpose**: Verify that creating, updating, and deleting a VirtualService works correctly.

**Steps**:
1. Create a VirtualService with a single port
2. Assert:
   - Finalizer is added
   - Status shows `Ready=True` with `Reason=Reconciled`
   - `status.allocatedPorts` contains one entry with a gatewayPort in range 6000-9999
   - Generated Service is created with correct name, namespace, and port mapping
   - HAProxy mock received `Configure()` call with correct listener config
3. Delete the VirtualService
4. Assert:
   - HAProxy mock received `Remove()` call
   - Generated Service is deleted
   - VirtualService is fully removed (finalizer processed)

---

### 2. Multi-Port VirtualService

**Test: `TestVirtualServiceMultiplePorts`**

**Purpose**: Verify that VirtualServices with multiple ports allocate unique gateway ports for each.

**Steps**:
1. Create a VirtualService with 3 ports (e.g., 80→8080, 443→8443, 9090→9090)
2. Assert:
   - `status.allocatedPorts` has 3 entries
   - Each gatewayPort is unique
   - Generated Service has 3 ServicePorts, each with correct `port` and `targetPort` (= gatewayPort)
   - HAProxy mock received config with 3 listeners

---

### 3. Port Allocation Stability Across Reconciles

**Test: `TestPortAllocationStability`**

**Purpose**: Verify that port allocations are stable and reused across reconciliation cycles.

**Steps**:
1. Create a VirtualService with 2 ports
2. Record the allocated gateway ports from status
3. Trigger a reconcile (e.g., by adding an annotation to the VirtualService)
4. Assert:
   - The gateway ports in status remain the same (no reallocation)
5. Update the VirtualService spec (e.g., change a port)
6. Assert:
   - Ports are reallocated as needed
   - Old allocations are released

---

### 4. Generated Service Creation and Ownership

**Test: `TestGeneratedServiceOwnership`**

**Purpose**: Verify the generated Service has correct OwnerReference and labels.

**Steps**:
1. Create a VirtualService
2. Wait for the generated Service to appear
3. Assert:
   - Service name matches VirtualService name
   - Service namespace matches VirtualService namespace
   - Service has OwnerReference pointing to VirtualService with `controller=true`
   - Service has labels: `gpu-provider.glami-ml.com/managed-by: virtualservice-controller`
   - Service type is ClusterIP
   - Service selector matches gateway pod labels (from test setup)

---

### 5. Service Conflict Detection

**Test: `TestServiceConflictDetection`**

**Purpose**: Verify that if a Service with the same name already exists (not owned by VirtualService), the controller sets a conflict condition.

**Steps**:
1. Pre-create a Service with name "my-vsvc" in namespace "default" (without VirtualService ownership)
2. Create a VirtualService named "my-vsvc" in namespace "default"
3. Assert:
   - VirtualService status shows `Ready=False`
   - Reason is `ServiceConflict`
   - Message indicates the Service already exists
4. Delete the pre-existing Service
5. Trigger reconcile
6. Assert:
   - VirtualService becomes `Ready=True`
   - Generated Service is now created with correct ownership

---

### 6. Spec Validation — Protocol

**Test: `TestProtocolValidation`**

**Purpose**: Verify that non-TCP protocols are rejected.

**Steps**:
1. Create a VirtualService with `protocol: UDP`
2. Assert:
   - Status shows `Ready=False`
   - Reason is `UnsupportedSpec`
   - Message mentions protocol must be TCP

---

### 7. Spec Validation — Port Range

**Test: `TestPortRangeValidation`**

**Purpose**: Verify that invalid port numbers are rejected.

**Steps**:
1. Create a VirtualService with `port: 0`
2. Assert:
   - Status shows `Ready=False`
   - Reason is `UnsupportedSpec`
   - Message mentions port must be between 1 and 65535
3. Create a VirtualService with `port: 70000`
4. Assert same error condition

---

### 8. Virtual Pod Matching

**Test: `TestVirtualPodMatching`**

**Purpose**: Verify that only pods with `virtual: "true"` annotation and matching labels are selected as backends.

**Steps**:
1. Create a VirtualService with selector `app: my-app`
2. Create Pod A with labels `app: my-app` but NO `virtual: "true"` annotation
3. Create Pod B with labels `app: my-app` AND annotation `virtual: "true"` + `gpu-provider.glami.cz/proxy-slot-id: "5"`
4. Create Pod C with labels `app: other-app` AND annotation `virtual: "true"`
5. Wait for reconcile
6. Assert:
   - HAProxy mock config includes only Pod B as a backend
   - Backend has correct Wireguard IP: `10.254.254.16` (11 + 5 = 16)

---

### 9. Backend Port Calculation (Wireproxy Formula)

**Test: `TestBackendPortCalculation`**

**Purpose**: Verify the backend port formula: `ListenPort = 10000 + proxySlotID * 100 + portID + 1`

**Steps**:
1. Create a virtual pod with:
   - `gpu-provider.glami.cz/proxy-slot-id: "3"`
   - Container ports: 8080, 9090 (sorted order)
2. Create a VirtualService targeting port 9090
3. Wait for reconcile
4. Assert:
   - portID for 9090 is 1 (index in sorted [8080, 9090])
   - Backend port = 10000 + 3*100 + 1 + 1 = 10302
   - HAProxy mock config shows backend with port 10302

---

### 10. Pod Lifecycle Events

**Test: `TestPodAddRemoveUpdatesBackends`**

**Purpose**: Verify that adding/removing pods dynamically updates HAProxy backends.

**Steps**:
1. Create a VirtualService with selector `app: worker`
2. Assert HAProxy has 0 backends initially
3. Create Pod 1 with `app: worker`, `virtual: "true"`, slot 1
4. Wait for reconcile
5. Assert HAProxy now has 1 backend
6. Create Pod 2 with `app: worker`, `virtual: "true"`, slot 2
7. Wait for reconcile
8. Assert HAProxy now has 2 backends
9. Delete Pod 1
10. Wait for reconcile
11. Assert HAProxy now has 1 backend (only Pod 2)

---

### 11. VirtualService Finalization

**Test: `TestVirtualServiceFinalization`**

**Purpose**: Verify that finalization cleans up all resources properly.

**Steps**:
1. Create a VirtualService
2. Wait for it to be Ready
3. Record allocated ports and HAProxy configs
4. Delete the VirtualService
5. Assert:
   - HAProxy `Remove()` was called with correct owner key
   - Generated Service no longer exists
   - Port allocations are released (create another VirtualService, verify it can get same ports)
   - VirtualService is fully deleted (no finalizer blocking)

---

### 12. Status ObservedGeneration Tracking

**Test: `TestObservedGenerationTracking`**

**Purpose**: Verify that status.observedGeneration is updated correctly.

**Steps**:
1. Create a VirtualService
2. Wait for Ready
3. Assert `status.observedGeneration == metadata.generation`
4. Update the VirtualService spec
5. Wait for reconcile
6. Assert `status.observedGeneration` matches new `metadata.generation`

---

### 13. Condition LastTransitionTime

**Test: `TestConditionTransitionTime`**

**Purpose**: Verify that condition lastTransitionTime only changes when status changes.

**Steps**:
1. Create a VirtualService
2. Wait for `Ready=True`
3. Record `lastTransitionTime`
4. Trigger a no-op reconcile (e.g., add annotation)
5. Assert `lastTransitionTime` is unchanged (status didn't change)
6. Cause a failure (e.g., create conflicting Service)
7. Assert `lastTransitionTime` is updated (status changed to False)

---

### 14. Multiple VirtualServices with Port Allocation

**Test: `TestMultipleVirtualServicesPortAllocation`**

**Purpose**: Verify that multiple VirtualServices get unique port allocations.

**Steps**:
1. Create VirtualService A with 2 ports
2. Create VirtualService B with 2 ports
3. Create VirtualService C with 1 port
4. Assert:
   - All 5 allocated gatewayPorts are unique
   - Each VirtualService has its own generated Service
   - HAProxy mock has configs for all 3 owner keys

---

### 15. VirtualService Update — Port Addition

**Test: `TestVirtualServicePortAddition`**

**Purpose**: Verify that adding ports to an existing VirtualService works correctly.

**Steps**:
1. Create a VirtualService with 1 port
2. Wait for Ready
3. Record allocated port
4. Update VirtualService to have 2 ports
5. Wait for reconcile
6. Assert:
   - Status now has 2 allocated ports
   - Generated Service now has 2 ports
   - HAProxy config updated with 2 listeners

---

### 16. VirtualService Update — Port Removal

**Test: `TestVirtualServicePortRemoval`**

**Purpose**: Verify that removing ports from an existing VirtualService releases allocations.

**Steps**:
1. Create a VirtualService with 3 ports
2. Wait for Ready
3. Record allocated ports
4. Update VirtualService to have 1 port
5. Wait for reconcile
6. Assert:
   - Status now has 1 allocated port
   - Previously allocated ports are released
   - Generated Service now has 1 port
   - HAProxy config updated with 1 listener

---

### 17. Namespace Isolation

**Test: `TestNamespaceIsolation`**

**Purpose**: Verify that VirtualServices in different namespaces are independent.

**Steps**:
1. Create namespace "ns-a" and "ns-b"
2. Create VirtualService "my-svc" in "ns-a"
3. Create VirtualService "my-svc" in "ns-b" (same name, different namespace)
4. Assert:
   - Both have separate status and allocations
   - Both have their own generated Services
   - HAProxy has separate configs with different owner keys

---

### 18. Pod Not Ready Handling

**Test: `TestPodReadinessHandling`**

**Purpose**: Verify that pod readiness (Running phase) affects backend inclusion.

**Steps**:
1. Create a VirtualService
2. Create a virtual pod in `Pending` phase
3. Assert: HAProxy backend list does NOT include the pod (or includes but marked)
4. Update pod to `Running` phase
5. Assert: HAProxy backend now includes the pod
6. Update pod to `Failed` phase
7. Assert: Pod removed from backends

---

### 19. Empty Selector Edge Case

**Test: `TestEmptySelector`**

**Purpose**: Verify behavior with an empty pod selector (matches all virtual pods in namespace).

**Steps**:
1. Create a VirtualService with empty selector `{}`
2. Create multiple virtual pods with different labels
3. Assert:
   - All virtual pods in the same namespace are matched
   - All appear as backends in HAProxy config

---

### 20. Target Port Not Found in Pod

**Test: `TestTargetPortNotFoundInPod`**

**Purpose**: Verify that if a pod doesn't expose the targetPort, it's skipped with a warning.

**Steps**:
1. Create a VirtualService targeting port 9999
2. Create a virtual pod with container ports [8080, 8081] (no 9999)
3. Wait for reconcile
4. Assert:
   - VirtualService is Ready (not an error condition)
   - HAProxy config has 0 backends for this listener
   - Log contains warning about port not found

---

## Test Utilities

### Helper Functions

```go
// createVirtualService creates a VirtualService and waits for it to be accepted
func createVirtualService(ctx context.Context, name, namespace string, ports []v1alpha1.ServicePort, selector map[string]string) *v1alpha1.VirtualService

// waitForCondition waits for a VirtualService to have a specific condition
func waitForCondition(ctx context.Context, name, namespace string, conditionType string, status metav1.ConditionStatus, timeout time.Duration) error

// createVirtualPod creates a pod with virtual annotation and proxy slot
func createVirtualPod(ctx context.Context, name, namespace string, labels map[string]string, proxySlotID int, containerPorts []int32) *corev1.Pod

// getGeneratedService retrieves the Service generated for a VirtualService
func getGeneratedService(ctx context.Context, name, namespace string) (*corev1.Service, error)

// assertHAProxyConfig asserts the mock HAProxy has the expected configuration
func assertHAProxyConfig(t *testing.T, mock *MockManager, ownerKey string, expectedListeners int, expectedBackends map[int]int)
```

### Constants

```go
const (
    testNamespace        = "test-ns"
    gatewayPodName       = "test-gateway"
    gatewayPodNamespace  = "gateway-ns"
    defaultTimeout       = 10 * time.Second
    pollInterval         = 100 * time.Millisecond
)
```

## Implementation Notes

### Controller Initialization for Tests

The controller needs to be initialized with a mock HAProxy manager. We need to either:

1. **Option A**: Add a constructor option/interface to inject the HAProxy manager
2. **Option B**: Create a `NewControllerForTesting` function that accepts a mock
3. **Option C**: Make the HAProxy manager an interface and use dependency injection

**Recommendation**: Option C (interface) is the cleanest approach. Define:

```go
// HAProxyConfigurer is the interface for configuring HAProxy
type HAProxyConfigurer interface {
    Configure(ownerKey string, configs []ListenerConfig) error
    Remove(ownerKey string) error
}
```

Then `haproxy.Manager` implements this interface, and tests can provide a mock.

### CRD Installation

The test suite must install the CRD before starting. Use:

```go
testEnv.CRDDirectoryPaths = []string{
    filepath.Join("..", "..", "..", "deploy", "chart", "crds"),
}
```

### Gateway Pod Setup

The controller requires a gateway pod to exist. Create a minimal pod in `BeforeSuite`:

```go
gatewayPod := &corev1.Pod{
    ObjectMeta: metav1.ObjectMeta{
        Name:      "test-gateway",
        Namespace: "gateway-ns",
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
```

## Dependencies to Add

No new dependencies required — `sigs.k8s.io/controller-runtime` (which includes envtest) is already in go.mod.

## Running Tests

```bash
# Run all gateway controller tests
go test -v ./internal/gateway/...

# Run with race detector
go test -race -v ./internal/gateway/...

# Run specific test
go test -v -run TestVirtualServiceBasicLifecycle ./internal/gateway/...
```

## Future Considerations

1. **Performance Tests**: Add benchmarks for port allocation under high load
2. **Chaos Testing**: Test controller recovery after restarts
3. **Concurrency Tests**: Multiple concurrent VirtualService creates/updates
4. **Leader Election**: Test behavior in multi-replica scenarios (if applicable)

## Appendix: Code Changes Required

### 1. Extract HAProxy Interface

File: `internal/gateway/haproxy/interface.go`

```go
package haproxy

// Configurer defines the interface for HAProxy configuration management
type Configurer interface {
    Configure(ownerKey string, configs []ListenerConfig) error
    Remove(ownerKey string) error
}
```

### 2. Update Controller to Use Interface

File: `internal/gateway/controller.go`

```go
type Controller struct {
    // ...
    haproxyManager haproxy.Configurer  // Change from *haproxy.Manager
    // ...
}
```

### 3. Add Testing Constructor

File: `internal/gateway/controller.go`

```go
// NewControllerForTesting creates a controller with a custom HAProxy manager (for testing)
func NewControllerForTesting(
    kubeClient kubernetes.Interface,
    dynamicClient dynamic.Interface,
    // ... other params ...
    haproxyManager haproxy.Configurer,  // Accept interface instead of creating manager
) (*Controller, error) {
    // ...
}
```

Or modify `NewController` to accept an optional `haproxy.Configurer` parameter.
