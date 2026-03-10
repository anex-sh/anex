package runpod

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

// Client implements cloudprovider.Client for RunPod.
type Client struct {
	apiKey     string
	clusterUID string
	nodeName   string
}

func NewClient(apiKey, clusterUID, nodeName string) *Client {
	return &Client{
		apiKey:     apiKey,
		clusterUID: clusterUID,
		nodeName:   nodeName,
	}
}

var errNotImplemented = fmt.Errorf("runpod: not implemented")

func (c *Client) SupportsMachineBans() bool { return false }
func (c *Client) BanMachine(_ string)       {}

func (c *Client) SelectAndProvisionMachine(ctx context.Context, spec virtualpod.MachineSpecification, _ *v1.Pod, _ virtualpod.PodProxyConfig, _ bool, _ record.EventRecorder) (string, error) {
	logger := log.G(ctx)

	query, warnings := BuildProvisionQuery(spec)
	for _, w := range warnings {
		logger.Warnf("RunPod filter: %s", w)
	}

	payload, err := json.MarshalIndent(query, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal provisioning query: %w", err)
	}

	logger.Infof("RunPod provisioning query (dry-run):\n%s", string(payload))
	return "", errNotImplemented
}

func (c *Client) GetMachine(_ context.Context, _ string) (*virtualpod.Machine, error) {
	return nil, errNotImplemented
}

func (c *Client) ListMachines(_ context.Context) ([]*virtualpod.Machine, error) {
	return nil, errNotImplemented
}

func (c *Client) MapRunningMachines(_ context.Context, _ *v1.PodList) (map[string]*virtualpod.Machine, error) {
	return nil, errNotImplemented
}

func (c *Client) PruneDanglingMachines(_ context.Context, _ []string) error {
	return errNotImplemented
}

func (c *Client) DestroyMachine(_ context.Context, _ string) error {
	return errNotImplemented
}

func (c *Client) RestartMachine(_ context.Context, _ string, _ bool) error {
	return errNotImplemented
}

func (c *Client) RenewMachineKeys(_ context.Context, _ string, _ virtualpod.PodProxyConfig) error {
	return errNotImplemented
}

func (c *Client) CopyFileToMachine(_ context.Context, _ string, _, _ string) error {
	return errNotImplemented
}
