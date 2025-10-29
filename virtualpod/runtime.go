package virtualpod

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"text/template"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"gitlab.devklarka.cz/ai/gpu-provider/internal/utils"
)

type GenericResponse struct {
	Message string `json:"message"`
}

func (vp *VirtualPod) WaitForAgentReady(ctx context.Context, httpClient *retryablehttp.Client) error {
	logger := log.G(ctx)

	vp.mutex.RLock()
	url := vp.machine.GetAgentAddress() + "/healthz"
	headers := vp.authHeaders()
	vp.mutex.RUnlock()

	logger.Infof("Waiting for agent to be ready on %s", url)
	status, _, err := utils.MakeRequest[GenericResponse](ctx, httpClient, http.MethodGet, url, nil, headers)
	logger.Errorf("Wait for Agent ready ended with status %d", status)

	return err
}

func (vp *VirtualPod) pushFile(ctx context.Context, httpClient *retryablehttp.Client, targetPath string, data string) error {
	logger := log.G(ctx)

	vp.mutex.RLock()
	url := vp.machine.GetAgentAddress() + "/push_file"
	headers := vp.authHeaders()
	vp.mutex.RUnlock()

	type pushFileRequest struct {
		Filepath string `json:"filepath"`
		Data     string `json:"data"`
	}

	payload := pushFileRequest{Filepath: targetPath, Data: data}
	status, _, err := utils.MakeRequest[GenericResponse](ctx, httpClient, http.MethodPost, url, payload, headers)

	if err != nil || status < 200 || status >= 300 {
		if err != nil {
			logger.Errorf("failed to push file %s: %v", targetPath, err)
		} else {
			logger.Errorf("failed to push file %s: http %d", targetPath, status)
		}
	}

	return nil
}

func (vp *VirtualPod) PushEnvVars(ctx context.Context, httpClient *retryablehttp.Client) error {
	if httpClient == nil {
		return fmt.Errorf("httpClient is nil")
	}

	vp.mutex.RLock()
	envVars := vp.pod.Spec.Containers[0].Env
	vp.mutex.RUnlock()

	target := "/etc/virtualpod/environment"
	var data string
	for _, env := range envVars {
		// TODO: Remove after implementing service account token
		if strings.HasPrefix(env.Name, "AWS_WEB_IDENTITY_TOKEN_FILE") {
			continue
		}
		data += fmt.Sprintf("%s=%s\n", env.Name, env.Value)
	}

	return vp.pushFile(ctx, httpClient, target, data)
}

func (vp *VirtualPod) PushConfigMaps(ctx context.Context, httpClient *retryablehttp.Client) error {
	logger := log.G(ctx)
	if vp.volumeMounts == nil || len(vp.configMaps) == 0 {
		return nil
	}

	for _, f := range vp.volumeMounts {
		cm, ok := vp.configMaps[f.ConfigMapName]
		if !ok {
			logger.Errorf("configMap %q not found", f.ConfigMapName)
			continue
		}
		data, ok := cm[f.Key]
		if !ok {
			logger.Errorf("key %q missing in configMap %q", f.Key, f.ConfigMapName)
			continue
		}

		target := f.TargetPath

		err := vp.pushFile(ctx, httpClient, target, data)
		if err != nil {
			return err
		}
	}

	return nil
}

func (vp *VirtualPod) PushWireproxyConfig(ctx context.Context, httpClient *retryablehttp.Client) error {
	targetPath := "/etc/virtualpod/wireproxy.tpl"
	config, err := vp.generateWireproxyConfig(ctx)
	if err != nil {
		return err
	}

	return vp.pushFile(ctx, httpClient, targetPath, config)
}

func (vp *VirtualPod) PushPromtailConfig(ctx context.Context, httpClient *retryablehttp.Client, lokiConfig LokiPushGateway) error {
	vp.mutex.RLock()
	lokiConfig.PodName = vp.pod.Name
	lokiConfig.Namespace = vp.pod.Namespace
	vp.mutex.RUnlock()

	var output bytes.Buffer
	t := template.Must(template.New("wireproxy").Parse(promtailTemplate))
	err := t.Execute(&output, lokiConfig)

	targetPath := "/etc/virtualpod/promtail.yaml"
	config := output.String()
	if err != nil {
		return err
	}

	return vp.pushFile(ctx, httpClient, targetPath, config)
}

func (vp *VirtualPod) RunCommand(ctx context.Context, httpClient *retryablehttp.Client) error {
	vp.mutex.RLock()
	url := vp.machine.GetAgentAddress() + "/run"
	headers := vp.authHeaders()
	vp.mutex.RUnlock()

	logger := log.G(ctx)
	status, _, err := utils.MakeRequest[GenericResponse](ctx, httpClient, http.MethodPut, url, nil, headers)
	logger.Infof("runCommand: %d", status)

	return err
}
