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

// TODO: Refactor defaults
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

// LogLevel represents the logging level
type LogLevel string

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

// MachineBansStoreLocalFileConfig holds local file configuration for machine bans
type MachineBansStoreLocalFileConfig struct {
	Enable  bool   `yaml:"enable"`
	Path    string `yaml:"path,omitempty"`
	Timeout string `yaml:"timeout,omitempty"`
}

// MachineBansStoreConfig holds machine bans store configuration
type MachineBansStoreConfig struct {
	LocalFile MachineBansStoreLocalFileConfig `yaml:"localFile"`
}

// ProvisioningConfig holds provisioning configuration
type ProvisioningConfig struct {
	MaxRetries          int                    `yaml:"maxRetries,omitempty"`
	StartupTimeout      string                 `yaml:"startupTimeout,omitempty"`
	StatusReportTimeout string                 `yaml:"statusReportTimeout,omitempty"`
	MachineBansStore    MachineBansStoreConfig `yaml:"machineBansStore"`
}

// TaintConfig holds a taint entry for the virtual node
type TaintConfig struct {
	Key      string `yaml:"key"`
	Operator string `yaml:"operator,omitempty"`
	Effect   string `yaml:"effect"`
	Value    string `yaml:"value,omitempty"`
}

// VirtualNodeConfig holds virtual node configuration
type VirtualNodeConfig struct {
	NodeName string            `yaml:"nodeName,omitempty"`
	PodLimit string            `yaml:"podLimit"`
	CPU      string            `yaml:"cpu"`
	Memory   string            `yaml:"memory"`
	Labels   map[string]string `yaml:"labels,omitempty"`
	Taints   []TaintConfig     `yaml:"taints,omitempty"`
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
	LogLevel       string              `yaml:"logLevel,omitempty"`
	Cluster        ClusterConfig       `yaml:"cluster"`
	CloudProvider  CloudProviderConfig `yaml:"cloudProvider"`
	Provisioning   ProvisioningConfig  `yaml:"provisioning"`
	VirtualNode    VirtualNodeConfig   `yaml:"virtualNode"`
	Proxy          ProxyConfig         `yaml:"proxy"`
	Promtail       PromtailConfig      `yaml:"promtail"`
	AgentAuthToken string              `yaml:"agentAuthToken,omitempty"`
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
	// LogLevel
	if val := os.Getenv(getEnvWithPrefix("logLevel")); val != "" {
		c.LogLevel = val
	}

	// Cluster
	if val := os.Getenv(getEnvWithPrefix("cluster", "clusterUUID")); val != "" {
		c.Cluster.ClusterUUID = val
	}

	// CloudProvider - VastAI
	if val := os.Getenv(getEnvWithPrefix("cloudProvider", "vastAI", "apiKey")); val != "" {
		c.CloudProvider.VastAI.APIKey = val
	}

	// Provisioning
	if val := os.Getenv(getEnvWithPrefix("provisioning", "maxRetries")); val != "" {
		fmt.Sscanf(val, "%d", &c.Provisioning.MaxRetries)
	}
	if val := os.Getenv(getEnvWithPrefix("provisioning", "startupTimeout")); val != "" {
		c.Provisioning.StartupTimeout = val
	}
	if val := os.Getenv(getEnvWithPrefix("provisioning", "statusReportTimeout")); val != "" {
		c.Provisioning.StatusReportTimeout = val
	}
	if val := os.Getenv(getEnvWithPrefix("provisioning", "machineBansStore", "localFile", "enable")); val != "" {
		c.Provisioning.MachineBansStore.LocalFile.Enable = val == "true"
	}
	if val := os.Getenv(getEnvWithPrefix("provisioning", "machineBansStore", "localFile", "path")); val != "" {
		c.Provisioning.MachineBansStore.LocalFile.Path = val
	}
	if val := os.Getenv(getEnvWithPrefix("provisioning", "machineBansStore", "localFile", "timeout")); val != "" {
		c.Provisioning.MachineBansStore.LocalFile.Timeout = val
	}

	// VirtualNode
	if val := os.Getenv(getEnvWithPrefix("virtualNode", "nodeName")); val != "" {
		c.VirtualNode.NodeName = val
	}
	if val := os.Getenv(getEnvWithPrefix("virtualNode", "pods")); val != "" {
		c.VirtualNode.PodLimit = val
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
	if !c.Provisioning.MachineBansStore.LocalFile.Enable {
		return 0
	}

	duration, err := parseDuration(c.Provisioning.MachineBansStore.LocalFile.Timeout)
	if err != nil {
		return 0
	}

	return uint64(duration.Seconds())
}

// GetStartupTimeout returns the startup timeout as a time.Duration
func (c *ProviderConfig) GetStartupTimeout() time.Duration {
	if c.Provisioning.StartupTimeout == "" {
		return 10 * time.Minute // default
	}

	duration, err := parseDuration(c.Provisioning.StartupTimeout)
	if err != nil {
		return 10 * time.Minute // fallback to default
	}

	return duration
}

// GetStatusReportTimeout returns the status report timeout as a time.Duration
func (c *ProviderConfig) GetStatusReportTimeout() time.Duration {
	if c.Provisioning.StatusReportTimeout == "" {
		return 5 * time.Minute // default
	}

	duration, err := parseDuration(c.Provisioning.StatusReportTimeout)
	if err != nil {
		return 5 * time.Minute // fallback to default
	}

	return duration
}

// loadConfig loads the given YAML configuration file. Node name is ignored.
func loadConfig(providerConfig string) (config ProviderConfig, err error) {
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

	// Apply defaults
	if config.LogLevel == "" {
		config.LogLevel = "info"
	}

	// Apply defaults for Provisioning
	if config.Provisioning.MaxRetries == 0 {
		config.Provisioning.MaxRetries = 10
	}
	if config.Provisioning.StartupTimeout == "" {
		config.Provisioning.StartupTimeout = "10m"
	}
	if config.Provisioning.StatusReportTimeout == "" {
		config.Provisioning.StatusReportTimeout = "2m"
	}
	if config.Provisioning.MachineBansStore.LocalFile.Path == "" {
		config.Provisioning.MachineBansStore.LocalFile.Path = "/tmp/machine-bans.json"
	}
	if config.Provisioning.MachineBansStore.LocalFile.Timeout == "" {
		config.Provisioning.MachineBansStore.LocalFile.Timeout = "1d"
	}

	// Apply defaults for VirtualNode
	if config.VirtualNode.NodeName == "" {
		config.VirtualNode.NodeName = "virtual-node"
	}
	if config.VirtualNode.CPU == "" {
		config.VirtualNode.CPU = defaultCPUCapacity
	}
	if config.VirtualNode.Memory == "" {
		config.VirtualNode.Memory = defaultMemoryCapacity
	}
	if config.VirtualNode.PodLimit == "" {
		config.VirtualNode.PodLimit = defaultPodCapacity
	}

	// Validate required fields
	if config.CloudProvider.VastAI.APIKey == "" {
		return config, fmt.Errorf("cloudProvider.vastAI.apiKey is required and cannot be empty")
	}

	// Validate resource quantities
	if _, err = resource.ParseQuantity(config.VirtualNode.CPU); err != nil {
		return config, fmt.Errorf("invalid CPU value %v", config.VirtualNode.CPU)
	}
	if _, err = resource.ParseQuantity(config.VirtualNode.Memory); err != nil {
		return config, fmt.Errorf("invalid memory value %v", config.VirtualNode.Memory)
	}
	if _, err = resource.ParseQuantity(config.VirtualNode.PodLimit); err != nil {
		return config, fmt.Errorf("invalid pods value %v", config.VirtualNode.PodLimit)
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
		Peers                        []*virtualpod.ProxyClientConfig `yaml:"peers"`
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
