package controlsync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"mindfs/server/internal/pins"
)

// 置顶是「存在即置顶」的布尔语义，值与账户都进载荷：
// 冲突卡只拿到记录时也要能显示「谁的哪个会话」。
type projectPinPayload struct {
	Scope      string `json:"scope"`
	PinnedAtMs int64  `json:"pinned_at_ms"`
}

type sessionPinPayload struct {
	Scope    string `json:"scope"`
	PinnedAt string `json:"pinned_at"`
}

const (
	pinKindProject = "project"
	pinKindSession = "session"
)

// pinStore 是置顶表需要的能力面。
type pinStore interface {
	ProjectPins() map[string]int64
	SessionPinnedAtAll() map[string]string
	RestoreProjectPin(key string, atMillis int64) error
	RestoreSessionPin(key, at string) error
}

type pinOpener func(dir string) (pinStore, error)

// PinsDomain 同步置顶。置顶文件按账户分，key 必须带账户。
type PinsDomain struct {
	configDir string
	open      pinOpener
}

func NewPinsDomain(configDir string) (*PinsDomain, error) {
	return newPinsDomain(configDir, func(dir string) (pinStore, error) {
		return pins.NewStoreAt(dir)
	})
}

func newPinsDomain(configDir string, open pinOpener) (*PinsDomain, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, errors.New("controlsync: config dir required")
	}
	if open == nil {
		return nil, errors.New("controlsync: pin opener required")
	}
	return &PinsDomain{configDir: configDir, open: open}, nil
}

func (d *PinsDomain) Name() string { return DomainPins }

func (d *PinsDomain) Snapshot(ctx context.Context) ([]Record, error) {
	accounts, err := AccountDirs(d.configDir)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(accounts))
	for _, account := range accounts {
		store, err := d.open(account.Dir)
		if err != nil {
			return nil, fmt.Errorf("controlsync: open pin store %s: %w", account.Key, err)
		}
		for scope, at := range store.ProjectPins() {
			rec, err := newRecord(DomainPins, PinRecordKey(account.Key, pinKindProject, scope), projectPinPayload{Scope: scope, PinnedAtMs: at})
			if err != nil {
				return nil, err
			}
			out = append(out, rec)
		}
		for scope, at := range store.SessionPinnedAtAll() {
			rec, err := newRecord(DomainPins, PinRecordKey(account.Key, pinKindSession, scope), sessionPinPayload{Scope: scope, PinnedAt: at})
			if err != nil {
				return nil, err
			}
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (d *PinsDomain) Apply(ctx context.Context, rec Record) error {
	if rec.Domain != DomainPins {
		return fmt.Errorf("controlsync: pins domain got %q record", rec.Domain)
	}
	account, kind, scope, err := parsePinRecordKey(rec.Key)
	if err != nil {
		return err
	}
	if !rec.Deleted {
		switch kind {
		case pinKindProject:
			payload, err := decodePayload[projectPinPayload](rec)
			if err != nil {
				return err
			}
			if payload.Scope != scope {
				return fmt.Errorf("controlsync: pin key %q does not match payload scope %q", rec.Key, payload.Scope)
			}
			return d.apply(account, kind, scope, payload.PinnedAtMs, "")
		case pinKindSession:
			payload, err := decodePayload[sessionPinPayload](rec)
			if err != nil {
				return err
			}
			if payload.Scope != scope {
				return fmt.Errorf("controlsync: pin key %q does not match payload scope %q", rec.Key, payload.Scope)
			}
			return d.apply(account, kind, scope, 0, payload.PinnedAt)
		default:
			return fmt.Errorf("controlsync: unknown pin kind %q", kind)
		}
	}
	// 墓碑：kind 与 scope 都从 key 里取，值留空即删除。
	return d.apply(account, kind, scope, 0, "")
}

func (d *PinsDomain) apply(account, kind, scope string, atMillis int64, at string) error {
	dir, err := AccountDirFor(d.configDir, account)
	if err != nil {
		return err
	}
	store, err := d.open(dir)
	if err != nil {
		return fmt.Errorf("controlsync: open pin store %s: %w", account, err)
	}
	if kind == pinKindProject {
		return store.RestoreProjectPin(scope, atMillis)
	}
	return store.RestoreSessionPin(scope, at)
}

// PinRecordKey 拼置顶记录 key：<账户>/<kind>:<scope>。
func PinRecordKey(account, kind, scope string) string {
	return account + "/" + kind + ":" + scope
}

func parsePinRecordKey(key string) (account, kind, scope string, err error) {
	slash := strings.Index(key, "/")
	if slash <= 0 {
		return "", "", "", fmt.Errorf("controlsync: malformed pin key %q", key)
	}
	account = key[:slash]
	if !accountKeyPattern.MatchString(account) {
		return "", "", "", fmt.Errorf("controlsync: invalid account key %q", account)
	}
	rest := key[slash+1:]
	colon := strings.Index(rest, ":")
	if colon <= 0 {
		return "", "", "", fmt.Errorf("controlsync: malformed pin key %q", key)
	}
	kind, scope = rest[:colon], rest[colon+1:]
	if kind != pinKindProject && kind != pinKindSession {
		return "", "", "", fmt.Errorf("controlsync: unknown pin kind %q", kind)
	}
	if strings.TrimSpace(scope) == "" {
		return "", "", "", fmt.Errorf("controlsync: empty pin scope in %q", key)
	}
	return account, kind, scope, nil
}
