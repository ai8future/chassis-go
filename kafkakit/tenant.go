package kafkakit

import "sync"

// TenantFilter decides whether an event should be delivered based on tenant ID.
// Events from the own tenant, from "shared", or from explicitly granted tenants
// are delivered. All others are filtered out.
type TenantFilter struct {
	ownTenant string
	mu        sync.RWMutex
	grants    map[string]bool
}

// NewTenantFilter creates a TenantFilter for the given tenant ID.
func NewTenantFilter(ownTenant string) *TenantFilter {
	return &TenantFilter{
		ownTenant: ownTenant,
		grants:    make(map[string]bool),
	}
}

// ShouldDeliver returns true if the event should be delivered to this tenant.
// Delivers if:
//   - eventTenantID is empty (system events)
//   - eventTenantID matches own tenant
//   - eventTenantID is "shared"
//   - eventTenantID is in the grants map
func (f *TenantFilter) ShouldDeliver(eventTenantID string) bool {
	if eventTenantID == "" {
		return true
	}
	if eventTenantID == f.ownTenant {
		return true
	}
	if eventTenantID == "shared" {
		return true
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.grants[eventTenantID]
}

// Grant adds a tenant ID to the allowed grants.
func (f *TenantFilter) Grant(tenantID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants[tenantID] = true
}

// Revoke removes a tenant ID from the allowed grants.
func (f *TenantFilter) Revoke(tenantID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.grants, tenantID)
}
