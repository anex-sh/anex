package virtualpod

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/utils"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FileMapping struct {
	TargetPath    string
	ConfigMapName string
	Key           string
}

type VirtualPod struct {
	mutex                   sync.RWMutex
	id                      string
	pod                     *v1.Pod
	machine                 *Machine
	proxyConfig             ProxyConfig
	provisioningCompleted   bool
	readySince              time.Time
	effectiveRestartCounter uint
	ProvisionCancel         context.CancelFunc
	LifecycleCancel         context.CancelFunc
	authToken               string
	authHeader              http.Header
	configMaps              map[string]map[string]string
	volumeMounts            []FileMapping
}

func NewVirtualPod(id string, pod *v1.Pod, machine *Machine, proxyConfig ProxyConfig, configMaps map[string]map[string]string, volumeMounts []FileMapping, authToken string) *VirtualPod {
	return &VirtualPod{
		id:           id,
		pod:          pod,
		machine:      machine,
		proxyConfig:  proxyConfig,
		authHeader:   http.Header{"Authorization": []string{"Bearer " + authToken}},
		configMaps:   configMaps,
		volumeMounts: volumeMounts,
	}
}

func (vp *VirtualPod) ID() string {
	return vp.id
}

func (vp *VirtualPod) MachineID() string {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	return vp.machine.ID
}

func (vp *VirtualPod) SetMachine(machine *Machine) {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	vp.machine = machine
}

func (vp *VirtualPod) RemoveMachine() {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	vp.machine = &Machine{}
}

func (vp *VirtualPod) Pod() *v1.Pod {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	return vp.pod.DeepCopy()
}

func (vp *VirtualPod) PodStatus() *v1.PodStatus {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	return vp.pod.Status.DeepCopy()
}

func (vp *VirtualPod) AuthToken() string {
	return vp.authToken
}

func (vp *VirtualPod) authHeaders() http.Header {
	return http.Header{"Authorization": []string{"Bearer " + vp.authToken}}
}

func (vp *VirtualPod) ProvisioningCompleted() bool {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	return vp.provisioningCompleted
}

func (vp *VirtualPod) SetProvisioningCompleted() {
	vp.mutex.Lock()
	defer vp.mutex.Unlock()
	vp.provisioningCompleted = true
	vp.readySince = time.Now()
}

func (vp *VirtualPod) ImagePullAlways() bool {
	return vp.pod.Spec.Containers[0].ImagePullPolicy == v1.PullAlways
}

type ContainerState string

type Transition struct{ from, to ContainerState }

const (
	ContainerStateWaiting    ContainerState = "Waiting"
	ContainerStateRunning    ContainerState = "Running"
	ContainerStateTerminated ContainerState = "Terminated"
)

func readContainerState(cs *v1.ContainerState) (x ContainerState) {
	switch {
	case cs.Waiting != nil:
		return ContainerStateWaiting
	case cs.Running != nil:
		return ContainerStateRunning
	case cs.Terminated != nil:
		return ContainerStateTerminated
	default:
		// Never happens; ContainerState is a union type
		return ContainerStateTerminated
	}
}

type StatusUpdate struct {
	Changed    bool
	Terminated bool
	Restarts   bool
	Backoff    time.Duration
}

func (vp *VirtualPod) PodStatusUpdate(ctx context.Context, httpClient *retryablehttp.Client) (update StatusUpdate, err error) {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()

	url := vp.machine.GetAgentAddress() + "/status"
	headers := vp.authHeaders()
	_, newStateRaw, err := utils.MakeRequest[v1.ContainerState](ctx, httpClient, http.MethodGet, url, nil, headers)
	if err != nil {
		return StatusUpdate{}, err
	}

	lastState := readContainerState(&vp.pod.Status.ContainerStatuses[0].State)
	newState := readContainerState(&newStateRaw)

	if lastState == newState {
		return StatusUpdate{
			Changed: false,
		}, nil
	}

	if newState == ContainerStateRunning {
		vp.handleContainerStart(newStateRaw)
		return StatusUpdate{
			Changed: true,
		}, nil
	}

	// Container terminated before we pulled a running state
	if lastState == ContainerStateWaiting && newState == ContainerStateTerminated {
		vp.handleContainerStart(newStateRaw)
	}

	if newState == ContainerStateTerminated {
		vp.handleContainerTermination(newStateRaw)
		restarts := vp.podShouldRestart()

		if restarts {
			var backoff time.Duration
			exitCode := vp.pod.Status.ContainerStatuses[0].State.Terminated.ExitCode

			if exitCode == 0 || time.Since(vp.readySince) > 10*time.Minute {
				vp.effectiveRestartCounter = 0
			} else {
				backoff = time.Duration(10 * math.Pow(2, float64(vp.effectiveRestartCounter)))
				vp.effectiveRestartCounter += 1
			}

			vp.provisioningCompleted = false
			vp.handleContainerRestart(backoff > 0)

			return StatusUpdate{
				Changed:  true,
				Restarts: true,
				Backoff:  backoff,
			}, nil
		}

		if vp.pod.Status.ContainerStatuses[0].State.Terminated.ExitCode == 0 {
			vp.pod.Status.Phase = v1.PodSucceeded
		} else {
			vp.pod.Status.Phase = v1.PodFailed
		}

		return StatusUpdate{
			Changed:    true,
			Terminated: true,
			Restarts:   false,
		}, nil
	}

	return StatusUpdate{}, fmt.Errorf("container state transition not found")
}

func (vp *VirtualPod) setCondition(conditionType v1.PodConditionType, status v1.ConditionStatus) {
	newCondition := v1.PodCondition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
	}

	conditions := &vp.pod.Status.Conditions
	for i, c := range *conditions {
		if c.Type == conditionType {
			(*conditions)[i] = newCondition
			return
		}
	}
	*conditions = append(*conditions, newCondition)
}

func (vp *VirtualPod) podShouldRestart() (restart bool) {
	cs := vp.pod.Status.ContainerStatuses[0].State
	if cs.Terminated == nil {
		return false
	}

	switch vp.pod.Spec.RestartPolicy {
	case v1.RestartPolicyAlways:
		return true
	case v1.RestartPolicyOnFailure:
		return cs.Terminated.ExitCode != 0
	case v1.RestartPolicyNever:
		return false
	default:
		return false
	}
}

func (vp *VirtualPod) handleContainerStart(newContainerState v1.ContainerState) {
	vp.pod.Status.ContainerStatuses[0].Ready = true

	// In case newContainerState already reads Terminated we set the startedAt to the current time
	startedAt := metav1.Now()
	if newContainerState.Running != nil {
		startedAt = newContainerState.Running.StartedAt
	}
	vp.pod.Status.ContainerStatuses[0].State = v1.ContainerState{
		Running: &v1.ContainerStateRunning{
			StartedAt: startedAt,
		},
	}

	vp.setCondition(v1.ContainersReady, v1.ConditionTrue)
	vp.setCondition(v1.PodReady, v1.ConditionTrue)
}

func (vp *VirtualPod) handleContainerTermination(newContainerState v1.ContainerState) {
	vp.pod.Status.ContainerStatuses[0].Ready = false
	vp.pod.Status.ContainerStatuses[0].LastTerminationState = vp.pod.Status.ContainerStatuses[0].State

	vp.pod.Status.ContainerStatuses[0].State = v1.ContainerState{
		Terminated: &v1.ContainerStateTerminated{
			ExitCode:   newContainerState.Terminated.ExitCode,
			Reason:     newContainerState.Terminated.Reason,
			StartedAt:  newContainerState.Terminated.StartedAt,
			FinishedAt: newContainerState.Terminated.FinishedAt,
		},
	}

	vp.setCondition(v1.ContainersReady, v1.ConditionFalse)
	vp.setCondition(v1.PodReady, v1.ConditionFalse)
}

func (vp *VirtualPod) handleContainerRestart(backoff bool) {
	vp.pod.Status.ContainerStatuses[0].RestartCount += 1

	vp.pod.Status.ContainerStatuses[0].State = v1.ContainerState{
		Waiting: &v1.ContainerStateWaiting{
			Message: "Container restarting",
		},
	}
	if backoff {
		vp.pod.Status.ContainerStatuses[0].State.Waiting.Reason = "CrashLoopBackOff"
	}
}

func (vp *VirtualPod) FailPod(err error) {
	vp.mutex.Lock()
	defer vp.mutex.Unlock()

	failContainerState := v1.ContainerState{
		Terminated: &v1.ContainerStateTerminated{
			ExitCode:   1,
			Reason:     "Failed",
			Message:    err.Error(),
			StartedAt:  metav1.Now(),
			FinishedAt: metav1.Now(),
		},
	}

	vp.handleContainerTermination(failContainerState)
}
