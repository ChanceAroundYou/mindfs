package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"mindfs/server/internal/preferences"
)

// preferenceStore 是偏好表需要的能力面。
type preferenceStore interface {
	Fields() (map[string]json.RawMessage, error)
	SetField(name string, raw json.RawMessage) error
}

// PreferencesDomain 同步共享偏好。合并单位是**顶层字段**：整文件覆盖会让
// 「A 改了 agents」把「B 刚改的 session_naming」一起冲掉，冲突也永远落在同一行上。
type PreferencesDomain struct {
	store preferenceStore
}

func NewPreferencesDomain(store *preferences.Store) *PreferencesDomain {
	if store == nil {
		return &PreferencesDomain{}
	}
	return &PreferencesDomain{store: store}
}

func (d *PreferencesDomain) Name() string { return DomainPreferences }

func (d *PreferencesDomain) Snapshot(ctx context.Context) ([]Record, error) {
	if d == nil || d.store == nil {
		return nil, errors.New("controlsync: preferences store required")
	}
	fields, err := d.store.Fields()
	if err != nil {
		return nil, fmt.Errorf("controlsync: read preferences: %w", err)
	}
	out := make([]Record, 0, len(fields))
	for name, raw := range fields {
		rec, err := newRecord(DomainPreferences, name, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (d *PreferencesDomain) Apply(ctx context.Context, rec Record) error {
	if d == nil || d.store == nil {
		return errors.New("controlsync: preferences store required")
	}
	if rec.Domain != DomainPreferences {
		return fmt.Errorf("controlsync: preferences domain got %q record", rec.Domain)
	}
	if !preferences.ValidFieldName(rec.Key) {
		return fmt.Errorf("controlsync: invalid preference field %q", rec.Key)
	}
	if rec.Deleted {
		return d.store.SetField(rec.Key, nil)
	}
	if len(rec.Payload) == 0 {
		return fmt.Errorf("controlsync: empty payload for preference %q", rec.Key)
	}
	return d.store.SetField(rec.Key, rec.Payload)
}
