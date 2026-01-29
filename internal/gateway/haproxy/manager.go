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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
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

// Manager manages HAProxy configuration via Data Plane API
type Manager struct {
	socketPath string
	apiURL     string
	client     *http.Client
	mutex      sync.RWMutex
	listeners  map[string][]ListenerConfig // owner key -> listener configs
}

 // authRoundTripper adds Basic Auth to every request when username is set
type authRoundTripper struct {
	base     http.RoundTripper
	username string
	password string
}

func (t authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.username != "" {
		// Clone the request to avoid mutating caller's request
		r := req.Clone(req.Context())
		r.SetBasicAuth(t.username, t.password)
		return t.base.RoundTrip(r)
	}
	return t.base.RoundTrip(req)
}

// NewManager creates a new HAProxy manager using Data Plane API
func NewManager(endpoint, username, password string) (*Manager, error) {
	// Support both Unix socket path and HTTP(S) URL endpoints
	var baseTransport http.RoundTripper
	var apiBase string

	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		// TCP over localhost (or provided host)
		baseTransport = http.DefaultTransport
		apiBase = strings.TrimRight(endpoint, "/") + "/v2"
	} else {
		// Unix domain socket
		baseTransport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", endpoint)
			},
		}
		apiBase = "http://unix/v2"
	}

	client := &http.Client{
		Transport: authRoundTripper{
			base:     baseTransport,
			username: username,
			password: password,
		},
		Timeout: 10 * time.Second,
	}

	m := &Manager{
		socketPath: endpoint,
		apiURL:     apiBase,
		client:     client,
		listeners:  make(map[string][]ListenerConfig),
	}

	// Wait for Data Plane API to be ready
	if err := m.waitForAPI(); err != nil {
		klog.Warningf("Data Plane API not ready yet: %v", err)
	}

	return m, nil
}

// waitForAPI waits for the Data Plane API to become available
func (m *Manager) waitForAPI() error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := m.client.Get(m.apiURL + "/info")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				klog.Info("HAProxy Data Plane API is ready")
				return nil
			}
			klog.V(4).Infof("Data Plane API not ready yet, status=%d", resp.StatusCode)
		} else {
			klog.V(4).Infof("Waiting for Data Plane API: %v", err)
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("Data Plane API not available after 60 seconds")
}

// Configure applies configuration for a specific owner (VirtualService)
func (m *Manager) Configure(ownerKey string, configs []ListenerConfig) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Store configuration
	m.listeners[ownerKey] = configs

	// Apply to HAProxy via Data Plane API
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

		// Start a transaction
		txID, err := m.startTransaction()
		if err != nil {
			klog.Errorf("Failed to start transaction: %v", err)
			continue
		}

		// Ensure backend exists
		if err := m.ensureBackend(txID, backendName); err != nil {
			klog.Errorf("Failed to ensure backend %s: %v", backendName, err)
			m.deleteTransaction(txID)
			continue
		}

		// Update backend servers
		if err := m.updateBackendServers(txID, backendName, config.Backends); err != nil {
			klog.Errorf("Failed to update backend servers for %s: %v", backendName, err)
			m.deleteTransaction(txID)
			continue
		}

		// Ensure frontend exists
		if err := m.ensureFrontend(txID, frontendName, config.Port, backendName); err != nil {
			klog.Errorf("Failed to ensure frontend %s: %v", frontendName, err)
			m.deleteTransaction(txID)
			continue
		}

		// Commit transaction
		if err := m.commitTransaction(txID); err != nil {
			klog.Errorf("Failed to commit transaction: %v", err)
			m.deleteTransaction(txID)
			continue
		}

		klog.V(4).Infof("Successfully configured %s with %d backends", frontendName, len(config.Backends))
	}

	return nil
}

// removeConfiguration removes configuration from HAProxy
func (m *Manager) removeConfiguration(ownerKey string, configs []ListenerConfig) error {
	klog.V(4).Infof("Removing HAProxy configuration for %s", ownerKey)

	for _, config := range configs {
		frontendName := sanitizeName(config.Name)
		backendName := sanitizeName(config.Name) + "-backend"

		// Start a transaction
		txID, err := m.startTransaction()
		if err != nil {
			klog.Errorf("Failed to start transaction: %v", err)
			continue
		}

		// Delete frontend
		if err := m.deleteFrontend(txID, frontendName); err != nil {
			klog.Warningf("Failed to delete frontend %s: %v", frontendName, err)
		}

		// Delete backend
		if err := m.deleteBackend(txID, backendName); err != nil {
			klog.Warningf("Failed to delete backend %s: %v", backendName, err)
		}

		// Commit transaction
		if err := m.commitTransaction(txID); err != nil {
			klog.Errorf("Failed to commit transaction: %v", err)
			m.deleteTransaction(txID)
			continue
		}
	}

	return nil
}

 // Transaction management

func (m *Manager) getConfigVersion() (int, error) {
	url := fmt.Sprintf("%s/services/haproxy/configuration/version", m.apiURL)
	resp, err := m.client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("failed to get config version: %s (status: %d)", string(body), resp.StatusCode)
	}

	var v interface{}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return 0, err
	}

	switch t := v.(type) {
	case float64:
		return int(t), nil
	case map[string]interface{}:
		if val, ok := t["version"]; ok {
			switch vv := val.(type) {
			case float64:
				return int(vv), nil
			case int:
				return vv, nil
			}
		}
		if val, ok := t["_version"]; ok {
			switch vv := val.(type) {
			case float64:
				return int(vv), nil
			case int:
				return vv, nil
			}
		}
	}

	return 0, fmt.Errorf("unable to parse configuration version")
}

func (m *Manager) startTransaction() (string, error) {
	version, err := m.getConfigVersion()
	if err != nil {
		return "", fmt.Errorf("failed to determine configuration version: %w", err)
	}

	txURL := fmt.Sprintf("%s/services/haproxy/transactions?version=%d", m.apiURL, version)
	resp, err := m.client.Post(txURL, "application/json", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to start transaction: %s (status: %d)", string(body), resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	txID, ok := result["id"].(string)
	if !ok {
		return "", fmt.Errorf("transaction ID not found in response")
	}

	return txID, nil
}

func (m *Manager) commitTransaction(txID string) error {
	url := fmt.Sprintf("%s/services/haproxy/transactions/%s?force_reload=true", m.apiURL, txID)
	req, err := http.NewRequest(http.MethodPut, url, nil)
	if err != nil {
		return err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to commit transaction: %s (status: %d)", string(body), resp.StatusCode)
	}

	return nil
}

func (m *Manager) deleteTransaction(txID string) error {
	url := fmt.Sprintf("%s/services/haproxy/transactions/%s", m.apiURL, txID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// Frontend management

func (m *Manager) ensureFrontend(txID, name string, port int, backendName string) error {
	// Check if frontend exists
	url := fmt.Sprintf("%s/services/haproxy/configuration/frontends/%s?transaction_id=%s", m.apiURL, name, txID)
	resp, err := m.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	frontendExists := resp.StatusCode == http.StatusOK

	if frontendExists {
		// Frontend exists, verify and update bind if needed
		klog.V(4).Infof("Frontend %s already exists, checking bind configuration", name)
		
		// Check the bind configuration
		if err := m.ensureBind(txID, name, port); err != nil {
			return fmt.Errorf("failed to ensure bind for existing frontend: %w", err)
		}
		
		// Update default backend if needed
		frontend := map[string]interface{}{
			"name":            name,
			"mode":            "tcp",
			"default_backend": backendName,
		}

		body, err := json.Marshal(frontend)
		if err != nil {
			return err
		}

		url = fmt.Sprintf("%s/services/haproxy/configuration/frontends/%s?transaction_id=%s", m.apiURL, name, txID)
		req, err := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := m.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("failed to update frontend: %s (status: %d)", string(respBody), resp.StatusCode)
		}

		klog.V(4).Infof("Updated frontend %s", name)
		return nil
	}

	// Create frontend
	frontend := map[string]interface{}{
		"name":            name,
		"mode":            "tcp",
		"default_backend": backendName,
	}

	body, err := json.Marshal(frontend)
	if err != nil {
		return err
	}

	url = fmt.Sprintf("%s/services/haproxy/configuration/frontends?transaction_id=%s", m.apiURL, txID)
	resp, err = m.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create frontend: %s (status: %d)", string(respBody), resp.StatusCode)
	}

	// Add bind to the frontend
	if err := m.ensureBind(txID, name, port); err != nil {
		return fmt.Errorf("failed to create bind: %w", err)
	}

	klog.V(4).Infof("Created frontend %s on port %d", name, port)
	return nil
}

// ensureBind ensures a bind exists on the frontend with the correct port
func (m *Manager) ensureBind(txID, frontendName string, port int) error {
	// Get existing binds for this frontend
	url := fmt.Sprintf("%s/services/haproxy/configuration/binds?frontend=%s&transaction_id=%s", m.apiURL, frontendName, txID)
	resp, err := m.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var existingBinds []map[string]interface{}
	if resp.StatusCode == http.StatusOK {
		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}
		if data, ok := result["data"].([]interface{}); ok {
			for _, item := range data {
				if bind, ok := item.(map[string]interface{}); ok {
					existingBinds = append(existingBinds, bind)
				}
			}
		}
	}

	// Check if bind_1 exists and has correct port
	var bind1 map[string]interface{}
	for _, bind := range existingBinds {
		if name, ok := bind["name"].(string); ok && name == "bind_1" {
			bind1 = bind
			break
		}
	}

	if bind1 != nil {
		// Check if port is correct
		currentPort := 0
		if p, ok := bind1["port"].(float64); ok {
			currentPort = int(p)
		} else if p, ok := bind1["port"].(int); ok {
			currentPort = p
		}

		if currentPort == port {
			klog.V(4).Infof("Bind for frontend %s already has correct port %d", frontendName, port)
			return nil
		}

		// Port is wrong, update it
		klog.V(4).Infof("Updating bind port for frontend %s from %d to %d", frontendName, currentPort, port)
		
		bind := map[string]interface{}{
			"name":    "bind_1",
			"address": "*",
			"port":    port,
		}

		body, err := json.Marshal(bind)
		if err != nil {
			return err
		}

		url = fmt.Sprintf("%s/services/haproxy/configuration/binds/bind_1?frontend=%s&transaction_id=%s", m.apiURL, frontendName, txID)
		req, err := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := m.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("failed to update bind: %s (status: %d)", string(respBody), resp.StatusCode)
		}

		klog.V(4).Infof("Updated bind for frontend %s to port %d", frontendName, port)
		return nil
	}

	// Bind doesn't exist, create it
	bind := map[string]interface{}{
		"name":    "bind_1",
		"address": "*",
		"port":    port,
	}

	body, err := json.Marshal(bind)
	if err != nil {
		return err
	}

	url = fmt.Sprintf("%s/services/haproxy/configuration/binds?frontend=%s&transaction_id=%s", m.apiURL, frontendName, txID)
	resp, err = m.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create bind: %s (status: %d)", string(respBody), resp.StatusCode)
	}

	klog.V(4).Infof("Created bind for frontend %s on port %d", frontendName, port)
	return nil
}

func (m *Manager) deleteFrontend(txID, name string) error {
	url := fmt.Sprintf("%s/services/haproxy/configuration/frontends/%s?transaction_id=%s", m.apiURL, name, txID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete frontend: %s (status: %d)", string(body), resp.StatusCode)
	}

	klog.V(4).Infof("Deleted frontend %s", name)
	return nil
}

// Backend management

func (m *Manager) ensureBackend(txID, name string) error {
	// Check if backend exists
	url := fmt.Sprintf("%s/services/haproxy/configuration/backends/%s?transaction_id=%s", m.apiURL, name, txID)
	resp, err := m.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// Backend exists
		klog.V(4).Infof("Backend %s already exists", name)
		return nil
	}

	// Create backend
	backend := map[string]interface{}{
		"name":    name,
		"mode":    "tcp",
		"balance": map[string]interface{}{"algorithm": "roundrobin"},
	}

	body, err := json.Marshal(backend)
	if err != nil {
		return err
	}

	url = fmt.Sprintf("%s/services/haproxy/configuration/backends?transaction_id=%s", m.apiURL, txID)
	resp, err = m.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create backend: %s (status: %d)", string(respBody), resp.StatusCode)
	}

	klog.V(4).Infof("Created backend %s", name)
	return nil
}

func (m *Manager) deleteBackend(txID, name string) error {
	url := fmt.Sprintf("%s/services/haproxy/configuration/backends/%s?transaction_id=%s", m.apiURL, name, txID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete backend: %s (status: %d)", string(body), resp.StatusCode)
	}

	klog.V(4).Infof("Deleted backend %s", name)
	return nil
}

// Server management

func (m *Manager) updateBackendServers(txID, backendName string, backends []Backend) error {
	// Get existing servers
	url := fmt.Sprintf("%s/services/haproxy/configuration/servers?backend=%s&transaction_id=%s", m.apiURL, backendName, txID)
	resp, err := m.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var existingServers []map[string]interface{}
	if resp.StatusCode == http.StatusOK {
		var result map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return err
		}
		if data, ok := result["data"].([]interface{}); ok {
			for _, item := range data {
				if server, ok := item.(map[string]interface{}); ok {
					existingServers = append(existingServers, server)
				}
			}
		}
	}

	// Build desired server set
	desiredServers := make(map[string]Backend)
	for _, backend := range backends {
		serverName := sanitizeName(backend.Name)
		desiredServers[serverName] = backend
	}

	// Remove servers that shouldn't exist
	for _, existing := range existingServers {
		serverName, ok := existing["name"].(string)
		if !ok {
			continue
		}
		if _, shouldExist := desiredServers[serverName]; !shouldExist {
			m.deleteServer(txID, backendName, serverName)
		}
	}

	// Add or update desired servers
	for serverName, backend := range desiredServers {
		// Check if server exists
		found := false
		for _, existing := range existingServers {
			if name, ok := existing["name"].(string); ok && name == serverName {
				found = true
				break
			}
		}

		if found {
			// Update existing server
			if err := m.updateServer(txID, backendName, serverName, backend); err != nil {
				klog.Errorf("Failed to update server %s: %v", serverName, err)
			}
		} else {
			// Create new server
			if err := m.createServer(txID, backendName, serverName, backend); err != nil {
				klog.Errorf("Failed to create server %s: %v", serverName, err)
			}
		}
	}

	return nil
}

func (m *Manager) createServer(txID, backendName, serverName string, backend Backend) error {
	server := map[string]interface{}{
		"name":    serverName,
		"address": backend.Address,
		"port":    backend.Port,
		"check":   "disabled",
	}

	body, err := json.Marshal(server)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/services/haproxy/configuration/servers?backend=%s&transaction_id=%s", m.apiURL, backendName, txID)
	resp, err := m.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create server: %s (status: %d)", string(respBody), resp.StatusCode)
	}

	klog.V(4).Infof("Created server %s in backend %s at %s:%d", serverName, backendName, backend.Address, backend.Port)
	return nil
}

func (m *Manager) updateServer(txID, backendName, serverName string, backend Backend) error {
	server := map[string]interface{}{
		"name":    serverName,
		"address": backend.Address,
		"port":    backend.Port,
		"check":   "disabled",
	}

	body, err := json.Marshal(server)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/services/haproxy/configuration/servers/%s?backend=%s&transaction_id=%s", m.apiURL, serverName, backendName, txID)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update server: %s (status: %d)", string(respBody), resp.StatusCode)
	}

	klog.V(4).Infof("Updated server %s in backend %s to %s:%d", serverName, backendName, backend.Address, backend.Port)
	return nil
}

func (m *Manager) deleteServer(txID, backendName, serverName string) error {
	url := fmt.Sprintf("%s/services/haproxy/configuration/servers/%s?backend=%s&transaction_id=%s", m.apiURL, serverName, backendName, txID)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete server: %s (status: %d)", string(body), resp.StatusCode)
	}

	klog.V(4).Infof("Deleted server %s from backend %s", serverName, backendName)
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
