package proxy

import (
	"testing"
)

func TestProxyPoolRotation(t *testing.T) {
	pool := NewPool(nil, true, "round-robin")

	_, err := pool.AddNode("http://127.0.0.1:8080", "Node 1")
	if err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}
	_, err = pool.AddNode("socks5://127.0.0.1:1080", "Node 2")
	if err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}

	nodes := pool.ListNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	// Pick next (round robin)
	n1, u1, err := pool.PickNext()
	if err != nil || u1 == nil {
		t.Fatalf("PickNext failed: %v", err)
	}
	if n1.Label != "Node 2" && n1.Label != "Node 1" {
		t.Errorf("unexpected node: %s", n1.Label)
	}

	n2, u2, err := pool.PickNext()
	if err != nil || u2 == nil {
		t.Fatalf("PickNext failed: %v", err)
	}
	if n1.ID == n2.ID && len(nodes) > 1 {
		t.Errorf("expected round robin rotation, got same node")
	}
}

func TestProxyBatchAndGroups(t *testing.T) {
	pool := NewPool(nil, true, "round-robin")

	batch := []string{
		"http://user:pass@10.0.0.1:8080",
		"http://10.0.0.2:8080",
		"socks5://10.0.0.3:1080",
	}

	count, err := pool.AddBatch(batch, "dahl_proxies")
	if err != nil || count != 3 {
		t.Fatalf("AddBatch failed: count=%d, err=%v", count, err)
	}

	dahlNodes := pool.ListNodesByGroup("dahl_proxies")
	if len(dahlNodes) != 3 {
		t.Fatalf("expected 3 dahl nodes, got %d", len(dahlNodes))
	}

	groups := pool.ListGroups()
	if len(groups) == 0 || groups[0] != "dahl_proxies" {
		t.Fatalf("expected group dahl_proxies, got %v", groups)
	}

	// Pick for group
	node, u, err := pool.PickNextForGroup("dahl_proxies")
	if err != nil || u == nil || node == nil {
		t.Fatalf("PickNextForGroup failed: %v", err)
	}
	if node.GroupName != "dahl_proxies" {
		t.Errorf("expected group dahl_proxies, got %s", node.GroupName)
	}
}

func TestProxyPoolToggleObedience(t *testing.T) {
	pool := NewPool(nil, true, "round-robin")
	_, err := pool.AddNode("http://127.0.0.1:8080", "Node 1")
	if err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}
	_, err = pool.AddNodeWithGroup("http://127.0.0.1:8081", "Node G2", "group_b")
	if err != nil {
		t.Fatalf("AddNodeWithGroup failed: %v", err)
	}

	// 1. When enabled, IsEnabled is true and PickNext returns a proxy
	if !pool.IsEnabled() {
		t.Fatalf("expected pool to be enabled")
	}
	n, u, err := pool.PickNext()
	if err != nil || u == nil || n == nil {
		t.Fatalf("expected active proxy when enabled, got nil")
	}

	// 2. Toggle global proxy pool OFF
	pool.SetEnabled(false)
	if pool.IsEnabled() {
		t.Fatalf("expected pool to be disabled after SetEnabled(false)")
	}

	// When disabled, PickNext must return nil (direct connection fallback)
	nOff, uOff, err := pool.PickNext()
	if err != nil || uOff != nil || nOff != nil {
		t.Fatalf("expected nil proxy URL (direct fallback) when pool disabled, got: %v", uOff)
	}

	nGroupOff, uGroupOff, err := pool.PickNextForGroup("group_b")
	if err != nil || uGroupOff != nil || nGroupOff != nil {
		t.Fatalf("expected nil proxy URL for group when pool disabled, got: %v", uGroupOff)
	}

	// 3. Toggle global proxy pool back ON
	pool.SetEnabled(true)
	if !pool.IsEnabled() {
		t.Fatalf("expected pool to be enabled after SetEnabled(true)")
	}

	// 4. Test Group Toggle: toggle group_b OFF
	err = pool.ToggleGroup("group_b", false)
	if err != nil {
		t.Fatalf("ToggleGroup failed: %v", err)
	}

	// group_b must now fallback to direct (nil)
	nB, uB, err := pool.PickNextForGroup("group_b")
	if err != nil || uB != nil || nB != nil {
		t.Fatalf("expected nil proxy URL for disabled group_b, got: %v", uB)
	}

	// default group must still work
	nDef, uDef, err := pool.PickNextForGroup("default")
	if err != nil || uDef == nil || nDef == nil {
		t.Fatalf("expected default group to remain active, got: %v", uDef)
	}
}
