package activity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

type TeeHandler struct {
	handlers []slog.Handler
}

func NewTeeHandler(handlers ...slog.Handler) slog.Handler {
	filtered := make([]slog.Handler, 0, len(handlers))
	for _, handler := range handlers {
		if handler != nil {
			filtered = append(filtered, handler)
		}
	}
	return &TeeHandler{handlers: filtered}
}

func (h *TeeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *TeeHandler) Handle(ctx context.Context, record slog.Record) error {
	var errs []error
	for _, handler := range h.handlers {
		if !handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *TeeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}
	return &TeeHandler{handlers: handlers}
}

func (h *TeeHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return &TeeHandler{handlers: handlers}
}

type StoreHandler struct {
	store  Store
	level  slog.Leveler
	attrs  []slog.Attr
	groups []string
}

func NewStoreHandler(store Store, opts *slog.HandlerOptions) slog.Handler {
	var level slog.Leveler
	if opts != nil && opts.Level != nil {
		level = opts.Level
	}
	return &StoreHandler{
		store: store,
		level: level,
	}
}

func (h *StoreHandler) Enabled(_ context.Context, level slog.Level) bool {
	if h.level == nil {
		return true
	}
	return level >= h.level.Level()
}

func (h *StoreHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.store == nil {
		return ErrStoreUnavailable
	}

	attrs := map[string]any{}
	for _, attr := range h.attrs {
		addAttr(attrs, h.groups, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		addAttr(attrs, h.groups, attr)
		return true
	})

	entry := Entry{
		Timestamp:  logTime(record.Time),
		Level:      record.Level.String(),
		Message:    record.Message,
		Kind:       stringValue(attrs["kind"]),
		Action:     stringValue(attrs["action"]),
		ActorID:    int64Value(attrs["actor_id"]),
		ActorName:  stringValue(attrs["actor_name"]),
		ActorRole:  stringValue(attrs["actor_role"]),
		Target:     stringValue(attrs["target"]),
		Method:     stringValue(attrs["method"]),
		Path:       stringValue(attrs["path"]),
		Status:     intValue(attrs["status"]),
		Attributes: attrs,
	}
	if entry.Kind == "" {
		if entry.Action != "" {
			entry.Kind = "activity"
		} else {
			entry.Kind = "log"
		}
	}

	// Only audit events are persisted to the store. General log records still
	// reach stdout (and Loki) via the other tee'd handler; duplicating them in
	// the database would be unbounded write-amplification for data no reader
	// queries — /v1/activity only ever lists kind="activity".
	if entry.Kind != "activity" {
		return nil
	}

	return h.store.Write(ctx, entry)
}

func (h *StoreHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nextAttrs := append(slices.Clone(h.attrs), attrs...)
	return &StoreHandler{
		store:  h.store,
		level:  h.level,
		attrs:  nextAttrs,
		groups: slices.Clone(h.groups),
	}
}

func (h *StoreHandler) WithGroup(name string) slog.Handler {
	nextGroups := append(slices.Clone(h.groups), name)
	return &StoreHandler{
		store:  h.store,
		level:  h.level,
		attrs:  slices.Clone(h.attrs),
		groups: nextGroups,
	}
}

func addAttr(dst map[string]any, groups []string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	if attr.Value.Kind() == slog.KindGroup {
		nextGroups := groups
		if attr.Key != "" {
			nextGroups = append(slices.Clone(groups), attr.Key)
		}
		for _, groupAttr := range attr.Value.Group() {
			addAttr(dst, nextGroups, groupAttr)
		}
		return
	}

	key := attr.Key
	if len(groups) > 0 {
		key = fmt.Sprintf("%s.%s", joinGroups(groups), attr.Key)
	}
	dst[key] = valueFromSlog(attr.Value)
}

func joinGroups(groups []string) string {
	if len(groups) == 0 {
		return ""
	}
	out := groups[0]
	for i := 1; i < len(groups); i++ {
		out += "." + groups[i]
	}
	return out
}

func valueFromSlog(value slog.Value) any {
	switch value.Kind() {
	case slog.KindBool:
		return value.Bool()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindFloat64:
		return value.Float64()
	case slog.KindInt64:
		return value.Int64()
	case slog.KindString:
		return value.String()
	case slog.KindTime:
		return value.Time().UTC()
	case slog.KindUint64:
		return value.Uint64()
	case slog.KindAny:
		return value.Any()
	default:
		return value.String()
	}
}

func logTime(ts time.Time) time.Time {
	if ts.IsZero() {
		return time.Now().UTC()
	}
	return ts.UTC()
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case uint64:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case uint64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}
