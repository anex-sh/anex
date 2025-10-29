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

	return machineList.Instances, nil
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
	// url := c.baseURL + "/search/asks/"
	url := c.baseURL + "/bundles/"
	var offers []virtualpod.Offer

	filters := buildInstanceFilters(spec)
	// _, bundleOffer, err := utils.MakeRequest[BundleOffers](ctx, c.retryClient, http.MethodPut, url, filters, c.authHeader)
	_, bundleOffer, err := utils.MakeRequest[BundleOffers](ctx, c.retryClient, http.MethodPost, url, filters, c.authHeader)
	if err != nil {
		return offers, err
	}
	candidates := bundleOffer.Offers
	candidatesSorted := sortCandidates(candidates)

	logger := log.G(ctx)
	logger.Infof("Found %d candidates for given pod (not considering price)", len(candidates))

	for _, candidate := range candidatesSorted {
		if candidate.DphTotal > spec.MaxPricePerHour {
			continue
		}

		offers = append(offers, virtualpod.Offer{
			OfferID:   strconv.Itoa(candidate.ID),
			MachineID: strconv.Itoa(candidate.MachineID),
		})
	}

	return offers, nil
}

func (c *Client) ProvisionMachine(ctx context.Context, candidatesID []string, pod *v1.Pod, authToken string, proxy, promtail bool) (machineID string, err error) {
	logger := log.G(ctx)

	if len(candidatesID) == 0 {
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

	for _, id := range candidatesID {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		logger.Infof("Attempting to provision instance with ID: %d", id)
		url := fmt.Sprintf("%s/asks/%s/", c.baseURL, id)

		// TODO: on bad request or auth error - fail pod immediately
		statusCode, response, err := utils.MakeRequest[provisionInstanceResponse](ctx, c.retryClient, http.MethodPut, url, payload, c.authHeader)
		if statusCode == 400 {
			logger.Warnf("Request to provision instance %d failed with status code 400; bad payload", id)
			return "", utils.ErrBadPayload
		}
		if statusCode == 401 {
			logger.Warnf("Request to provision instance %d failed with status code 401; unauthorized", id)
			return "", utils.ErrUnauthorized
		}
		if err != nil {
			logger.Warnf("Request to provision instance %d failed: %v", id, err)
			continue
		}
		if !response.Success {
			logger.Warnf("Failed to provision instance %d: API returned non-success status", id)
			continue
		}

		return strconv.Itoa(response.MachineID), nil
	}

	return "", fmt.Errorf("failed to provision instance")
}

type GenericApiResponse struct {
	Success bool   `json:"success"`
	Message string `json:"msg"`
}

func (c *Client) DestroyMachine(ctx context.Context, id string) error {
	// TODO: Make machine destroy graceful with SIGTERM and terminationGracePeriod
	logger := log.G(ctx)
	url := fmt.Sprintf("%s/instances/%s/", c.baseURL, id)

	_, response, err := utils.MakeRequest[GenericApiResponse](ctx, c.retryClient, http.MethodDelete, url, nil, c.authHeader)
	if err != nil {
		return err
	}

	if !response.Success {
		return fmt.Errorf("failed to destroy instance: %s", response.Message)
	}

	logger.Infof("Instance %s destroyed", id)
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

func (c *Client) PruneDanglingMachines(ctx context.Context, podUIDs []string) error {
	machines, err := c.listMachinesInternal(ctx)
	if err != nil {
		return err
	}

	for _, machine := range machines {
		label := parseMachineLabel(machine.Label)
		if label == nil {
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
			log.G(ctx).Infof("Deleting machine ID %d", machine.ID)
			err = c.DestroyMachine(ctx, strconv.Itoa(machine.ID))
			if err != nil {
				log.G(ctx).Errorf("Error deleting machine ID %d; %s", machine.ID, err)
			}
		}
	}

	return nil
}

func (c *Client) RestartMachine(ctx context.Context, id string, pullImage bool) error {
	log.G(ctx).Infof("Restarting machine ID %s", id)

	var url string
	if pullImage {
		url = fmt.Sprintf("%s/instances/recycle/%s", c.baseURL, id)
	} else {
		url = fmt.Sprintf("%s/instances/reboot/%s", c.baseURL, id)
	}

	_, _, err := utils.MakeRequest[GenericApiResponse](ctx, c.retryClient, http.MethodPut, url, nil, c.authHeader)
	if err != nil {
		log.G(ctx).Infof("Restart of machine ID %s failed", id)
		return err
	}

	return nil
}

func (c *Client) CopyFileToMachine(ctx context.Context, id string, src, dst string) error { return nil }
