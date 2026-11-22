package controlsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"mindfs/server/internal/api/usecase"
)

// promptPayload 是一条提示词记录的内容。账户名进载荷而不是只在 key 里：
// 冲突卡与日志只拿到记录时也要能说清「这是谁的那条」。
type promptPayload struct {
	Account string `json:"account"`
	Text    string `json:"text"`
}

// promptStore 是提示词库需要的能力面。写成接口是为了让测试能塞假实现，
// 也把「同步只依赖 Load/SetItem」这件事钉在编译期。
type promptStore interface {
	Load() ([]string, error)
	SetItem(text string, present bool) error
}

type promptOpener func(dir string) (promptStore, error)

// PromptsDomain 同步提示词库。提示词是**每账户一份**的文件，所以 key 必须带账户：
// 两台机器上同名账户里有同一条提示词是两件事，合并成一条就会把别人的词塞给你。
type PromptsDomain struct {
	configDir string
	open      promptOpener
}

func NewPromptsDomain(configDir string) (*PromptsDomain, error) {
	return newPromptsDomain(configDir, func(dir string) (promptStore, error) {
		return usecase.NewPromptStoreAt(dir)
	})
}

func newPromptsDomain(configDir string, open promptOpener) (*PromptsDomain, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, errors.New("controlsync: config dir required")
	}
	if open == nil {
		return nil, errors.New("controlsync: prompt opener required")
	}
	return &PromptsDomain{configDir: configDir, open: open}, nil
}

func (d *PromptsDomain) Name() string { return DomainPrompts }

func (d *PromptsDomain) Snapshot(ctx context.Context) ([]Record, error) {
	accounts, err := AccountDirs(d.configDir)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(accounts))
	for _, account := range accounts {
		store, err := d.open(account.Dir)
		if err != nil {
			return nil, fmt.Errorf("controlsync: open prompt store %s: %w", account.Key, err)
		}
		items, err := store.Load()
		if err != nil {
			return nil, fmt.Errorf("controlsync: load prompts %s: %w", account.Key, err)
		}
		seen := make(map[string]bool, len(items))
		for _, text := range items {
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			key := PromptRecordKey(account.Key, text)
			if seen[key] {
				continue // 同一条文本出现两次（老文件可能的残留），记录层面只留一条
			}
			seen[key] = true
			rec, err := newRecord(DomainPrompts, key, promptPayload{Account: account.Key, Text: text})
			if err != nil {
				return nil, err
			}
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (d *PromptsDomain) Apply(ctx context.Context, rec Record) error {
	if rec.Domain != DomainPrompts {
		return fmt.Errorf("controlsync: prompts domain got %q record", rec.Domain)
	}
	if rec.Deleted {
		// 墓碑只有 key：先在本地库里按 key 反查文本，再删。库里本来就没有就是成功。
		account, err := promptAccountOfKey(rec.Key)
		if err != nil {
			return err
		}
		return d.setItem(account, "", false, rec.Key)
	}
	payload, err := decodePayload[promptPayload](rec)
	if err != nil {
		return err
	}
	if got := PromptRecordKey(payload.Account, payload.Text); got != rec.Key {
		return fmt.Errorf("controlsync: prompt key %q does not match payload key %q", rec.Key, got)
	}
	return d.setItem(payload.Account, payload.Text, true, rec.Key)
}

func (d *PromptsDomain) setItem(account, text string, present bool, key string) error {
	dir, err := AccountDirFor(d.configDir, account)
	if err != nil {
		return err
	}
	store, err := d.open(dir)
	if err != nil {
		return fmt.Errorf("controlsync: open prompt store %s: %w", account, err)
	}
	if !present {
		items, err := store.Load()
		if err != nil {
			return fmt.Errorf("controlsync: load prompts %s: %w", account, err)
		}
		for _, item := range items {
			if PromptRecordKey(account, item) == key {
				text = item
				break
			}
		}
		if text == "" {
			return nil // 本地没有这条，墓碑幂等
		}
	}
	if err := store.SetItem(text, present); err != nil {
		return fmt.Errorf("controlsync: apply prompt %s: %w", account, err)
	}
	return nil
}

// PromptRecordKey 拼提示词记录 key：<账户>/sha256:<文本 hash>。
// 用文本 hash 而不是序号：提示词是集合语义，顺序不参与同步。
func PromptRecordKey(account, text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return account + "/sha256:" + hex.EncodeToString(sum[:])
}

// promptAccountOfKey 从 key 里取出账户名并校验。账户名来自网络，必须过白名单。
func promptAccountOfKey(key string) (string, error) {
	slash := strings.Index(key, "/sha256:")
	if slash <= 0 {
		return "", fmt.Errorf("controlsync: malformed prompt key %q", key)
	}
	account := key[:slash]
	if !accountKeyPattern.MatchString(account) {
		return "", fmt.Errorf("controlsync: invalid account key %q", account)
	}
	return account, nil
}
