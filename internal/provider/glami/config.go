package glami

import (
	"fmt"
	"os"
	"strings"
	"time"

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

// ClusterConfig holds cluster-specific configuration
type ClusterConfig struct {
	ClusterUUID string `yaml:"clusterUUID,omitempty"`
}

// VastAIConfig holds VastAI-specific configuration
type VastAIConfig struct {
	APIKey string `yaml:"apiKey"`
}

// CloudProviderConfig holds cloud provider configuration
type CloudProviderConfig struct {
	VastAI VastAIConfig `yaml:"vastAI"`
}

// VirtualKubeletImageConfig holds image configuration
type VirtualKubeletImageConfig struct {
	Repository string `yaml:"repository"`
	PullPolicy string `yaml:"pullPolicy"`
}

// VirtualKubeletServiceAccountConfig holds service account configuration
type VirtualKubeletServiceAccountConfig struct {
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// MachineBansStoreLocalFileConfig holds local file configuration for machine bans
type MachineBansStoreLocalFileConfig struct {
	Enable  bool   `yaml:"enable"`
	Timeout string `yaml:"timeout,omitempty"`
}

// MachineBansStoreConfig holds machine bans store configuration
type MachineBansStoreConfig struct {
	LocalFile MachineBansStoreLocalFileConfig `yaml:"localFile"`
}

// VirtualKubeletProvisioningConfig holds provisioning configuration
type VirtualKubeletProvisioningConfig struct {
	MaxRetries       int                    `yaml:"maxRetries,omitempty"`
	StartupTimeout   string                 `yaml:"startupTimeout,omitempty"`
	MachineBansStore MachineBansStoreConfig `yaml:"machineBansStore"`
}

// VirtualKubeletConfig holds virtual kubelet configuration
type VirtualKubeletConfig struct {
	Image          VirtualKubeletImageConfig          `yaml:"image"`
	ServiceAccount VirtualKubeletServiceAccountConfig `yaml:"serviceAccount"`
	Provisioning   VirtualKubeletProvisioningConfig   `yaml:"provisioning"`
}

// VirtualNodeConfig holds virtual node configuration
type VirtualNodeConfig struct {
	Pods   string `yaml:"pods"`
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// ProxyConfig holds proxy configuration
type ProxyConfig struct {
	Enable     bool   `yaml:"enable"`
	ConfigPath string `yaml:"configPath,omitempty"`
}

// PromtailClientConfig holds promtail client configuration
type PromtailClientConfig struct {
	URL       string                   `yaml:"url"`
	BasicAuth *PromtailBasicAuthConfig `yaml:"basicAuth,omitempty"`
}

// PromtailBasicAuthConfig holds basic auth configuration
type PromtailBasicAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// PromtailConfig holds promtail configuration
type PromtailConfig struct {
	Enable  bool                   `yaml:"enable"`
	Clients []PromtailClientConfig `yaml:"clients,omitempty"`
}

// ProviderConfig is the complete configuration structure
type ProviderConfig struct {
	Cluster        ClusterConfig        `yaml:"cluster"`
	CloudProvider  CloudProviderConfig  `yaml:"cloudProvider"`
	VirtualKubelet VirtualKubeletConfig `yaml:"virtualKubelet"`
	VirtualNode    VirtualNodeConfig    `yaml:"virtualNode"`
	Proxy          ProxyConfig          `yaml:"proxy"`
	Promtail       PromtailConfig       `yaml:"promtail"`
	AgentAuthToken string               `yaml:"agentAuthToken,omitempty"`
}

// Legacy fields for compatibility
type ProvisioningConfig struct {
	RetryLimit         uint64 `yaml:"retryLimit,omitempty"`
	StartupTimeout     uint64 `yaml:"startupTimeout,omitempty"`
	MachineBanDuration uint64 `yaml:"machineBanDuration,omitempty"`
	PersistBansToFile  bool   `yaml:"persistBansToLocalFile,omitempty"`
	BansFilePath       string `yaml:"bansFilePath,omitempty"`
}

// camelToSnake converts camelCase to snake_case
func camelToSnake(s string) string {
	var result strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			// Check if previous character is lowercase
			prevRune := rune(s[i-1])
			if prevRune >= 'a' && prevRune <= 'z' {
				result.WriteRune('_')
			}
		}
		result.WriteRune(r)
	}
	return strings.ToUpper(result.String())
}

// getEnvWithPrefix builds the environment variable name from nested keys
func getEnvWithPrefix(keys ...string) string {
	var parts []string
	for _, key := range keys {
		parts = append(parts, camelToSnake(key))
	}
	return strings.Join(parts, "_")
}

// overrideWithEnv overrides configuration values with environment variables
func (c *ProviderConfig) overrideWithEnv() {
	// Cluster
	if val := os.Getenv(getEnvWithPrefix("cluster", "clusterUUID")); val != "" {
		c.Cluster.ClusterUUID = val
	}

	// CloudProvider - VastAI
	if val := os.Getenv(getEnvWithPrefix("cloudProvider", "vastAI", "apiKey")); val != "" {
		c.CloudProvider.VastAI.APIKey = val
	}

	// VirtualKubelet.Image
	if val := os.Getenv(getEnvWithPrefix("virtualKubelet", "image", "repository")); val != "" {
		c.VirtualKubelet.Image.Repository = val
	}
	if val := os.Getenv(getEnvWithPrefix("virtualKubelet", "image", "pullPolicy")); val != "" {
		c.VirtualKubelet.Image.PullPolicy = val
	}

	// VirtualKubelet.Provisioning
	if val := os.Getenv(getEnvWithPrefix("virtualKubelet", "provisioning", "maxRetries")); val != "" {
		fmt.Sscanf(val, "%d", &c.VirtualKubelet.Provisioning.MaxRetries)
	}
	if val := os.Getenv(getEnvWithPrefix("virtualKubelet", "provisioning", "startupTimeout")); val != "" {
		c.VirtualKubelet.Provisioning.StartupTimeout = val
	}
	if val := os.Getenv(getEnvWithPrefix("virtualKubelet", "provisioning", "machineBansStore", "localFile", "enable")); val != "" {
		c.VirtualKubelet.Provisioning.MachineBansStore.LocalFile.Enable = val == "true"
	}
	if val := os.Getenv(getEnvWithPrefix("virtualKubelet", "provisioning", "machineBansStore", "localFile", "timeout")); val != "" {
		c.VirtualKubelet.Provisioning.MachineBansStore.LocalFile.Timeout = val
	}

	// VirtualNode
	if val := os.Getenv(getEnvWithPrefix("virtualNode", "pods")); val != "" {
		c.VirtualNode.Pods = val
	}
	if val := os.Getenv(getEnvWithPrefix("virtualNode", "cpu")); val != "" {
		c.VirtualNode.CPU = val
	}
	if val := os.Getenv(getEnvWithPrefix("virtualNode", "memory")); val != "" {
		c.VirtualNode.Memory = val
	}

	// Proxy
	if val := os.Getenv(getEnvWithPrefix("proxy", "enable")); val != "" {
		c.Proxy.Enable = val == "true"
	}
	if val := os.Getenv(getEnvWithPrefix("proxy", "configPath")); val != "" {
		c.Proxy.ConfigPath = val
	}

	// Promtail
	if val := os.Getenv(getEnvWithPrefix("promtail", "enable")); val != "" {
		c.Promtail.Enable = val == "true"
	}

	// AgentAuthToken
	if val := os.Getenv(getEnvWithPrefix("agentAuthToken")); val != "" {
		c.AgentAuthToken = val
	}
}

// parseDuration parses duration strings like "1d", "2h", "30m"
func parseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}

	// Handle days
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var d int
		_, err := fmt.Sscanf(days, "%d", &d)
		if err != nil {
			return 0, err
		}
		return time.Duration(d) * 24 * time.Hour, nil
	}

	// Otherwise use standard time.ParseDuration
	return time.ParseDuration(s)
}

// GetMachineBanDuration returns the machine ban duration in seconds
func (c *ProviderConfig) GetMachineBanDuration() uint64 {
	if !c.VirtualKubelet.Provisioning.MachineBansStore.LocalFile.Enable {
		return 0
	}

	duration, err := parseDuration(c.VirtualKubelet.Provisioning.MachineBansStore.LocalFile.Timeout)
	if err != nil {
		return 0
	}

	return uint64(duration.Seconds())
}

// GetStartupTimeout returns the startup timeout as a time.Duration
func (c *ProviderConfig) GetStartupTimeout() time.Duration {
	if c.VirtualKubelet.Provisioning.StartupTimeout == "" {
		return 10 * time.Minute // default
	}

	duration, err := parseDuration(c.VirtualKubelet.Provisioning.StartupTimeout)
	if err != nil {
		return 10 * time.Minute // fallback to default
	}

	return duration
}

// loadConfig loads the given YAML configuration file. Node name is ignored.
func loadConfig(providerConfig, nodeName string) (config ProviderConfig, err error) {
	data, err := os.ReadFile(providerConfig)
	if err != nil {
		return config, err
	}

	// Unmarshal YAML
	if err = yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	// Override with environment variables
	config.overrideWithEnv()

	// Validate required fields
	if config.CloudProvider.VastAI.APIKey == "" {
		return config, fmt.Errorf("cloudProvider.vastAI.apiKey is required and cannot be empty")
	}

	// Apply defaults for VirtualNode if any field is empty
	if config.VirtualNode.CPU == "" {
		config.VirtualNode.CPU = defaultCPUCapacity
	}
	if config.VirtualNode.Memory == "" {
		config.VirtualNode.Memory = defaultMemoryCapacity
	}
	if config.VirtualNode.Pods == "" {
		config.VirtualNode.Pods = defaultPodCapacity
	}

	// Validate resource quantities
	if _, err = resource.ParseQuantity(config.VirtualNode.CPU); err != nil {
		return config, fmt.Errorf("invalid CPU value %v", config.VirtualNode.CPU)
	}
	if _, err = resource.ParseQuantity(config.VirtualNode.Memory); err != nil {
		return config, fmt.Errorf("invalid memory value %v", config.VirtualNode.Memory)
	}
	if _, err = resource.ParseQuantity(config.VirtualNode.Pods); err != nil {
		return config, fmt.Errorf("invalid pods value %v", config.VirtualNode.Pods)
	}

	// Validate promtail config
	if config.Promtail.Enable && len(config.Promtail.Clients) == 0 {
		return config, fmt.Errorf("promtail.enable is true but promtail.clients list is empty")
	}

	return config, nil
}

func (p *Provider) loadProxyConfig() error {
	type WireguardKeys struct {
		virtualpod.ProxyServerConfig `yaml:"server"`
		Peers                        []virtualpod.ProxyClientConfig `yaml:"peers"`
	}

	proxyConfigPath := p.config.Proxy.ConfigPath
	if proxyConfigPath == "" {
		return fmt.Errorf("proxy.configPath is required when proxy is enabled")
	}

	data, err := os.ReadFile(proxyConfigPath)
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
