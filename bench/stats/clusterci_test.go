package stats

import "testing"

func TestClusterBootstrapCI(t *testing.T) {
	if _, _, ok := ClusterBootstrapCI([]Cluster{{Sum: 1, N: 2}}, 0.95, 100, 1); ok {
		t.Fatal("one cluster has no between-task spread")
	}
	if _, _, ok := ClusterBootstrapCI(nil, 0.95, 100, 1); ok {
		t.Fatal("no clusters")
	}
	cs := []Cluster{{Sum: 2, N: 2}, {Sum: 0, N: 2}, {Sum: 1, N: 2}, {Sum: 2, N: 2}}
	lo, hi, ok := ClusterBootstrapCI(cs, 0.95, 2000, 7)
	if !ok || lo < 0 || hi > 1 || lo >= hi || lo > 0.625 || hi < 0.625 {
		t.Fatalf("interval [%v, %v] ok %v must hold the mean 0.625", lo, hi, ok)
	}
	lo2, hi2, _ := ClusterBootstrapCI(cs, 0.95, 2000, 7)
	if lo != lo2 || hi != hi2 {
		t.Fatal("same seed must give the same interval")
	}
	// Every task identical: no spread.
	lo, hi, _ = ClusterBootstrapCI([]Cluster{{Sum: 1, N: 1}, {Sum: 1, N: 1}}, 0.95, 500, 1)
	if lo != 1 || hi != 1 {
		t.Fatalf("all-pass interval [%v, %v]", lo, hi)
	}
}
