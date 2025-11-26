package vastai

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/utils"
	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
	v1 "k8s.io/api/core/v1"
)

type Client struct {
	baseURL     string
	authHeader  http.Header
	clusterUID  string
	retryClient *retryablehttp.Client
	nodeName    string
}

func NewClient(baseURL string, apiKey string, clusterUID string, nodeName string) *Client {
	return &Client{
		baseURL: baseURL,
		authHeader: http.Header{
			"Authorization": []string{"Bearer " + apiKey},
		},
		clusterUID:  clusterUID,
		retryClient: utils.NewDefaultRetryClient(),
		nodeName:    nodeName,
	}
}

func (c *Client) buildMachineLabel(podUID interface{}) string {
	// Format: prefix:clusterID:vkIdentifier:namespace:podName
	prefix := "vk"
	return fmt.Sprintf("%s:%s:%s:%s", prefix, c.clusterUID, c.nodeName, podUID)
}

func (c *Client) listMachinesInternal(ctx context.Context) ([]*Machine, error) {
	url := c.baseURL + "/instances/"

	type MachineList struct {
		Instances []*Machine `json:"instances"`
	}

	_, machineList, err := utils.MakeRequest[MachineList](ctx, c.retryClient, http.MethodGet, url, nil, c.authHeader)
	if err != nil {
		return nil, err
	}

	// Filter machines that match clusterUID and node name. Drop the rest.
	var filteredMachines []*Machine
	for _, machine := range machineList.Instances {
		label := parseMachineLabel(machine.Label)
		if label == nil {
			continue
		}

		if label.ClusterUID == c.clusterUID && label.NodeName == c.nodeName {
			filteredMachines = append(filteredMachines, machine)
		}
	}

	return filteredMachines, nil
}

func (c *Client) ListMachines(ctx context.Context) ([]*virtualpod.Machine, error) {
	vastaiMachineList, err := c.listMachinesInternal(ctx)
	if err != nil {
		return nil, err
	}

	var machinesList []*virtualpod.Machine
	virtualNodeLabelPrefix := c.buildMachineLabel("")
	for _, vastaiMachine := range vastaiMachineList {
		if !strings.HasPrefix(vastaiMachine.Label, virtualNodeLabelPrefix) {
			continue
		}

		machine := GenericMachineAdapter(vastaiMachine)
		machinesList = append(machinesList, machine)
	}

	return machinesList, nil
}

func (c *Client) GetMachine(ctx context.Context, machineID string) (machine *virtualpod.Machine, err error) {
	url := fmt.Sprintf("%s/instances/%s/", c.baseURL, machineID)

	type PortInfo struct {
		HostIp   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}

	type MachineResponse struct {
		Instance struct {
			Machine
		} `json:"instances"`
	}

	_, response, err := utils.MakeRequest[MachineResponse](ctx, c.retryClient, http.MethodGet, url, nil, c.authHeader)
	if err != nil {
		return nil, err
	}

	machine = GenericMachineAdapter(&response.Instance.Machine)
	return machine, nil
}

func sortCandidates(candidates []BundleOffer) []BundleOffer {
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].DphTotal < candidates[j].DphTotal
	})

	return candidates
}

func (c *Client) GetRentalCandidates(ctx context.Context, spec virtualpod.MachineSpecification) ([]virtualpod.Offer, error) {
	logger := log.G(ctx)
	logger.Info("Fetching rental candidates from VastAI")

	// url := c.baseURL + "/search/asks/"
	url := c.baseURL + "/bundles/"
	var offers []virtualpod.Offer

	filters := buildInstanceFilters(spec)
	// _, bundleOffer, err := utils.MakeRequest[BundleOffers](ctx, c.retryClient, http.MethodPut, url, filters, c.authHeader)
	_, bundleOffer, err := utils.MakeRequest[BundleOffers](ctx, c.retryClient, http.MethodPost, url, filters, c.authHeader)
	if err != nil {
		logger.Errorf("Failed to fetch rental candidates: %v", err)
		return offers, err
	}
	candidates := bundleOffer.Offers
	candidatesSorted := sortCandidates(candidates)

	logger.Infof("Found %d candidates (before price filtering)", len(candidates))

	for _, candidate := range candidatesSorted {
		// Check price constraints
		// If exact price is specified, it's already filtered in buildInstanceFilters
		// Here we only need to check max price if specified
		if spec.PriceMax != nil && candidate.DphTotal > *spec.PriceMax {
			continue
		}

		offers = append(offers, virtualpod.Offer{
			OfferID:   strconv.Itoa(candidate.ID),
			MachineID: strconv.Itoa(candidate.MachineID),
		})
	}

	logger.Infof("Returning %d offers after filtering", len(offers))
	return offers, nil
}

func (c *Client) ProvisionMachine(ctx context.Context, candidatesID []string, pod *v1.Pod, authToken string, proxy, promtail bool) (machineID string, err error) {
	logger := log.G(ctx)
	logger.Infof("Attempting to provision machine from %d candidates", len(candidatesID))

	if len(candidatesID) == 0 {
		logger.Error("No instance candidates provided")
		return "", fmt.Errorf("no instance candidates provided")
	}

	var ports []int
	for _, c := range pod.Spec.Containers {
		for _, cp := range c.Ports {
			ports = append(ports, int(cp.ContainerPort))
		}
	}
	sort.Ints(ports)

	// TODO: Do not hardcode URLs
	agentURL := "https://glami-gpu-provider.glami-ml.com/container_agent_v0.1.4?token=cSrYDWSRTawnkIup"
	wireproxyURL := "https://glami-gpu-provider.glami-ml.com/wireproxy?token=cSrYDWSRTawnkIup"
	promtailURL := "https://glami-gpu-provider.glami-ml.com/promtail?token=cSrYDWSRTawnkIup"

	containerCommand := strings.Join(pod.Spec.Containers[0].Command, " ")
	commandWrapper := fmt.Sprintf("/container_agent run -p 25001 -c \"%s\" --auth-token \"%s\"", containerCommand, authToken)
	if proxy {
		commandWrapper += " --proxy"
	}

	if promtail {
		commandWrapper += " --promtail"
	}

	params := OnStartTemplateParams{
		Workdir:      pod.Spec.Containers[0].WorkingDir,
		Command:      commandWrapper,
		AgentURL:     agentURL,
		WireproxyURL: wireproxyURL,
		PromtailURL:  promtailURL,
		AuthToken:    authToken,
	}

	var diskSize int
	annotations := pod.GetAnnotations()
	if diskSizeStr, ok := annotations["glami.cz/disk-space-gb"]; ok {
		if parsedDiskSize, err := strconv.Atoi(diskSizeStr); err == nil {
			diskSize = parsedDiskSize
		}
	} else {
		diskSize = 30
	}

	image := pod.Spec.Containers[0].Image
	script := GenerateOnStartScript(params)

	// TODO: Ban reserved ports from pod spec
	payload := map[string]interface{}{
		"client_id": "me",
		"image":     image,
		"onstart":   script,
		"label":     c.buildMachineLabel(pod.UID),
		"disk":      strconv.Itoa(diskSize),
		"runtype":   "ssh",
		"env": map[string]string{
			"-p 25001:25001":     "1",
			"-p 72000:72000/udp": "1",
		},
	}

	// Check if the image is from AWS ECR registry; if so, build ECR login string
	if strings.Contains(image, ".dkr.ecr.") && strings.Contains(image, ".amazonaws.com") {
		payload["image_login"] = utils.GetAWSECRLogin(ctx, image)
	}

	type provisionInstanceResponse struct {
		Success   bool `json:"success"`
		MachineID int  `json:"new_contract"`
	}

	for idx, id := range candidatesID {
		if ctx.Err() != nil {
			logger.Info("Context cancelled, stopping provisioning attempts")
			return "", ctx.Err()
		}

		logger.Infof("Attempting to provision instance %d/%d with offer ID: %s", idx+1, len(candidatesID), id)
		url := fmt.Sprintf("%s/asks/%s/", c.baseURL, id)

		// TODO: on bad request or auth error - fail pod immediately
		statusCode, response, err := utils.MakeRequest[provisionInstanceResponse](ctx, c.retryClient, http.MethodPut, url, payload, c.authHeader)
		if statusCode == 400 {
			logger.Warnf("Provisioning failed for offer %s: bad request (status 400)", id)
			// return "", utils.ErrBadPayload
			continue
		}
		if statusCode == 401 {
			logger.Errorf("Provisioning failed for offer %s: unauthorized (status 401)", id)
			return "", utils.ErrUnauthorized
		}
		if err != nil {
			logger.Warnf("Provisioning failed for offer %s: %v", id, err)
			continue
		}
		if !response.Success {
			logger.Warnf("Provisioning failed for offer %s: API returned non-success status", id)
			continue
		}

		logger.Infof("Successfully provisioned machine with ID: %d", response.MachineID)
		return strconv.Itoa(response.MachineID), nil
	}

	logger.Error("Failed to provision instance from any of the provided candidates")
	return "", fmt.Errorf("failed to provision instance")
}

type GenericApiResponse struct {
	Success bool   `json:"success"`
	Message string `json:"msg"`
}

func (c *Client) DestroyMachine(ctx context.Context, id string) error {
	// TODO: Make machine destroy graceful with SIGTERM and terminationGracePeriod
	logger := log.G(ctx)
	logger.Infof("Destroying machine: %s", id)
	url := fmt.Sprintf("%s/instances/%s/", c.baseURL, id)

	_, response, err := utils.MakeRequest[GenericApiResponse](ctx, c.retryClient, http.MethodDelete, url, nil, c.authHeader)
	if err != nil {
		logger.Errorf("Failed to destroy machine %s: %v", id, err)
		return err
	}

	if !response.Success {
		logger.Errorf("Failed to destroy machine %s: %s", id, response.Message)
		return fmt.Errorf("failed to destroy instance: %s", response.Message)
	}

	logger.Infof("Successfully destroyed machine: %s", id)
	return nil
}

type labelInfo struct {
	Prefix     string // Basic identifier (e.g., "virtual-kubelet-container")
	ClusterUID string // Kubernetes cluster identifier
	NodeName   string // VK Node name
	PodUID     string // Kubernetes namespace
}

func parseMachineLabel(label string) *labelInfo {
	parts := strings.Split(label, ":")
	if len(parts) != 4 || parts[0] != "vk" {
		return nil
	}

	return &labelInfo{
		Prefix:     parts[0],
		ClusterUID: parts[1],
		NodeName:   parts[2],
		PodUID:     parts[3],
	}
}

func (c *Client) MapRunningMachines(ctx context.Context, pods *v1.PodList) (map[string]*virtualpod.Machine, error) {
	machines, err := c.listMachinesInternal(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]*virtualpod.Machine)
	for _, machine := range machines {
		label := parseMachineLabel(machine.Label)
		if label == nil {
			continue
		}

		for _, pod := range pods.Items {
			if string(pod.UID) == label.PodUID {
				result[label.PodUID] = GenericMachineAdapter(machine)
				break
			}
		}
	}

	return result, nil
}

func (c *Client) PruneDanglingMachines(ctx context.Context, podUIDs []string) error {
	logger := log.G(ctx)
	logger.Info("Starting dangling machines pruning")

	machines, err := c.listMachinesInternal(ctx)
	if err != nil {
		logger.Errorf("Failed to list machines: %v", err)
		return err
	}

	logger.Infof("Found %d machines to check", len(machines))

	for _, machine := range machines {
		label := parseMachineLabel(machine.Label)
		if label == nil {
			continue
		}

		if label.ClusterUID != c.clusterUID {
			continue
		}

		active := false
		for _, podUID := range podUIDs {
			if label.PodUID == podUID {
				active = true
				break
			}
		}

		if !active {
			logger.Infof("Deleting dangling machine ID %d (label: %s)", machine.ID, machine.Label)
			err = c.DestroyMachine(ctx, strconv.Itoa(machine.ID))
			if err != nil {
				logger.Errorf("Error deleting dangling machine ID %d: %v", machine.ID, err)
			}
		}
	}

	logger.Info("Dangling machines pruning completed")
	return nil
}

func (c *Client) RestartMachine(ctx context.Context, id string, pullImage bool) error {
	logger := log.G(ctx)

	restartType := "reboot"
	if pullImage {
		restartType = "recycle"
		logger.Infof("Restarting machine %s with image pull (recycle)", id)
	} else {
		logger.Infof("Restarting machine %s without image pull (reboot)", id)
	}

	var url string
	if pullImage {
		url = fmt.Sprintf("%s/instances/recycle/%s", c.baseURL, id)
	} else {
		url = fmt.Sprintf("%s/instances/reboot/%s", c.baseURL, id)
	}

	_, _, err := utils.MakeRequest[GenericApiResponse](ctx, c.retryClient, http.MethodPut, url, nil, c.authHeader)
	if err != nil {
		logger.Errorf("Failed to restart machine %s (%s): %v", id, restartType, err)
		return err
	}

	logger.Infof("Successfully restarted machine %s (%s)", id, restartType)
	return nil
}

func (c *Client) CopyFileToMachine(ctx context.Context, id string, src, dst string) error { return nil }
