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

// Configurer defines the interface for HAProxy configuration management.
// This interface allows for mocking in tests.
type Configurer interface {
	// Configure applies configuration for a specific owner (VirtualService).
	// The ownerKey is typically "namespace/name" of the VirtualService.
	// configs contains the listener configurations to apply.
	Configure(ownerKey string, configs []ListenerConfig) error

	// Remove removes all configuration for a specific owner.
	Remove(ownerKey string) error
}

// Ensure Manager implements Configurer
var _ Configurer = (*Manager)(nil)
