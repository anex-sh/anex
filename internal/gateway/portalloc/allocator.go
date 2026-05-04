package portalloc

import (
	"fmt"
	"sync"
)

// Allocator manages port allocation for VirtualServices
type Allocator struct {
	mutex      sync.RWMutex
	rangeStart int
	rangeEnd   int
	allocated  map[int]string   // port -> owner key
	ownerPorts map[string][]int // owner key -> allocated ports
}

// NewAllocator creates a new port allocator
func NewAllocator(rangeStart, rangeEnd int) *Allocator {
	return &Allocator{
		rangeStart: rangeStart,
		rangeEnd:   rangeEnd,
		allocated:  make(map[int]string),
		ownerPorts: make(map[string][]int),
	}
}

// Allocate allocates a port for the given owner
func (a *Allocator) Allocate(ownerKey string) (int, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	// Find first available port
	for port := a.rangeStart; port <= a.rangeEnd; port++ {
		if _, exists := a.allocated[port]; !exists {
			// Port is available
			a.allocated[port] = ownerKey
			a.ownerPorts[ownerKey] = append(a.ownerPorts[ownerKey], port)
			return port, nil
		}
	}

	return 0, fmt.Errorf("no available ports in range %d-%d", a.rangeStart, a.rangeEnd)
}

// Release releases a specific port
func (a *Allocator) Release(ownerKey string, port int) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if owner, exists := a.allocated[port]; exists && owner == ownerKey {
		delete(a.allocated, port)

		// Remove from owner's port list
		if ports, ok := a.ownerPorts[ownerKey]; ok {
			newPorts := []int{}
			for _, p := range ports {
				if p != port {
					newPorts = append(newPorts, p)
				}
			}
			if len(newPorts) > 0 {
				a.ownerPorts[ownerKey] = newPorts
			} else {
				delete(a.ownerPorts, ownerKey)
			}
		}
	}
}

// ReleaseAll releases all ports for a given owner
func (a *Allocator) ReleaseAll(ownerKey string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if ports, exists := a.ownerPorts[ownerKey]; exists {
		for _, port := range ports {
			delete(a.allocated, port)
		}
		delete(a.ownerPorts, ownerKey)
	}
}

// IsAllocated checks if a port is allocated
func (a *Allocator) IsAllocated(port int) bool {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	_, exists := a.allocated[port]
	return exists
}

// GetOwner returns the owner of a port, or empty string if not allocated
func (a *Allocator) GetOwner(port int) string {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	return a.allocated[port]
}

// GetAllocatedPorts returns all ports allocated to an owner
func (a *Allocator) GetAllocatedPorts(ownerKey string) []int {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	if ports, exists := a.ownerPorts[ownerKey]; exists {
		// Return a copy
		result := make([]int, len(ports))
		copy(result, ports)
		return result
	}
	return []int{}
}

// Stats returns allocation statistics
func (a *Allocator) Stats() (allocated, available, total int) {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	total = a.rangeEnd - a.rangeStart + 1
	allocated = len(a.allocated)
	available = total - allocated
	return
}
