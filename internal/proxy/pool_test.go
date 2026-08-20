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
