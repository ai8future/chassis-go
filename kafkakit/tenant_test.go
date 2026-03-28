package kafkakit

import (
	"sync"
	"testing"
)

func TestTenantFilter_RevokeStopsDelivery(t *testing.T) {
	f := NewTenantFilter("acme")
	f.Grant("partner")
	f.Revoke("partner")

	if f.ShouldDeliver("partner") {
		t.Error("revoked tenant should no longer be delivered")
	}
}

func TestTenantFilter_RevokeNonexistent(t *testing.T) {
	f := NewTenantFilter("acme")
	// Should not panic.
	f.Revoke("never-granted")
}

func TestTenantFilter_GrantIdempotent(t *testing.T) {
	f := NewTenantFilter("acme")
	f.Grant("partner")
	f.Grant("partner") // duplicate

	if !f.ShouldDeliver("partner") {
		t.Error("double grant should still deliver")
	}
	f.Revoke("partner")
	if f.ShouldDeliver("partner") {
		t.Error("single revoke should remove even after double grant")
	}
}

func TestTenantFilter_MultipleGrants(t *testing.T) {
	f := NewTenantFilter("acme")
	f.Grant("alpha")
	f.Grant("beta")
	f.Grant("gamma")

	for _, tenant := range []string{"alpha", "beta", "gamma"} {
		if !f.ShouldDeliver(tenant) {
			t.Errorf("should deliver to granted tenant %q", tenant)
		}
	}

	f.Revoke("beta")
	if f.ShouldDeliver("beta") {
		t.Error("beta should be blocked after revoke")
	}
	if !f.ShouldDeliver("alpha") {
		t.Error("alpha should still be delivered")
	}
}

func TestTenantFilter_ConcurrentAccess(t *testing.T) {
	f := NewTenantFilter("acme")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		tenant := string(rune('A' + i%26))
		go func() {
			defer wg.Done()
			f.Grant(tenant)
		}()
		go func() {
			defer wg.Done()
			_ = f.ShouldDeliver(tenant)
		}()
	}
	wg.Wait()
}
