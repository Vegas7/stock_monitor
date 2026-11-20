package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Vegas7/stock_monitor/internal/check"
	"github.com/Vegas7/stock_monitor/internal/misaka"
)

func TestOpenMissingIsEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.Items) != 0 || len(snap.Orders) != 0 {
		t.Fatalf("%+v", snap)
	}
}

func TestUpdatePersists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(p)
	err := s.Update(func(st *State) {
		st.Items["x"] = &ItemState{Stock: check.InStock, ConsecutiveFails: 2}
		st.UsedArmIDs = append(st.UsedArmIDs, "arm1")
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	snap := s2.Snapshot()
	if snap.Items["x"].Stock != check.InStock || !snap.ArmUsed("arm1") || snap.ArmUsed("arm2") {
		t.Fatalf("%+v", snap)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	s.Update(func(st *State) { st.Items["x"] = &ItemState{Detail: "a"} })
	snap := s.Snapshot()
	snap.Items["x"].Detail = "changed"
	snap.Items["y"] = &ItemState{}
	again := s.Snapshot()
	if again.Items["x"].Detail != "a" || again.Items["y"] != nil {
		t.Fatal("snapshot shares memory with store")
	}
}

func TestOpenCorruptFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(p, []byte("{nope"), 0o600)
	if _, err := Open(p); err == nil {
		t.Fatal("expected error")
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Update(func(st *State) {
				if st.Items["c"] == nil {
					st.Items["c"] = &ItemState{}
				}
				st.Items["c"].ConsecutiveFails++
			})
		}()
	}
	wg.Wait()
	if n := s.Snapshot().Items["c"].ConsecutiveFails; n != 50 {
		t.Fatalf("got %d", n)
	}
}

func TestTokenStore(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	var ts misaka.TokenStore = s
	if err := ts.SaveTokens(misaka.Tokens{Access: "a", Refresh: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := ts.LoadTokens(); got.Access != "a" || got.Refresh != "r" {
		t.Fatalf("%+v", got)
	}
}
