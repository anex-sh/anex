package runpod

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/anex-sh/anex/internal/utils"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

// RunPod cannot take registry credentials inline on pod creation; they must be
// stored account-wide as named registry auth objects and referenced by ID via
// containerRegistryAuthId. Auth names are unique per account, so per-pod auths
// are namespaced with the cluster UID and node name.

type registryAuth struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) buildRegistryAuthName(podUID interface{}) string {
	return fmt.Sprintf("%s%s", c.registryAuthNamePrefix(), podUID)
}

// registryAuthNamePrefix scopes auth names to this cluster and node so garbage
// collection never touches auths owned by other deployments in the account.
func (c *Client) registryAuthNamePrefix() string {
	return fmt.Sprintf("anex-%s-%s-", c.clusterUID, c.nodeName)
}

func (c *Client) listRegistryAuths(ctx context.Context) ([]registryAuth, error) {
	url := baseURL + "/containerregistryauth"
	_, auths, err := utils.MakeRequest[[]registryAuth](ctx, c.retryClient, http.MethodGet, url, nil, c.authHeader)
	return auths, err
}

func (c *Client) createRegistryAuth(ctx context.Context, name, username, password string) (string, error) {
	url := baseURL + "/containerregistryauth"
	payload := map[string]string{
		"name":     name,
		"username": username,
		"password": password,
	}
	_, auth, err := utils.MakeRequest[registryAuth](ctx, c.retryClient, http.MethodPost, url, payload, c.authHeader)
	if err != nil {
		return "", err
	}
	if auth.ID == "" {
		return "", fmt.Errorf("RunPod returned empty registry auth ID")
	}
	return auth.ID, nil
}

func (c *Client) deleteRegistryAuth(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/containerregistryauth/%s", baseURL, id)
	_, _, err := utils.MakeRequest[struct{}](ctx, c.retryClient, http.MethodDelete, url, nil, c.authHeader)
	return err
}

// ensureRegistryAuth registers fresh registry credentials under the given name,
// replacing any existing auth with that name: a leftover auth from a previous
// provisioning attempt holds a stale token, and the name conflict would make
// creation fail anyway.
func (c *Client) ensureRegistryAuth(ctx context.Context, name, username, password string) (string, error) {
	logger := log.G(ctx)

	auths, err := c.listRegistryAuths(ctx)
	if err != nil {
		logger.Warnf("Failed to list RunPod registry auths: %v", err)
	}
	for _, a := range auths {
		if a.Name != name {
			continue
		}
		logger.Infof("Replacing existing RunPod registry auth %s (name: %s)", a.ID, a.Name)
		if err := c.deleteRegistryAuth(ctx, a.ID); err != nil {
			logger.Warnf("Failed to delete stale RunPod registry auth %s: %v", a.ID, err)
		}
	}

	return c.createRegistryAuth(ctx, name, username, password)
}

// pruneRegistryAuths deletes per-pod registry auths whose pod UID is no longer
// present on the virtual node.
func (c *Client) pruneRegistryAuths(ctx context.Context, liveUIDs map[string]bool) {
	logger := log.G(ctx)

	auths, err := c.listRegistryAuths(ctx)
	if err != nil {
		logger.Warnf("Failed to list RunPod registry auths: %v", err)
		return
	}

	prefix := c.registryAuthNamePrefix()
	for _, a := range auths {
		podUID, ok := strings.CutPrefix(a.Name, prefix)
		if !ok || liveUIDs[podUID] {
			continue
		}
		logger.Infof("Deleting dangling RunPod registry auth %s (name: %s)", a.ID, a.Name)
		if err := c.deleteRegistryAuth(ctx, a.ID); err != nil {
			logger.Errorf("Failed to delete dangling RunPod registry auth %s: %v", a.ID, err)
		}
	}
}
