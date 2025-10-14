package glami

import (
	"fmt"
	"os"

	"gitlab.devklarka.cz/ai/gpu-provider/virtualpod"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// Provider configuration defaults.

	// Values used in tracing as attribute keys.
	namespaceKey     = "namespace"
	nameKey          = "name"
	containerNameKey = "containerName"

	defaultCPUCapacity    = "20"
	defaultMemoryCapacity = "100Gi"
	defaultPodCapacity    = "20"
)

type ProvisioningConfig struct {
	RetryLimit         uint64 `yaml:"retryLimit,omitempty"`
	StartupTimeout     uint64 `yaml:"startupTimeout,omitempty"`
	MachineBanDuration uint64 `yaml:"machineBanDuration,omitempty"`
	PersistBansToFile  bool   `yaml:"persistBansToLocalFile,omitempty"`
	BansFilePath       string `yaml:"bansFilePath,omitempty"`
}

type ProviderConfig struct {
	CPU             string             `yaml:"cpu,omitempty"`
	Memory          string             `yaml:"memory,omitempty"`
	Pods            string             `yaml:"pods,omitempty"`
	ProviderID      string             `yaml:"providerID,omitempty"`
	CloudProvider   string             `yaml:"cloudProvider,omitempty"`
	ProxyConfigPath string             `yaml:"proxyConfigPath,omitempty"`
	Provisioning    ProvisioningConfig `yaml:"provisioning,omitempty"`
}

// loadConfig loads the given YAML configuration file. Node name is ignored.
func loadConfig(providerConfig, nodeName string) (config ProviderConfig, err error) {
	data, err := os.ReadFile(providerConfig)
	if err != nil {
		return config, err
	}
	// Unmarshal YAML (also supports JSON), expecting a flat structure directly into ProviderConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}
	// Apply defaults if any field is empty
	if config.CPU == "" {
		config.CPU = defaultCPUCapacity
	}
	if config.Memory == "" {
		config.Memory = defaultMemoryCapacity
	}
	if config.Pods == "" {
		config.Pods = defaultPodCapacity
	}

	if _, err = resource.ParseQuantity(config.CPU); err != nil {
		return config, fmt.Errorf("invalid CPU value %v", config.CPU)
	}
	if _, err = resource.ParseQuantity(config.Memory); err != nil {
		return config, fmt.Errorf("invalid memory value %v", config.Memory)
	}
	if _, err = resource.ParseQuantity(config.Pods); err != nil {
		return config, fmt.Errorf("invalid pods value %v", config.Pods)
	}
	//for _, v := range config.Others {
	//	if _, err = resource.ParseQuantity(v); err != nil {
	//		return config, fmt.Errorf("invalid other value %v", v)
	//	}
	//}
	return config, nil
}

func (p *Provider) loadProxyConfig() error {
	type WireguardKeys struct {
		virtualpod.ProxyServerConfig `yaml:"server"`
		Peers                        []virtualpod.ProxyClientConfig `yaml:"peers"`
	}

	data, err := os.ReadFile(p.config.ProxyConfigPath)
	if err != nil {
		return err
	}

	var keys WireguardKeys
	if err = yaml.Unmarshal(data, &keys); err != nil {
		return err
	}

	p.serverProxySettings = keys.ProxyServerConfig
	p.clientProxySettings = keys.Peers

	return nil
}
