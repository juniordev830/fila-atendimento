package main

import (
    "bytes"
    "errors"
    "os"
    "path/filepath"
    "sync"
    "testing"
)

func TestAtomicSnapshotFileRoundTripAndPermissions(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "queue.json")
    q := NewQueue()
    for i := 0; i < 3; i++ { q.Issue() }
    if _, err := q.Call("A"); err != nil { t.Fatal(err) }
    if err := q.SaveSnapshotFile(path); err != nil { t.Fatal(err) }
    info, err := os.Stat(path)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0600 {
        t.Fatalf("snapshot must be private: %v", info.Mode().Perm())
    }
    loaded, err := LoadSnapshotFile(path)
    if err != nil { t.Fatal(err) }
    if _, err := loaded.Call("A"); !errors.Is(err, ErrDeskBusy) {
        t.Fatalf("active desk lost: %v", err)
    }
    if ticket, err := loaded.Call("B"); err != nil || ticket.ID != 2 {
        t.Fatalf("queue order lost: %+v %v", ticket, err)
    }
    if ticket := loaded.Issue(); ticket.ID != 4 {
        t.Fatalf("next ID lost: %+v", ticket)
    }
    if _, err := q.Resolve(1, "completed"); err != nil { t.Fatal(err) }
    if err := q.SaveSnapshotFile(path); err != nil { t.Fatal(err) }
    newer, err := LoadSnapshotFile(path)
    if err != nil { t.Fatal(err) }
    if newer.Snapshot()[0].Status != "completed" {
        t.Fatal("atomic replacement did not publish latest state")
    }
    entries, err := os.ReadDir(dir)
    if err != nil { t.Fatal(err) }
    if len(entries) != 1 || entries[0].Name() != "queue.json" {
        t.Fatalf("temporary files leaked: %+v", entries)
    }
}

func TestSnapshotFileFailureDoesNotOverwriteBackup(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "queue.json")
    q := NewQueue()
    q.Issue()
    if err := q.SaveSnapshotFile(path); err != nil { t.Fatal(err) }
    before, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    if err := q.SaveSnapshotFile(filepath.Join(dir, "missing", "queue.json")); err == nil {
        t.Fatal("missing directory must fail")
    }
    after, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(before, after) {
        t.Fatal("failed checkpoint modified previous backup")
    }
    if err := q.SaveSnapshotFile(""); err == nil {
        t.Fatal("empty path must fail")
    }
    if _, err := LoadSnapshotFile(""); err == nil {
        t.Fatal("empty path must fail")
    }
    if _, err := LoadSnapshotFile(filepath.Join(dir, "missing.json")); err == nil {
        t.Fatal("missing file must fail")
    }
}

func TestSnapshotFileRejectsCorruptionAndOversize(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "queue.json")
    if err := os.WriteFile(path, []byte(`{"version":1,"next_id":2,"tickets":[{"id":1,"status":"called"}]}`), 0600); err != nil {
        t.Fatal(err)
    }
    if _, err := LoadSnapshotFile(path); err == nil {
        t.Fatal("invalid active desk accepted")
    }
    if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 5*1024*1024+1), 0600); err != nil {
        t.Fatal(err)
    }
    if _, err := LoadSnapshotFile(path); err == nil {
        t.Fatal("oversized file accepted")
    }
}

func TestConcurrentSnapshotsRemainConsistent(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "queue.json")
    q := NewQueue()
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            q.Issue()
        }()
    }
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            if err := q.SaveSnapshotFile(path); err != nil {
                t.Errorf("concurrent checkpoint: %v", err)
            }
        }()
    }
    wg.Wait()
    // Multiple independent atomic writers may race, but each published
    // snapshot must remain structurally valid and have unique ticket IDs.
    loaded, err := LoadSnapshotFile(path)
    if err != nil { t.Fatal(err) }
    seen := map[int]bool{}
    for _, ticket := range loaded.Snapshot() {
        if seen[ticket.ID] { t.Fatalf("duplicate ID %d", ticket.ID) }
        seen[ticket.ID] = true
    }
    if len(seen) > 100 { t.Fatalf("invalid snapshot size: %d", len(seen)) }
}
