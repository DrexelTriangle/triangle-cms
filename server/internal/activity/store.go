package activity

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrStoreUnavailable = errors.New("activity store unavailable")

type Entry struct {
	ID         string         `json:"id"`
	Timestamp  time.Time      `json:"timestamp"`
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Kind       string         `json:"kind"`
	Action     string         `json:"action,omitempty"`
	ActorID    int64          `json:"actor_id,omitempty"`
	ActorName  string         `json:"actor_name,omitempty"`
	ActorRole  string         `json:"actor_role,omitempty"`
	Target     string         `json:"target,omitempty"`
	Method     string         `json:"method,omitempty"`
	Path       string         `json:"path,omitempty"`
	Status     int            `json:"status,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type Query struct {
	Kind  string
	Limit int
}

type ListResult struct {
	Entries    []Entry
	TotalCount int
}

type Store interface {
	Write(context.Context, Entry) error
	List(context.Context, Query) (ListResult, error)
	Close() error
}

var (
	defaultStore   Store
	defaultStoreMu sync.RWMutex
)

func SetDefaultStore(store Store) {
	defaultStoreMu.Lock()
	defer defaultStoreMu.Unlock()
	defaultStore = store
}

func DefaultStore() Store {
	defaultStoreMu.RLock()
	defer defaultStoreMu.RUnlock()
	return defaultStore
}

func ListDefault(ctx context.Context, query Query) (ListResult, error) {
	store := DefaultStore()
	if store == nil {
		return ListResult{}, ErrStoreUnavailable
	}
	return store.List(ctx, query)
}
