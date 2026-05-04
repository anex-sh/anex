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
