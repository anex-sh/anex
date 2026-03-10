package mock

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/agent"
	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

type Client struct {
	clusterUID        string
	nodeName          string
	BasePortWireproxy int
	BasePortAgent     int
	machineCounter    int
	machines          map[string]*virtualpod.Machine
	mu                sync.RWMutex
}

func NewClient(clusterUID string, nodeName string) *Client {
	os.RemoveAll("/tmp/gpu-provider")

	return &Client{
		clusterUID:        clusterUID,
		nodeName:          nodeName,
		BasePortWireproxy: 51900,
		BasePortAgent:     31000,
		machineCounter:    0,
		machines:          make(map[string]*virtualpod.Machine),
	}
}

func (c *Client) SupportsMachineBans() bool { return false }
func (c *Client) BanMachine(_ string)       {}

func (c *Client) SelectAndProvisionMachine(ctx context.Context, spec virtualpod.MachineSpecification, pod *v1.Pod, proxy virtualpod.PodProxyConfig, promtail bool, recorder record.EventRecorder) (string, error) {
	offers, err := c.GetRentalCandidates(ctx, spec)
	if err != nil {
		return "", err
	}
	var candidateIDs []string
	for _, o := range offers {
		candidateIDs = append(candidateIDs, o.OfferID)
	}
	return c.ProvisionMachine(ctx, candidateIDs, pod, proxy, promtail)
}

func (c *Client) ListMachines(ctx context.Context) ([]*virtualpod.Machine, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var machinesList []*virtualpod.Machine
	for _, machine := range c.machines {
		machinesList = append(machinesList, machine)
	}

	return machinesList, nil
}

func (c *Client) GetMachine(ctx context.Context, machineID string) (machine *virtualpod.Machine, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if m, exists := c.machines[machineID]; exists {
		return m, nil
	}

	return nil, fmt.Errorf("machine %s not found", machineID)
}

func (c *Client) GetRentalCandidates(ctx context.Context, spec virtualpod.MachineSpecification) ([]virtualpod.Offer, error) {
	logger := log.G(ctx)
	logger.Info("Mock: Fetching rental candidates")

	nextID := strconv.Itoa(c.machineCounter + 1)

	var offers []virtualpod.Offer
	offers = append(offers, virtualpod.Offer{
		OfferID:   nextID,
		MachineID: nextID,
	})

	return offers, nil
}

const wireproxyConfigTemplate = `
[Interface]
Address     = {{ .ProxyConfig.Client.Address }}
PrivateKey  = {{ .ProxyConfig.Client.PrivateKey }}
ListenPort  = {{ .WireproxyPort }}
DNS         = 10.254.254.1

[Peer]
PublicKey           = {{ .ProxyConfig.Server.PublicKey }}
Endpoint            = {{ .ProxyConfig.Server.Endpoint }}
AllowedIPs          = 0.0.0.0/0
PersistentKeepalive = 25

[TCPServerTunnel]
ListenPort = {{ .AgentPublicPort }}
Target = 127.0.0.1:{{ .AgentLocalPort }}
`

func (c *Client) generateWireproxyConfig(ctx context.Context, proxy virtualpod.PodProxyConfig, wireproxyPort, agentPublicPort, agentLocalPort string) (string, error) {
	type OnStartTemplateParams struct {
		ProxyConfig     virtualpod.PodProxyConfig
		ListenPort      int
		WireproxyPort   string
		AgentPublicPort string
		AgentLocalPort  string
	}

	params := OnStartTemplateParams{
		ProxyConfig:     proxy,
		WireproxyPort:   wireproxyPort,
		AgentPublicPort: agentPublicPort,
		AgentLocalPort:  agentLocalPort,
	}

	t := template.Must(template.New("wireproxy").Parse(wireproxyConfigTemplate))
	var output bytes.Buffer
	err := t.Execute(&output, params)

	return output.String(), err
}

func (c *Client) ProvisionMachine(ctx context.Context, candidatesID []string, pod *v1.Pod, proxy virtualpod.PodProxyConfig, promtail bool) (machineID string, err error) {
	logger := log.G(ctx)
	logger.Infof("Mock: Provision machine from %d candidates", len(candidatesID))

	newMachineID := candidatesID[0]

	var wireproxyDir string
	agentPublicPort := c.BasePortAgent + c.machineCounter
	agentLocalPort := strconv.Itoa(32000 + c.machineCounter)
	wireproxyPort := strconv.Itoa(c.BasePortWireproxy + c.machineCounter)

	wireproxyDir = fmt.Sprintf("/tmp/gpu-provider/pod-%s", newMachineID)
	err = os.MkdirAll(wireproxyDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create directory: %v", err)
	}

	wpCfg, err := c.generateWireproxyConfig(ctx, proxy, wireproxyPort, strconv.Itoa(agentPublicPort), agentLocalPort)
	if err != nil {
		return "", fmt.Errorf("failed to genereate wireproxy config template: %v", err)
	}

	err = os.WriteFile(wireproxyDir+"/wireproxy.tpl", []byte(wpCfg), 0644)
	if err != nil {
		return "", fmt.Errorf("failed to write hello file: %v", err)
	}

	a := agent.NewAgent(32000+c.machineCounter, "sleep 36000", wireproxyDir)
	a.EnablePromtail = false
	a.EnableProxy = true

	go func(ctx context.Context) {
		result, _ := a.Run()
		logger.Infof(result)
	}(ctx)

	// Store the machine
	machine := &virtualpod.Machine{
		ID:        newMachineID,
		MachineID: newMachineID,
		PublicIP:  "127.0.0.1",
		AgentPort: agentPublicPort,
		State:     virtualpod.MachineStateRunning,
	}

	c.mu.Lock()
	c.machines[newMachineID] = machine
	c.machineCounter++
	c.mu.Unlock()

	return newMachineID, nil
}

type GenericApiResponse struct {
	Success bool   `json:"success"`
	Message string `json:"msg"`
}

func (c *Client) DestroyMachine(ctx context.Context, id string) error {
	logger := log.G(ctx)
	logger.Infof("Destroying machine: %s", id)

	c.mu.Lock()
	delete(c.machines, id)
	c.mu.Unlock()

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
	result := make(map[string]*virtualpod.Machine)

	return result, nil
}

func (c *Client) PruneDanglingMachines(ctx context.Context, podUIDs []string) error {
	return nil
}

func (c *Client) RestartMachine(ctx context.Context, id string, pullImage bool) error {
	logger := log.G(ctx)
	logger.Infof("Restart machine: %s", id)
	logger.Infof("NOT IMPLEMENTED!!!")

	return nil
}

func (c *Client) RenewMachineKeys(ctx context.Context, machineID string, proxy virtualpod.PodProxyConfig) error {
	logger := log.G(ctx)
	logger.Infof("Renewing machine keys: %s", machineID)
	logger.Infof("NOT IMPLEMENTED!!!")

	command := fmt.Sprintf("echo 'GPU_PROVIDER_GATEWAY_CLIENT_ADDRESS=%s\n' > /etc/virtualpod/wireproxy.keys;", proxy.Client.Address)
	command += fmt.Sprintf("echo 'GPU_PROVIDER_GATEWAY_CLIENT_PK=%s\n' >> /etc/virtualpod/wireproxy.tpl;", proxy.Client.PrivateKey)
	command += fmt.Sprintf("echo 'GPU_PROVIDER_GATEWAY_CLIENT_SERVER_ENDPOINT=%s\n' >> /etc/virtualpod/wireproxy.tpl;", proxy.Server.Endpoint)
	command += fmt.Sprintf("echo 'GPU_PROVIDER_GATEWAY_CLIENT_SERVER_PK=%s\n' >> /etc/virtualpod/wireproxy.tpl", proxy.Server.PublicKey)

	return nil
}

func (c *Client) CopyFileToMachine(ctx context.Context, id string, src, dst string) error { return nil }
