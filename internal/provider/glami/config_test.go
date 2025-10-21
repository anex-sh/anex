package glami

import (
	"os"
	"testing"
)

func TestCamelToSnake(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"clusterUUID", "CLUSTER_UUID"},
		{"cloudProvider", "CLOUD_PROVIDER"},
		{"apiKey", "API_KEY"},
		{"virtualKubelet", "VIRTUAL_KUBELET"},
		{"maxRetries", "MAX_RETRIES"},
		{"machineBansStore", "MACHINE_BANS_STORE"},
		{"localFile", "LOCAL_FILE"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := camelToSnake(tt.input)
			if result != tt.expected {
				t.Errorf("camelToSnake(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetEnvWithPrefix(t *testing.T) {
	tests := []struct {
		keys     []string
		expected string
	}{
		{[]string{"cluster", "clusterUUID"}, "CLUSTER_CLUSTER_UUID"},
		{[]string{"cloudProvider", "apiKey"}, "CLOUD_PROVIDER_API_KEY"},
		{[]string{"virtualKubelet", "provisioning", "maxRetries"}, "VIRTUAL_KUBELET_PROVISIONING_MAX_RETRIES"},
		{[]string{"virtualKubelet", "provisioning", "machineBansStore", "localFile", "enable"}, "VIRTUAL_KUBELET_PROVISIONING_MACHINE_BANS_STORE_LOCAL_FILE_ENABLE"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := getEnvWithPrefix(tt.keys...)
			if result != tt.expected {
				t.Errorf("getEnvWithPrefix(%v) = %q, want %q", tt.keys, result, tt.expected)
			}
		})
	}
}

func TestOverrideWithEnv(t *testing.T) {
	// Set test environment variables
	os.Setenv("CLUSTER_CLUSTER_UUID", "test-cluster")
	os.Setenv("CLOUD_PROVIDER_VAST_AI_API_KEY", "test-key")
	os.Setenv("VIRTUAL_NODE_CPU", "100")
	os.Setenv("VIRTUAL_NODE_MEMORY", "200Gi")
	os.Setenv("VIRTUAL_KUBELET_PROVISIONING_MAX_RETRIES", "5")
	os.Setenv("VIRTUAL_KUBELET_PROVISIONING_MACHINE_BANS_STORE_LOCAL_FILE_ENABLE", "true")

	defer func() {
		os.Unsetenv("CLUSTER_CLUSTER_UUID")
		os.Unsetenv("CLOUD_PROVIDER_VAST_AI_API_KEY")
		os.Unsetenv("VIRTUAL_NODE_CPU")
		os.Unsetenv("VIRTUAL_NODE_MEMORY")
		os.Unsetenv("VIRTUAL_KUBELET_PROVISIONING_MAX_RETRIES")
		os.Unsetenv("VIRTUAL_KUBELET_PROVISIONING_MACHINE_BANS_STORE_LOCAL_FILE_ENABLE")
	}()

	config := ProviderConfig{
		Cluster: ClusterConfig{
			ClusterUUID: "original-cluster",
		},
		CloudProvider: CloudProviderConfig{
			VastAI: VastAIConfig{
				APIKey: "original-key",
			},
		},
		VirtualNode: VirtualNodeConfig{
			CPU:    "50",
			Memory: "100Gi",
		},
		VirtualKubelet: VirtualKubeletConfig{
			Provisioning: VirtualKubeletProvisioningConfig{
				MaxRetries: 10,
				MachineBansStore: MachineBansStoreConfig{
					LocalFile: MachineBansStoreLocalFileConfig{
						Enable: false,
					},
				},
			},
		},
	}

	config.overrideWithEnv()

	if config.Cluster.ClusterUUID != "test-cluster" {
		t.Errorf("Expected cluster UUID to be 'test-cluster', got '%s'", config.Cluster.ClusterUUID)
	}
	if config.CloudProvider.VastAI.APIKey != "test-key" {
		t.Errorf("Expected API key to be 'test-key', got '%s'", config.CloudProvider.VastAI.APIKey)
	}
	if config.VirtualNode.CPU != "100" {
		t.Errorf("Expected CPU to be '100', got '%s'", config.VirtualNode.CPU)
	}
	if config.VirtualNode.Memory != "200Gi" {
		t.Errorf("Expected memory to be '200Gi', got '%s'", config.VirtualNode.Memory)
	}
	if config.VirtualKubelet.Provisioning.MaxRetries != 5 {
		t.Errorf("Expected max retries to be 5, got %d", config.VirtualKubelet.Provisioning.MaxRetries)
	}
	if !config.VirtualKubelet.Provisioning.MachineBansStore.LocalFile.Enable {
		t.Error("Expected machine bans store to be enabled")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input       string
		expectedSec int64
		shouldError bool
	}{
		{"1d", 86400, false},
		{"2d", 172800, false},
		{"1h", 3600, false},
		{"30m", 1800, false},
		{"", 0, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseDuration(tt.input)
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for input %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error for input %q: %v", tt.input, err)
				return
			}
			if int64(result.Seconds()) != tt.expectedSec {
				t.Errorf("parseDuration(%q) = %d seconds, want %d seconds", tt.input, int64(result.Seconds()), tt.expectedSec)
			}
		})
	}
}
