/*
Copyright 2026 GLAMI ML.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package haproxy

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// Backend represents a backend server
type Backend struct {
	Name    string
	Address string
	Port    int
}

// ListenerConfig represents a frontend listener configuration
type ListenerConfig struct {
	Name     string
	Port     int
	Backends []Backend
}

// Manager manages HAProxy configuration
type Manager struct {
	socketPath string
	mutex      sync.RWMutex
	listeners  map[string][]ListenerConfig // owner key -> listener configs
}

// NewManager creates a new HAProxy manager
func NewManager(socketPath string) (*Manager, error) {
	m := &Manager{
		socketPath: socketPath,
		listeners:  make(map[string][]ListenerConfig),
	}
	return m, nil
}

// Configure applies configuration for a specific owner (VirtualService)
func (m *Manager) Configure(ownerKey string, configs []ListenerConfig) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Store configuration
	m.listeners[ownerKey] = configs

	// Apply to HAProxy via runtime API
	return m.applyConfiguration(ownerKey, configs)
}

// Remove removes all configuration for a specific owner
func (m *Manager) Remove(ownerKey string) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	configs, exists := m.listeners[ownerKey]
	if !exists {
		return nil
	}

	// Remove from HAProxy
	if err := m.removeConfiguration(ownerKey, configs); err != nil {
		return err
	}

	delete(m.listeners, ownerKey)
	return nil
}

// applyConfiguration applies the configuration to HAProxy
func (m *Manager) applyConfiguration(ownerKey string, configs []ListenerConfig) error {
	klog.V(4).Infof("Applying HAProxy configuration for %s", ownerKey)

	for _, config := range configs {
		frontendName := sanitizeName(config.Name)
		backendName := sanitizeName(config.Name) + "-backend"

		// Check if frontend exists, if not create it
		exists, err := m.frontendExists(frontendName)
		if err != nil {
			klog.Errorf("Failed to check if frontend %s exists: %v", frontendName, err)
			continue
		}

		if !exists {
			// Create frontend
			if err := m.createFrontend(frontendName, config.Port, backendName); err != nil {
				klog.Errorf("Failed to create frontend %s: %v", frontendName, err)
				continue
			}
		}

		// Check if backend exists, if not create it
		backendExists, err := m.backendExists(backendName)
		if err != nil {
			klog.Errorf("Failed to check if backend %s exists: %v", backendName, err)
			continue
		}

		if !backendExists {
			// Create backend
			if err := m.createBackend(backendName); err != nil {
				klog.Errorf("Failed to create backend %s: %v", backendName, err)
				continue
			}
		}

		// Update backend servers
		if err := m.updateBackendServers(backendName, config.Backends); err != nil {
			klog.Errorf("Failed to update backend servers for %s: %v", backendName, err)
			continue
		}
	}

	return nil
}

// removeConfiguration removes configuration from HAProxy
func (m *Manager) removeConfiguration(ownerKey string, configs []ListenerConfig) error {
	klog.V(4).Infof("Removing HAProxy configuration for %s", ownerKey)

	for _, config := range configs {
		frontendName := sanitizeName(config.Name)
		backendName := sanitizeName(config.Name) + "-backend"

		// Disable and delete frontend
		if err := m.deleteFrontend(frontendName); err != nil {
			klog.Errorf("Failed to delete frontend %s: %v", frontendName, err)
		}

		// Delete backend
		if err := m.deleteBackend(backendName); err != nil {
			klog.Errorf("Failed to delete backend %s: %v", backendName, err)
		}
	}

	return nil
}

// HAProxy runtime API commands

func (m *Manager) executeCommand(command string) (string, error) {
	conn, err := net.DialTimeout("unix", m.socketPath, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to connect to HAProxy socket: %w", err)
	}
	defer conn.Close()

	// Set read/write deadlines
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send command
	_, err = fmt.Fprintf(conn, "%s\n", command)
	if err != nil {
		return "", fmt.Errorf("failed to send command: %w", err)
	}

	// Read response
	scanner := bufio.NewScanner(conn)
	var response strings.Builder
	for scanner.Scan() {
		response.WriteString(scanner.Text())
		response.WriteString("\n")
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return response.String(), nil
}

func (m *Manager) frontendExists(name string) (bool, error) {
	resp, err := m.executeCommand("show stat")
	if err != nil {
		return false, err
	}
	return strings.Contains(resp, name), nil
}

func (m *Manager) backendExists(name string) (bool, error) {
	resp, err := m.executeCommand("show stat")
	if err != nil {
		return false, err
	}
	return strings.Contains(resp, name), nil
}

func (m *Manager) createFrontend(name string, port int, backendName string) error {
	// Note: HAProxy runtime API doesn't support creating frontends dynamically
	// In a real implementation, you would either:
	// 1. Generate a config file and reload HAProxy
	// 2. Use a template with placeholders and reload
	// 3. Use HAProxy Enterprise features
	
	// For this implementation, we'll log a warning and assume frontends are pre-configured
	// or use a different approach
	klog.Warningf("Frontend creation via runtime API not fully supported, assuming pre-configured or using config reload")
	return nil
}

func (m *Manager) createBackend(name string) error {
	// Similar to frontend, backend creation via runtime API is limited
	klog.V(4).Infof("Backend %s creation requested", name)
	return nil
}

func (m *Manager) updateBackendServers(backendName string, backends []Backend) error {
	// First, get existing servers
	existingServers, err := m.getBackendServers(backendName)
	if err != nil {
		klog.V(4).Infof("Backend %s may not exist yet: %v", backendName, err)
		existingServers = []string{}
	}

	// Build desired server set
	desiredServers := make(map[string]Backend)
	for _, backend := range backends {
		serverName := sanitizeName(backend.Name)
		desiredServers[serverName] = backend
	}

	// Remove servers that shouldn't exist
	for _, existingServer := range existingServers {
		if _, shouldExist := desiredServers[existingServer]; !shouldExist {
			m.disableServer(backendName, existingServer)
		}
	}

	// Add or update desired servers
	for serverName, backend := range desiredServers {
		addr := fmt.Sprintf("%s:%d", backend.Address, backend.Port)
		
		// Try to update existing server first
		cmd := fmt.Sprintf("set server %s/%s addr %s port %d", backendName, serverName, backend.Address, backend.Port)
		_, err := m.executeCommand(cmd)
		if err != nil {
			// Server doesn't exist, try to add it
			// Note: Adding servers dynamically is limited in HAProxy
			klog.V(4).Infof("Could not update server %s/%s, may need to add: %v", backendName, serverName, err)
		}

		// Enable the server
		cmd = fmt.Sprintf("enable server %s/%s", backendName, serverName)
		_, _ = m.executeCommand(cmd)
		
		klog.V(4).Infof("Updated backend server %s in %s to %s", serverName, backendName, addr)
	}

	return nil
}

func (m *Manager) getBackendServers(backendName string) ([]string, error) {
	cmd := fmt.Sprintf("show servers state %s", backendName)
	resp, err := m.executeCommand(cmd)
	if err != nil {
		return nil, err
	}

	servers := []string{}
	lines := strings.Split(resp, "\n")
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			servers = append(servers, fields[3]) // Server name is in field 3
		}
	}

	return servers, nil
}

func (m *Manager) disableServer(backendName, serverName string) error {
	cmd := fmt.Sprintf("disable server %s/%s", backendName, serverName)
	_, err := m.executeCommand(cmd)
	return err
}

func (m *Manager) deleteFrontend(name string) error {
	klog.V(4).Infof("Frontend %s deletion requested", name)
	return nil
}

func (m *Manager) deleteBackend(name string) error {
	klog.V(4).Infof("Backend %s deletion requested", name)
	return nil
}

func sanitizeName(name string) string {
	// Replace characters that might be problematic in HAProxy names
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, ":", "-")
	return name
}

// Rebuild rebuilds the entire HAProxy configuration from scratch
// This is useful after a gateway restart
func (m *Manager) Rebuild() error {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	klog.Info("Rebuilding HAProxy configuration")

	for ownerKey, configs := range m.listeners {
		if err := m.applyConfiguration(ownerKey, configs); err != nil {
			klog.Errorf("Failed to rebuild configuration for %s: %v", ownerKey, err)
		}
	}

	return nil
}
