package handlers

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestCheatCheckOfInFirstWrite: several cheat checks launched at once, with
// another writer loading and saving every evaluation meanwhile (a status
// refresh), each keep cheat_check_of: it is in the write that creates them.
func TestCheatCheckOfInFirstWrite(t *testing.T) {
	root := t.TempDir()
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode}
	st := evalstore.Open(root)
	const n = 8
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	stop := make(chan struct{})
	refreshed := make(chan struct{})
	go func() { // the refresher: load and save every stored evaluation until the launches end
		defer close(refreshed)
		for {
			select {
			case <-stop:
				return
			default:
			}
			list, _ := st.List()
			for _, e := range list {
				_ = st.Save(e)
			}
		}
	}()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			parent := fmt.Sprintf("ev_parent_%d", i)
			h := &evaluationsHandler{env: env, now: time.Now, prepare: func(e *schemas.Evaluation) { e.CheatCheckOf = parent }}
			ev, err := h.launch(context.Background(), context.Background(), nil, schemas.EvaluationSpec{Name: parent + " (cheat check)"}, "u", "")
			if err == nil {
				ids[i] = ev.ID
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	close(stop)
	<-refreshed
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("launch %d: %v", i, errs[i])
		}
		e, err := st.Load(ids[i])
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("ev_parent_%d", i); e.CheatCheckOf != want {
			t.Fatalf("evaluation %s: cheat_check_of %q, want %q", e.ID, e.CheatCheckOf, want)
		}
	}
}
