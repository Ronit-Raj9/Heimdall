package redteam

import "testing"

func TestCatalogLabels(t *testing.T) {
	if len(Categories()) != 5 || len(Strategies()) != 3 || len(Channels()) != 4 {
		t.Fatal("catalog sizes changed")
	}
	if CategoryLabel(CategoryPolicyBypass) != "Policy bypass" || StrategyLabel(StrategyTAP) != "Tree of attacks" || ChannelLabel("x") != "x" {
		t.Fatal("labels")
	}
	for _, c := range Categories() {
		for _, ch := range c.Channels {
			if !knownChannel(ch) {
				t.Errorf("category %s offers unknown channel %s", c.ID, ch)
			}
		}
	}
}
