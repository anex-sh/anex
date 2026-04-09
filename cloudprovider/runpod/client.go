package runpod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anex-sh/anex/internal/utils"
	"github.com/anex-sh/anex/virtualpod"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

const baseURL = "https://rest.runpod.io/v1"

// URLConfig holds CDN URLs for binaries and init script.
type URLConfig struct {
	InitURL      string
	AgentURL     string
	WireproxyURL string
	WstunnelURL  string
	PromtailURL  string
}

type podResponse struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	DesiredStatus string  `json:"desiredStatus"`
	PublicIP      string  `json:"publicIp"`
	MachineID     string  `json:"machineId"`
	CostPerHr     float64 `json:"costPerHr"`
	GPU           struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		Count       int    `json:"count"`
	} `json:"gpu"`
	VcpuCount  float64 `json:"vcpuCount"`
	MemoryInGb float64 `json:"memoryInGb"`
}

// Client implements cloudprovider.Client for RunPod.
type Client struct {
	clusterUID     string
	nodeName       string
	retryClient    *retryablehttp.Client
	authHeader     http.Header
	urls           URLConfig
	agentEndpoints sync.Map // machineID → agent endpoint URL
}

func NewClient(apiKey, clusterUID, nodeName string, urls URLConfig) *Client {
	return &Client{
		clusterUID: clusterUID,
		nodeName:   nodeName,
		authHeader: http.Header{
			"Authorization": []string{"Bearer " + apiKey},
		},
		retryClient: utils.NewDefaultRetryClient(),
		urls:        urls,
	}
}

var errNotImplemented = fmt.Errorf("runpod: not implemented")

func (c *Client) buildPodName(podUID interface{}) string {
	return fmt.Sprintf("vk:%s:%s:%s", c.clusterUID, c.nodeName, podUID)
}

type podNameInfo struct {
	Prefix     string
	ClusterUID string
	NodeName   string
	PodUID     string
}

func parsePodName(name string) *podNameInfo {
	parts := strings.Split(name, ":")
	if len(parts) != 4 || parts[0] != "vk" {
		return nil
	}
	return &podNameInfo{
		Prefix:     parts[0],
		ClusterUID: parts[1],
		NodeName:   parts[2],
		PodUID:     parts[3],
	}
}

func (c *Client) SupportsMachineBans() bool { return false }
func (c *Client) BanMachine(_ string)       {}

func (c *Client) SelectAndProvisionMachine(ctx context.Context, spec virtualpod.MachineSpecification, pod *v1.Pod, proxy virtualpod.PodProxyConfig, promtail bool, recorder record.EventRecorder) (string, error) {
	logger := log.G(ctx)

	query, warnings := BuildProvisionQuery(spec)
	for _, w := range warnings {
		logger.Warnf("RunPod filter: %s", w)
	}

	// Pod identity
	query["name"] = c.buildPodName(pod.UID)
	query["imageName"] = pod.Spec.Containers[0].Image

	// Disk size from annotation
	diskSize := 30
	if diskStr, ok := pod.GetAnnotations()["anex.sh/disk-space-gb"]; ok {
		if parsed, err := strconv.Atoi(diskStr); err == nil {
			diskSize = parsed
		}
	}
	query["containerDiskInGb"] = diskSize

	// Override entrypoint to download and run init script
	query["dockerEntrypoint"] = []string{"/bin/bash", "-c"}
	query["dockerStartCmd"] = []string{`apt-get update && apt-get install -y curl && curl -fsSL "$GPU_PROVIDER_INIT_URL" -o /tmp/init.sh && bash /tmp/init.sh`}

	// Build environment variables
	provisionEnv := BuildProvisionEnv(pod, proxy, promtail, c.urls)
	query["env"] = provisionEnv.ToEnvMap()

	// ECR login
	image := pod.Spec.Containers[0].Image
	if strings.Contains(image, ".dkr.ecr.") && strings.Contains(image, ".amazonaws.com") {
		query["containerRegistryAuthId"] = utils.GetAWSECRLogin(ctx, image)
	}

	payload, _ := json.MarshalIndent(query, "", "  ")
	logger.Infof("RunPod provisioning payload:\n%s", string(payload))

	recorder.Eventf(pod, v1.EventTypeNormal, "ProvisioningMachine", "Creating RunPod pod")

	type createPodResponse struct {
		ID        string  `json:"id"`
		CostPerHr float64 `json:"costPerHr"`
	}

	url := baseURL + "/pods"
	statusCode, response, err := utils.MakeRequest[createPodResponse](ctx, c.retryClient, http.MethodPost, url, query, c.authHeader)
	if statusCode == 401 {
		logger.Error("RunPod provisioning failed: unauthorized")
		return "", utils.ErrUnauthorized
	}
	if err != nil {
		logger.Errorf("RunPod provisioning failed: %v", err)
		recorder.Eventf(pod, v1.EventTypeWarning, "ProvisioningFailed", "Failed to create RunPod pod: %v", err)
		return "", err
	}
	if response.ID == "" {
		return "", fmt.Errorf("RunPod returned empty pod ID")
	}

	logger.Infof("Successfully provisioned RunPod pod ID=%s (cost: $%.4f/hr)", response.ID, response.CostPerHr)
	recorder.Eventf(pod, v1.EventTypeNormal, "MachineProvisioned", "RunPod pod created: %s", response.ID)
	return response.ID, nil
}

// RegisterAgentEndpoint stores the wireguard tunnel agent endpoint for a machine.
func (c *Client) RegisterAgentEndpoint(machineID, endpoint string) {
	c.agentEndpoints.Store(machineID, endpoint)
}

func (c *Client) checkAgentHealth(endpoint string) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) podToMachine(pod *podResponse, skipHealthCheck bool) *virtualpod.Machine {
	machine := &virtualpod.Machine{
		ID:       pod.ID,
		PublicIP: pod.PublicIP,
		States: virtualpod.States{
			GpuName:    pod.GPU.DisplayName,
			CpuCores:   pod.VcpuCount,
			CpuRam:     pod.MemoryInGb * 1024,
			PricePerHr: pod.CostPerHr,
		},
	}
	if pod.MachineID != "" {
		machine.MachineID = pod.MachineID
	}

	switch pod.DesiredStatus {
	case "EXITED":
		machine.State = virtualpod.MachineStateFailed
	case "RUNNING":
		if skipHealthCheck {
			machine.State = virtualpod.MachineStateRunning
		} else {
			machine.State = virtualpod.MachineStatePending
			if ep, ok := c.agentEndpoints.Load(pod.ID); ok {
				if c.checkAgentHealth(ep.(string)) {
					machine.State = virtualpod.MachineStateRunning
				}
			}
		}
	default:
		machine.State = virtualpod.MachineStatePending
	}

	return machine
}

func (c *Client) GetMachine(ctx context.Context, machineID string) (*virtualpod.Machine, error) {
	url := fmt.Sprintf("%s/pods/%s", baseURL, machineID)
	_, pod, err := utils.MakeRequest[podResponse](ctx, c.retryClient, http.MethodGet, url, nil, c.authHeader)
	if err != nil {
		return nil, err
	}
	if pod.ID == "" {
		return nil, fmt.Errorf("machine ID=%s not found", machineID)
	}
	return c.podToMachine(&pod, false), nil
}

func (c *Client) listPodsInternal(ctx context.Context) ([]podResponse, error) {
	url := baseURL + "/pods"
	_, pods, err := utils.MakeRequest[[]podResponse](ctx, c.retryClient, http.MethodGet, url, nil, c.authHeader)
	if err != nil {
		return nil, err
	}

	namePrefix := c.buildPodName("")
	var filtered []podResponse
	for _, p := range pods {
		if strings.HasPrefix(p.Name, namePrefix) {
			filtered = append(filtered, p)
		}
	}
	return filtered, nil
}

func (c *Client) ListMachines(ctx context.Context) ([]*virtualpod.Machine, error) {
	pods, err := c.listPodsInternal(ctx)
	if err != nil {
		return nil, err
	}

	var machines []*virtualpod.Machine
	for i := range pods {
		machines = append(machines, c.podToMachine(&pods[i], false))
	}
	return machines, nil
}

func (c *Client) MapRunningMachines(ctx context.Context, pods *v1.PodList) (map[string]*virtualpod.Machine, error) {
	rpPods, err := c.listPodsInternal(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]*virtualpod.Machine)
	for i := range rpPods {
		info := parsePodName(rpPods[i].Name)
		if info == nil {
			continue
		}
		for _, pod := range pods.Items {
			if string(pod.UID) == info.PodUID {
				result[info.PodUID] = c.podToMachine(&rpPods[i], true)
				break
			}
		}
	}
	return result, nil
}

func (c *Client) PruneDanglingMachines(ctx context.Context, podUIDs []string) error {
	logger := log.G(ctx)
	logger.Info("Starting dangling RunPod pods pruning")

	rpPods, err := c.listPodsInternal(ctx)
	if err != nil {
		return err
	}

	uidSet := make(map[string]bool, len(podUIDs))
	for _, uid := range podUIDs {
		uidSet[uid] = true
	}

	for _, rp := range rpPods {
		info := parsePodName(rp.Name)
		if info == nil {
			continue
		}
		if !uidSet[info.PodUID] {
			logger.Infof("Deleting dangling RunPod pod %s (name: %s)", rp.ID, rp.Name)
			if err := c.DestroyMachine(ctx, rp.ID); err != nil {
				logger.Errorf("Failed to delete dangling RunPod pod %s: %v", rp.ID, err)
			}
		}
	}

	logger.Info("Dangling RunPod pods pruning completed")
	return nil
}

func (c *Client) DestroyMachine(ctx context.Context, id string) error {
	logger := log.G(ctx)
	logger.Infof("Destroying RunPod pod: %s", id)

	url := fmt.Sprintf("%s/pods/%s", baseURL, id)
	_, _, err := utils.MakeRequest[struct{}](ctx, c.retryClient, http.MethodDelete, url, nil, c.authHeader)
	if err != nil {
		logger.Errorf("Failed to destroy RunPod pod %s: %v", id, err)
		return err
	}

	logger.Infof("Successfully destroyed RunPod pod: %s", id)
	return nil
}

func (c *Client) RestartMachine(ctx context.Context, id string, _ bool) error {
	logger := log.G(ctx)
	logger.Infof("Resetting RunPod pod: %s", id)

	url := fmt.Sprintf("%s/pods/%s/reset", baseURL, id)
	_, _, err := utils.MakeRequest[struct{}](ctx, c.retryClient, http.MethodPost, url, nil, c.authHeader)
	if err != nil {
		logger.Errorf("Failed to reset RunPod pod %s: %v", id, err)
		return err
	}

	logger.Infof("Successfully reset RunPod pod: %s", id)
	return nil
}

func (c *Client) RenewMachineKeys(_ context.Context, _ string, _ virtualpod.PodProxyConfig) error {
	return errNotImplemented
}

func (c *Client) CopyFileToMachine(_ context.Context, _ string, _, _ string) error {
	return errNotImplemented
}
