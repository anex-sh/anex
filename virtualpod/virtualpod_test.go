package virtualpod

import (
	"errors"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyOnFailure,
			Containers: []v1.Container{
				{
					Name:            "c",
					Image:           "busybox",
					ImagePullPolicy: v1.PullIfNotPresent,
				},
			},
		},
		Status: v1.PodStatus{
			Phase: v1.PodPending,
			ContainerStatuses: []v1.ContainerStatus{
				{
					Name:  "c",
					Ready: false,
					State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}},
				},
			},
		},
	}
}

// ---- Helper fake HTTP client to intercept MakeRequest -----
// We cannot easily mock utils.MakeRequest, but we can call methods that do not depend on it directly.
// For PodStatusUpdate we simulate by bypassing network and adjusting vp fields, then verifying transitions via
// handleContainerStart/Termination/Restart and podShouldRestart.

func TestReadContainerState(t *testing.T) {
	cases := []struct {
		name string
		cs   v1.ContainerState
		exp  ContainerState
	}{
		{"waiting", v1.ContainerState{Waiting: &v1.ContainerStateWaiting{}}, ContainerStateWaiting},
		{"running", v1.ContainerState{Running: &v1.ContainerStateRunning{}}, ContainerStateRunning},
		{"terminated", v1.ContainerState{Terminated: &v1.ContainerStateTerminated{}}, ContainerStateTerminated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := readContainerState(&tc.cs)
			if got != tc.exp {
				t.Fatalf("expected %s, got %s", tc.exp, got)
			}
		})
	}
}

func TestSetConditionAddAndUpdate(t *testing.T) {
	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, nil, nil, "")
	// Add new condition
	vp.setCondition(v1.PodReady, v1.ConditionTrue)
	if len(vp.pod.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(vp.pod.Status.Conditions))
	}
	if vp.pod.Status.Conditions[0].Type != v1.PodReady || vp.pod.Status.Conditions[0].Status != v1.ConditionTrue {
		t.Fatalf("unexpected condition: %+v", vp.pod.Status.Conditions[0])
	}
	// Update existing condition
	vp.setCondition(v1.PodReady, v1.ConditionFalse)
	if len(vp.pod.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition after update, got %d", len(vp.pod.Status.Conditions))
	}
	if vp.pod.Status.Conditions[0].Status != v1.ConditionFalse {
		t.Fatalf("condition not updated")
	}
}

func TestHandleContainerStartSetsReadyAndRunning(t *testing.T) {
	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, nil, nil, "")
	// new state Running
	now := metav1.Now()
	vp.handleContainerStart(v1.ContainerState{Running: &v1.ContainerStateRunning{StartedAt: now}})
	cs := vp.pod.Status.ContainerStatuses[0]
	if !cs.Ready {
		t.Fatalf("container should be ready")
	}
	if cs.State.Running == nil {
		t.Fatalf("container should be running state")
	}
	if cs.State.Running.StartedAt.Time.IsZero() {
		t.Fatalf("startedAt should be set")
	}
	// Conditions set
	var ready, containersReady bool
	for _, c := range vp.pod.Status.Conditions {
		if c.Type == v1.PodReady && c.Status == v1.ConditionTrue {
			ready = true
		}
		if c.Type == v1.ContainersReady && c.Status == v1.ConditionTrue {
			containersReady = true
		}
	}
	if !ready || !containersReady {
		t.Fatalf("ready conditions not set correctly: %+v", vp.pod.Status.Conditions)
	}
}

func TestHandleContainerTerminationSetsFailedState(t *testing.T) {
	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, nil, nil, "")
	term := v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: 1, Reason: "Error"}}
	vp.handleContainerTermination(term)
	cs := vp.pod.Status.ContainerStatuses[0]
	if cs.Ready {
		t.Fatalf("container should not be ready")
	}
	if cs.State.Terminated == nil || cs.State.Terminated.ExitCode != 1 {
		t.Fatalf("terminated state not set correctly: %+v", cs.State.Terminated)
	}
	// Conditions false
	for _, c := range vp.pod.Status.Conditions {
		if c.Type == v1.PodReady && c.Status != v1.ConditionFalse {
			t.Fatalf("PodReady condition should be false")
		}
		if c.Type == v1.ContainersReady && c.Status != v1.ConditionFalse {
			t.Fatalf("ContainersReady condition should be false")
		}
	}
}

func TestPodShouldRestartPolicies(t *testing.T) {
	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, nil, nil, "")
	// Set terminated exit codes and test restart policy logic
	setExit := func(code int32) {
		vp.pod.Status.ContainerStatuses[0].State = v1.ContainerState{Terminated: &v1.ContainerStateTerminated{ExitCode: code}}
	}
	vp.pod.Spec.RestartPolicy = v1.RestartPolicyAlways
	setExit(0)
	if !vp.podShouldRestart() {
		t.Fatalf("Always should restart")
	}
	vp.pod.Spec.RestartPolicy = v1.RestartPolicyOnFailure
	setExit(0)
	if vp.podShouldRestart() {
		t.Fatalf("OnFailure should not restart on code 0")
	}
	setExit(1)
	if !vp.podShouldRestart() {
		t.Fatalf("OnFailure should restart on non-zero")
	}
	vp.pod.Spec.RestartPolicy = v1.RestartPolicyNever
	setExit(1)
	if vp.podShouldRestart() {
		t.Fatalf("Never should not restart")
	}
}

func TestHandleContainerRestartIncrementsCountAndBackoffFlag(t *testing.T) {
	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, nil, nil, "")
	vp.handleContainerRestart(false)
	if vp.pod.Status.ContainerStatuses[0].RestartCount != 1 {
		t.Fatalf("expected restartcount 1")
	}
	if vp.pod.Status.ContainerStatuses[0].State.Waiting == nil || vp.pod.Status.ContainerStatuses[0].State.Waiting.Reason == "CrashLoopBackOff" {
		t.Fatalf("no CrashLoopBackOff expected when backoff=false")
	}
	vp.handleContainerRestart(true)
	if vp.pod.Status.ContainerStatuses[0].RestartCount != 2 {
		t.Fatalf("expected restartcount 2")
	}
	if vp.pod.Status.ContainerStatuses[0].State.Waiting == nil || vp.pod.Status.ContainerStatuses[0].State.Waiting.Reason != "CrashLoopBackOff" {
		t.Fatalf("CrashLoopBackOff expected when backoff=true")
	}
}

func TestFailPodSetsTerminationWithReasonAndExitCode(t *testing.T) {
	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, nil, nil, "")
	err := errors.New("boom")
	vp.FailPod(err)
	cs := vp.pod.Status.ContainerStatuses[0]
	if cs.State.Terminated == nil {
		t.Fatalf("expected terminated state")
	}
	if cs.State.Terminated.ExitCode != 1 || cs.State.Terminated.Reason != "Failed" {
		t.Fatalf("expected exitCode 1 and reason 'Failed', got %+v", cs.State.Terminated)
	}
}

func TestImagePullAlways(t *testing.T) {
	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, nil, nil, "")
	if vp.ImagePullAlways() {
		t.Fatalf("expected false by default")
	}
	vp.pod.Spec.Containers[0].ImagePullPolicy = v1.PullAlways
	if !vp.ImagePullAlways() {
		t.Fatalf("expected true when PullAlways")
	}
}

func TestAuthHeadersUsesToken(t *testing.T) {
	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, nil, nil, "token123")
	vp.authToken = "token123"
	h := vp.authHeaders()
	if got := h.Get("Authorization"); got != "Bearer token123" {
		t.Fatalf("unexpected auth header: %q", got)
	}
}

// Basic smoke tests for runtime helpers that don't require network
// TODO: Better test for pushes
//func TestPushEnvVarsSkipsWhenEmptyAndErrorsOnNilClient(t *testing.T) {
//	vp := NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, map[string]map[string]string{}, nil, "")
//	// No mounts or configMaps => no-op
//	if err := vp.PushEnvVars(context.Background(), nil); err != nil {
//		// when nil maps, function returns early without checking client; here configMaps is empty so nil
//		// client should not be evaluated; err must be nil
//		t.Fatalf("expected nil error for no-op, got %v", err)
//	}
//	// When there are mounts/configMaps, it should check client exists
//	vp = NewVirtualPod("id1", newTestPod(), &Machine{}, &ProxyConfig{}, map[string]map[string]string{"cm": {}}, []FileMapping{{TargetPath: "/t", ConfigMapName: "cm", Key: "k"}}, "")
//	err := vp.PushEnvVars(context.Background(), nil)
//	if err == nil {
//		t.Fatalf("expected error on nil client when work to do")
//	}
//}

// Basic test for RunCommand path composition: we can't hit network but ensure no panic and correct URL format via stub client
