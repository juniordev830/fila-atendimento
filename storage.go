package main

import (
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
)

// SaveSnapshotFile writes a recoverable point-in-time backup. A temporary file
// is flushed and renamed atomically, so readers never observe partial JSON.
// This is a manual checkpoint, not transactionally durable live storage.
func (q *Queue) SaveSnapshotFile(path string) (err error) {
    if path == "" {
        return errors.New("caminho de snapshot obrigatorio")
    }
    snapshot, err := q.ExportSnapshot()
    if err != nil {
        return fmt.Errorf("serializar snapshot: %w", err)
    }
    if len(snapshot) > 5*1024*1024 {
        return errors.New("snapshot excede 5 MiB")
    }
    directory := filepath.Dir(path)
    tmp, err := os.CreateTemp(directory, ".fila-snapshot-*")
    if err != nil {
        return fmt.Errorf("criar arquivo temporario: %w", err)
    }
    temporaryPath := tmp.Name()
    defer func() {
        if err != nil {
            _ = os.Remove(temporaryPath)
        }
    }()
    if err = tmp.Chmod(0600); err != nil {
        _ = tmp.Close()
        return fmt.Errorf("proteger snapshot: %w", err)
    }
    if _, err = tmp.Write(snapshot); err != nil {
        _ = tmp.Close()
        return fmt.Errorf("escrever snapshot: %w", err)
    }
    if err = tmp.Sync(); err != nil {
        _ = tmp.Close()
        return fmt.Errorf("sincronizar snapshot: %w", err)
    }
    if err = tmp.Close(); err != nil {
        return fmt.Errorf("fechar snapshot: %w", err)
    }
    if err = os.Rename(temporaryPath, path); err != nil {
        return fmt.Errorf("substituir snapshot: %w", err)
    }
    // Best-effort directory fsync for stronger crash recovery on Unix.
    if dir, openErr := os.Open(directory); openErr == nil {
        _ = dir.Sync()
        _ = dir.Close()
    }
    return nil
}

// LoadSnapshotFile validates a bounded snapshot before constructing a queue.
// A missing file is an error; callers must explicitly choose a fresh queue.
func LoadSnapshotFile(path string) (*Queue, error) {
    if path == "" {
        return nil, errors.New("caminho de snapshot obrigatorio")
    }
    file, err := os.Open(path)
    if err != nil {
        return nil, fmt.Errorf("abrir snapshot: %w", err)
    }
    defer file.Close()
    data, err := io.ReadAll(io.LimitReader(file, 5*1024*1024+1))
    if err != nil {
        return nil, fmt.Errorf("ler snapshot: %w", err)
    }
    queue, err := RestoreQueue(data)
    if err != nil {
        return nil, fmt.Errorf("validar snapshot: %w", err)
    }
    return queue, nil
}
