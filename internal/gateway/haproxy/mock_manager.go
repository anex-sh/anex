package haproxy

import (
	"sync"
)

// ConfigureCall records a single call to Configure
type ConfigureCall struct {
	OwnerKey string
	Configs  []ListenerConfig
}

// MockManager is a mock implementation of Configurer for testing
type MockManager struct {
	mu sync.Mutex

	// Configs stores the current configuration per ownerKey
	Configs map[string][]ListenerConfig

	// ConfigureCalls records all calls to Configure
	ConfigureCalls []ConfigureCall

	// RemoveCalls records all owner keys passed to Remove
	RemoveCalls []string

	// ConfigureError if set, Configure will return this error
	ConfigureError error

	// RemoveError if set, Remove will return this error
	RemoveError error
}

// NewMockManager creates a new MockManager
func NewMockManager() *MockManager {
	return &MockManager{
		Configs:        make(map[string][]ListenerConfig),
		ConfigureCalls: []ConfigureCall{},
		RemoveCalls:    []string{},
	}
}

// Configure implements Configurer
func (m *MockManager) Configure(ownerKey string, configs []ListenerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ConfigureError != nil {
		return m.ConfigureError
	}

	// Record the call
	m.ConfigureCalls = append(m.ConfigureCalls, ConfigureCall{
		OwnerKey: ownerKey,
		Configs:  configs,
	})

	// Store the configuration
	m.Configs[ownerKey] = configs

	return nil
}

// Remove implements Configurer
func (m *MockManager) Remove(ownerKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.RemoveError != nil {
		return m.RemoveError
	}

	// Record the call
	m.RemoveCalls = append(m.RemoveCalls, ownerKey)

	// Remove the configuration
	delete(m.Configs, ownerKey)

	return nil
}

// Reset clears all recorded calls and configurations
func (m *MockManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Configs = make(map[string][]ListenerConfig)
	m.ConfigureCalls = []ConfigureCall{}
	m.RemoveCalls = []string{}
	m.ConfigureError = nil
	m.RemoveError = nil
}

// GetConfigs returns a copy of the current configurations
func (m *MockManager) GetConfigs(ownerKey string) []ListenerConfig {
	m.mu.Lock()
	defer m.mu.Unlock()

	configs, ok := m.Configs[ownerKey]
	if !ok {
		return nil
	}

	// Return a copy to avoid race conditions
	result := make([]ListenerConfig, len(configs))
	copy(result, configs)
	return result
}

// GetConfigureCallCount returns the number of Configure calls
func (m *MockManager) GetConfigureCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.ConfigureCalls)
}

// GetRemoveCallCount returns the number of Remove calls
func (m *MockManager) GetRemoveCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.RemoveCalls)
}

// GetLastConfigureCall returns the last Configure call, or nil if none
func (m *MockManager) GetLastConfigureCall() *ConfigureCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.ConfigureCalls) == 0 {
		return nil
	}
	call := m.ConfigureCalls[len(m.ConfigureCalls)-1]
	return &call
}

// Ensure MockManager implements Configurer
var _ Configurer = (*MockManager)(nil)
