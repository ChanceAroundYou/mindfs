package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// blocksFileTpl 与主日志 / aux 同目录，便于一起备份与一起迁移。
const blocksFileTpl = "sessions/%s.blocks.jsonl"

// blockStepSentinel 表示「本轮步序未定」——块先落盘、步后收口时用（方案 §3.3）。
const blockStepSentinel = -1

func (m *Manager) blocksPath(key string) (string, error) {
	if strings.TrimSpace(m.root.MetaDir()) == "" {
		return "", errors.New("managed dir required")
	}
	if key == "" {
		return "", errors.New("session key required")
	}
	if strings.Contains(key, "..") || strings.ContainsRune(key, filepath.Separator) || strings.Contains(key, "/") {
		return "", fmt.Errorf("invalid session key: %s", key)
	}
	return filepath.ToSlash(fmt.Sprintf(blocksFileTpl, key)), nil
}

// AppendBlocks 追加块到块流。**调用方负责 blocks 非空** —— 空切片是 no-op。
//
// 不持 m.mu 由调用方保证吗？不：这里加锁，与 appendExchange / appendExchangeAux 一致。
// 块流没有内存缓存（不像 exchanges），所以读侧不需要游标推进。
func (m *Manager) AppendBlocks(ctx context.Context, key string, blocks []Block) error {
	if len(blocks) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	path, err := m.blocksPath(key)
	if err != nil {
		return err
	}
	file, err := m.root.OpenMetaFileAppend(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, block := range blocks {
		payload, err := json.Marshal(block)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(payload, '\n')); err != nil {
			return err
		}
	}
	return writer.Flush()
}

// ReadBlocks 全量读块流（按写入序，含全部 revision）。
//
// 用于复制（fork）与体检：那些场景要的是「盘上有什么」，所以不解析 revision。
// 渲染用的窗口读走 GetBlocksWindow。
func (m *Manager) ReadBlocks(ctx context.Context, key string) ([]Block, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readBlocksAll(key)
}

func (m *Manager) readBlocksAll(key string) ([]Block, error) {
	path, err := m.blocksPath(key)
	if err != nil {
		return nil, err
	}
	m.fullBlockReads++
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Block{}, nil
		}
		return nil, err
	}
	return parseBlocks(payload), nil
}

func exchangePathForKey(key string) (string, error) {
	return filepath.ToSlash(fmt.Sprintf(exchangeFileTpl, key)), nil
}

func blocksPathForKey(key string) (string, error) {
	return filepath.ToSlash(fmt.Sprintf(blocksFileTpl, key)), nil
}
