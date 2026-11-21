package reportcard

import (
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/bench/stats"
)

// Ten tasks run ten times each: five always pass, five always fail. Over
// runs that looks like 100 independent coin flips (a narrow interval); over
// tasks it is ten, so the clustered interval must be much wider.
func TestTaskCIClustersTrials(t *testing.T) {
	var runs []graded
	for i := range 10 {
		for range 10 {
			s := 0
			if i < 5 {
				s = 1
			}
			runs = append(runs, graded{task: fmt.Sprintf("t%d", i), condition: "clean", success: s})
		}
	}
	lo, hi := taskCI(clustersOf(runs, ""))
	cpLo, cpHi := stats.ClopperPearson(50, 100, Conf)
	if !(lo < 0.5 && hi > 0.5) {
		t.Fatalf("interval [%v, %v] misses the score", lo, hi)
	}
	if hi-lo <= (cpHi-cpLo)*1.5 {
		t.Errorf("clustered [%v, %v] not wider than runs-level [%v, %v]", lo, hi, cpLo, cpHi)
	}
	lo2, hi2 := taskCI(clustersOf(runs, ""))
	if lo2 != lo || hi2 != hi {
		t.Errorf("not reproducible: [%v, %v] then [%v, %v]", lo, hi, lo2, hi2)
	}
	m := metricsOf(runs)
	if m.Score != 0.5 || m.CILow != round(lo) || m.CIHigh != round(hi) {
		t.Errorf("metrics %+v", m)
	}
}

// One task: no spread between tasks to resample; the exact interval over
// runs is used.
func TestTaskCISingleTask(t *testing.T) {
	cs := []cluster{{key: "t", succ: 3, runs: 4}}
	lo, hi := taskCI(cs)
	cpLo, cpHi := stats.ClopperPearson(3, 4, Conf)
	if lo != cpLo || hi != cpHi {
		t.Errorf("[%v, %v], want [%v, %v]", lo, hi, cpLo, cpHi)
	}
	if lo, hi := taskCI(nil); lo != 0 || hi != 0 {
		t.Errorf("empty [%v, %v]", lo, hi)
	}
}
