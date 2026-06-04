package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dgraph-io/badger/v4"
)

const (
	defaultListLimit = 100
	maxListLimit     = 500
	logKeyPrefix     = "logs/"
)

type BadgerStore struct {
	db  *badger.DB
	seq uint64
}

func OpenBadgerStore(path string) (*BadgerStore, error) {
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		return nil, fmt.Errorf("badger path is required")
	}
	if err := os.MkdirAll(filepath.Clean(cleanPath), 0o755); err != nil {
		return nil, fmt.Errorf("create badger directory: %w", err)
	}

	opts := badger.DefaultOptions(cleanPath)
	opts.Logger = nil

	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open badger: %w", err)
	}

	return &BadgerStore{db: db}, nil
}

func (s *BadgerStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *BadgerStore) Write(ctx context.Context, entry Entry) error {
	if s == nil || s.db == nil {
		return ErrStoreUnavailable
	}

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	} else {
		entry.Timestamp = entry.Timestamp.UTC()
	}
	if entry.ID == "" {
		entry.ID = s.nextID(entry.Timestamp)
	}
	if entry.Kind == "" {
		if entry.Action != "" {
			entry.Kind = "activity"
		} else {
			entry.Kind = "log"
		}
	}

	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal activity entry: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		return txn.Set([]byte(s.keyFor(entry.Timestamp, entry.ID)), payload)
	})
}

func (s *BadgerStore) List(ctx context.Context, query Query) (ListResult, error) {
	if s == nil || s.db == nil {
		return ListResult{}, ErrStoreUnavailable
	}

	limit := query.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	result := ListResult{
		Entries: make([]Entry, 0, limit),
	}
	prefix := []byte(logKeyPrefix)
	seekKey := append(append([]byte{}, prefix...), 0xFF)

	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		opts.Reverse = true

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek(seekKey); it.ValidForPrefix(prefix); it.Next() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			item := it.Item()
			var entry Entry
			err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, &entry)
			})
			if err != nil {
				return fmt.Errorf("decode activity entry: %w", err)
			}
			if query.Kind != "" && entry.Kind != query.Kind {
				continue
			}
			result.TotalCount++
			if len(result.Entries) < limit {
				result.Entries = append(result.Entries, entry)
			}
		}
		return nil
	})
	if err != nil {
		return ListResult{}, err
	}

	return result, nil
}

func (s *BadgerStore) keyFor(ts time.Time, id string) string {
	return fmt.Sprintf("%s%020d/%s", logKeyPrefix, ts.UTC().UnixNano(), id)
}

func (s *BadgerStore) nextID(ts time.Time) string {
	seq := atomic.AddUint64(&s.seq, 1)
	return fmt.Sprintf("%020d-%020d", ts.UTC().UnixNano(), seq)
}

func isLogKey(key []byte) bool {
	return bytes.HasPrefix(key, []byte(logKeyPrefix))
}
