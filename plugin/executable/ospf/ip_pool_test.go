package ospf

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockRouter is a test double implementing the minimal router API used by IpPool.
type mockRouter struct{}

func (m *mockRouter) AnnounceASBRRoute([]net.IPNet) {}
func (m *mockRouter) RevokeASBRRoute([]net.IPNet)   {}

func TestSaveAndLoadIpPool(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "pool.json")

	router := &mockRouter{}
	logger := zap.NewNop()
	// Use long TTL so entry is still valid when loading
	p := NewIpPool(3600, router, logger, path, 0)

	ip := net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(32, 32)}
	p.AddIps("example.com", []net.IPNet{ip})

	if err := p.SaveToFile(path); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// ensure file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file exists: %v", err)
	}

	// load into new pool with mock router
	router2 := &mockRouter{}
	p2 := NewIpPool(3600, router2, logger, path, 0)
	if err := p2.LoadFromFile(path); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	// verify entry present
	p2.mux.Lock()
	defer p2.mux.Unlock()
	m, ok := p2.pool["example.com"]
	if !ok {
		t.Fatalf("domain not found after load")
	}
	if _, ok := m[ip.String()]; !ok {
		t.Fatalf("ip not found after load")
	}
}

func TestLoadIgnoresExpiredEntries(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "pool_exp.json")

	now := time.Now()
	entries := []map[string]interface{}{
		{"domain": "old.com", "ip": "192.0.2.1/32", "expiration": now.Add(-time.Hour).UnixNano()},
		{"domain": "new.com", "ip": "192.0.2.2/32", "expiration": now.Add(time.Hour).UnixNano()},
	}

	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	router := &mockRouter{}
	p := NewIpPool(3600, router, zap.NewNop(), path, 0)
	if err := p.LoadFromFile(path); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	p.mux.Lock()
	defer p.mux.Unlock()
	if _, ok := p.pool["old.com"]; ok {
		t.Fatalf("expired domain should be ignored")
	}
	if m, ok := p.pool["new.com"]; !ok {
		t.Fatalf("new domain missing")
	} else {
		if _, ok := m["192.0.2.2/32"]; !ok {
			t.Fatalf("new ip missing")
		}
	}
}
