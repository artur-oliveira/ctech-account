package dataexport

import (
	"context"
	"sync"
)

// MemoryRepository is the in-process Repository the service and handler tests
// use. It mirrors the conditional semantics of the DynamoDB one.
type MemoryRepository struct {
	mu sync.Mutex
	m  map[string]Export
}

func NewMemoryRepository() *MemoryRepository { return &MemoryRepository{m: map[string]Export{}} }

func clone(e Export) *Export {
	e.Parts = append([]Part(nil), e.Parts...)
	return &e
}

func (r *MemoryRepository) Get(_ context.Context, userID string) (*Export, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[userID]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(e), nil
}

func (r *MemoryRepository) Save(_ context.Context, e *Export, prev string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.m[e.UserID]; (ok && cur.ID != prev) || (!ok && prev != "") {
		return ErrBuilding
	}
	r.m[e.UserID] = *clone(*e)
	return nil
}

func (r *MemoryRepository) current(userID, exportID string) (Export, error) {
	cur, ok := r.m[userID]
	if !ok || cur.ID != exportID {
		return Export{}, ErrStale
	}
	return cur, nil
}

func (r *MemoryRepository) SetPart(_ context.Context, userID, exportID string, p Part) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, err := r.current(userID, exportID)
	if err != nil {
		return err
	}
	cur.Parts = append([]Part(nil), cur.Parts...)
	set := false
	for i := range cur.Parts {
		if cur.Parts[i].Service == p.Service {
			cur.Parts[i], set = p, true
		}
	}
	if !set {
		cur.Parts = append(cur.Parts, p)
	}
	r.m[userID] = cur
	return nil
}

func (r *MemoryRepository) Finish(_ context.Context, userID, exportID string, f Finish) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, err := r.current(userID, exportID)
	if err != nil {
		return err
	}
	cur.State, cur.Error = f.State, f.Error
	if !f.ReadyAt.IsZero() {
		cur.ReadyAt, cur.ExpiresAt = f.ReadyAt, f.ExpiresAt
	}
	if f.DropParts {
		cur.Parts = nil
	}
	r.m[userID] = cur
	return nil
}

func (r *MemoryRepository) MarkNotified(_ context.Context, userID, exportID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, err := r.current(userID, exportID)
	if err != nil || cur.Notified {
		return false, nil
	}
	cur.Notified = true
	r.m[userID] = cur
	return true, nil
}

func (r *MemoryRepository) Delete(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, userID)
	return nil
}
