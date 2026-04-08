package virtualpod

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/utils"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ProxyServerConfig struct {
	Endpoint  string `yaml:"endpoint"`
	PublicKey string `yaml:"public_key"`
	PortUDP   int    `yaml:"port_udp"`
	PortTCP   int    `yaml:"port_tcp"`
	DNS       string `yaml:"dns,omitempty"`
}

type ProxyClientConfig struct {
	Address           string `yaml:"address"`
	PrivateKey        string `yaml:"private_key"`
	PublicKey         string `yaml:"public_key"`
	GatewayPortOffset int    `yaml:"gateway_port_offset"`
	Assigned          bool
}

type PodProxyConfig struct {
	Enabled bool
	Server  ProxyServerConfig `yaml:"server"`
	Client  ProxyClientConfig `yaml:"client"`
}

type ProxyTunnels struct {
	Endpoints []struct {
		Address       string `yaml:"address"`
		ContainerPort int    `yaml:"containerPort"`
	} `yaml:"endpoints"`
}

type FileMapping struct {
	TargetPath    string
	ConfigMapName string
	Key           string
}

type VirtualPod struct {
	mutex                   sync.RWMutex
	SyncUpdateDelete        sync.Mutex
	name                    string
	namespace               string
	id                      string
	pod                     *v1.Pod
	machine                 *Machine
	finalized               bool
	gatewaySlotIndex        int
	agentPort               int
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

func NewVirtualPod(id string, pod *v1.Pod, machine *Machine, gatewaySlot int, configMaps map[string]map[string]string, volumeMounts []FileMapping, authToken string) *VirtualPod {
	return &VirtualPod{
		name:             pod.Name,
		namespace:        pod.Namespace,
		id:               id,
		pod:              pod,
		machine:          machine,
		gatewaySlotIndex: gatewaySlot,
		authHeader:       http.Header{"Authorization": []string{"Bearer " + authToken}},
		configMaps:       configMaps,
		volumeMounts:     volumeMounts,
	}
}

func (vp *VirtualPod) ID() string {
	return vp.id
}

func (vp *VirtualPod) GetAgentAddress() string {
	slot := vp.gatewaySlotIndex + 10 + 1

	return fmt.Sprintf("http://10.254.254.%d:%d", slot, vp.agentPort)
}

func (vp *VirtualPod) MachineRentID() string {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	return vp.machine.ID
}

func (vp *VirtualPod) MachineStableID() string {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	return vp.machine.MachineID
}

func (vp *VirtualPod) SetMachine(machine *Machine) {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	vp.machine = machine
	vp.pod.ObjectMeta.Annotations["anex.sh/machine-rent-id"] = machine.ID
	vp.pod.ObjectMeta.Annotations["anex.sh/machine-stable-id"] = machine.MachineID
}

func (vp *VirtualPod) SetAgentPort(port int) {
	vp.mutex.Lock()
	defer vp.mutex.Unlock()
	vp.agentPort = port
}

func (vp *VirtualPod) RemoveMachine() {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	vp.machine = &Machine{}
}

func (vp *VirtualPod) Finalize() {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	vp.finalized = true
}

func (vp *VirtualPod) Finalized() bool {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	return vp.finalized
}

func (vp *VirtualPod) Pod() *v1.Pod {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()
	return vp.pod.DeepCopy()
}

func (vp *VirtualPod) PodName() string {
	return vp.pod.Name
}

func (vp *VirtualPod) PodNamespace() string {
	return vp.pod.Namespace
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

func (vp *VirtualPod) SetProvisioningCompleted(completed bool) {
	vp.mutex.Lock()
	defer vp.mutex.Unlock()
	vp.provisioningCompleted = completed
	vp.readySince = time.Now()
}

func (vp *VirtualPod) UpdatePod(pod *v1.Pod) {
	vp.mutex.Lock()
	defer vp.mutex.Unlock()
	vp.pod = pod
}

func (vp *VirtualPod) ProxySlot() int {
	return vp.gatewaySlotIndex
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

func readContainerState(ctx context.Context, cs *v1.ContainerState) (x ContainerState) {
	switch {
	case cs.Waiting != nil:
		return ContainerStateWaiting
	case cs.Running != nil:
		return ContainerStateRunning
	case cs.Terminated != nil:
		return ContainerStateTerminated
	default:
		// Never happens; ContainerState is a union type
		logger := log.G(ctx)
		logger.Error("Unable to determine container state; ContainerState empty. Invalid!!!")
		return ContainerStateWaiting
	}
}

type StatusUpdate struct {
	Changed    bool
	Terminated bool
	Succeeded  bool
	Restarts   bool
	Backoff    time.Duration
}

func (vp *VirtualPod) PodStatusUpdate(ctx context.Context, httpClient *retryablehttp.Client) (update StatusUpdate, err error) {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()

	url := vp.GetAgentAddress() + "/status"
	headers := vp.authHeaders()
	_, newStateRaw, err := utils.MakeRequest[v1.ContainerState](ctx, httpClient, http.MethodGet, url, nil, headers)
	if err != nil {
		return StatusUpdate{}, err
	}

	lastState := readContainerState(ctx, &vp.pod.Status.ContainerStatuses[0].State)
	newState := readContainerState(ctx, &newStateRaw)

	if lastState == newState {
		return StatusUpdate{
			Changed: false,
		}, nil
	}

	if newState == ContainerStateRunning {
		vp.handleContainerStart(newStateRaw)
		vp.pod.Status.Phase = v1.PodRunning
		return StatusUpdate{
			Changed: true,
		}, nil
	}

	// Container terminated before we pulled a running state
	if lastState == ContainerStateWaiting && newState == ContainerStateTerminated {
		vp.handleContainerStart(newStateRaw)
		vp.pod.Status.Phase = v1.PodRunning
	}

	if newState == ContainerStateTerminated {
		vp.handleContainerTermination(newStateRaw)
		restarts := vp.podShouldRestart()
		containerSucceeded := vp.pod.Status.ContainerStatuses[0].State.Terminated.ExitCode == 0

		if restarts {
			var backoff time.Duration
			exitCode := vp.pod.Status.ContainerStatuses[0].State.Terminated.ExitCode

			if exitCode == 0 || time.Since(vp.readySince) > 10*time.Minute {
				vp.effectiveRestartCounter = 0
			} else {
				if vp.effectiveRestartCounter > 0 {
					backoff = time.Duration(10*math.Pow(2, float64(vp.effectiveRestartCounter-1))) * time.Second
				}
				vp.effectiveRestartCounter += 1
			}

			vp.provisioningCompleted = false
			vp.handleContainerRestart(backoff > 0)

			return StatusUpdate{
				Changed:    true,
				Terminated: false,
				Succeeded:  containerSucceeded,
				Restarts:   true,
				Backoff:    backoff,
			}, nil
		}

		if containerSucceeded {
			vp.pod.Status.Phase = v1.PodSucceeded
		} else {
			vp.pod.Status.Phase = v1.PodFailed
		}

		return StatusUpdate{
			Changed:    true,
			Terminated: true,
			Succeeded:  containerSucceeded,
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
	vp.pod.Status.ContainerStatuses[0].State = v1.ContainerState{
		Terminated: &v1.ContainerStateTerminated{
			ExitCode:   newContainerState.Terminated.ExitCode,
			Reason:     newContainerState.Terminated.Reason,
			StartedAt:  newContainerState.Terminated.StartedAt,
			FinishedAt: newContainerState.Terminated.FinishedAt,
		},
	}

	vp.pod.Status.ContainerStatuses[0].LastTerminationState = vp.pod.Status.ContainerStatuses[0].State

	vp.setCondition(v1.ContainersReady, v1.ConditionFalse)
	vp.setCondition(v1.PodReady, v1.ConditionFalse)
}

func (vp *VirtualPod) handleContainerRestart(backoff bool) {
	vp.pod.Status.ContainerStatuses[0].RestartCount += 1

	vp.pod.Status.ContainerStatuses[0].State = v1.ContainerState{
		Waiting: &v1.ContainerStateWaiting{
			Reason:  "ContainerCreating",
			Message: "container restarts",
		},
	}
	if backoff {
		vp.pod.Status.ContainerStatuses[0].State.Waiting.Reason = "CrashLoopBackOff"
	}
}

func (vp *VirtualPod) CrashLoopBackOffDone() {
	vp.mutex.RLock()
	defer vp.mutex.RUnlock()

	if vp.pod.Status.ContainerStatuses[0].State.Waiting == nil {
		return
	}
	vp.pod.Status.ContainerStatuses[0].State.Waiting.Reason = "ContainerCreating"
}

func (vp *VirtualPod) FailContainer(message string) {
	vp.mutex.Lock()
	defer vp.mutex.Unlock()

	// TODO: Message not written
	failContainerState := v1.ContainerState{
		Terminated: &v1.ContainerStateTerminated{
			ExitCode:   1,
			Reason:     "Failed",
			Message:    message,
			StartedAt:  metav1.Now(),
			FinishedAt: metav1.Now(),
		},
	}

	vp.handleContainerTermination(failContainerState)
}

// TerminatePod TODO: Make right with SIGTERM
func (vp *VirtualPod) TerminatePod(exitCode int32) {
	vp.mutex.Lock()
	defer vp.mutex.Unlock()

	if exitCode == 0 {
		vp.pod.Status.Phase = v1.PodSucceeded
	} else {
		vp.pod.Status.Phase = v1.PodFailed
	}

	deleteContainerState := v1.ContainerState{
		Terminated: &v1.ContainerStateTerminated{
			ExitCode:   exitCode,
			Reason:     "Pod Deleted",
			FinishedAt: metav1.Now(),
		},
	}

	if vp.pod.Status.ContainerStatuses[0].State.Running != nil {
		deleteContainerState.Terminated.StartedAt = vp.pod.Status.ContainerStatuses[0].State.Running.StartedAt
	}

	vp.handleContainerTermination(deleteContainerState)
}
