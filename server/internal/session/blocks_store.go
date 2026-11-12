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

// BlocksLogAbsolutePath 返回块流的绝对路径（解析器与 AppendBlocks 一致）。
//
// 迁移提示要把「正文在哪」告诉 agent：主日志自 S2d-2 起 content 恒空，
// 只有块流行里才有可读的正文。
func (m *Manager) BlocksLogAbsolutePath(key string) string {
	path, err := m.blocksPath(key)
	if err != nil {
		return ""
	}
	abs, err := m.root.MetaFileAbsolutePath(path)
	if err != nil {
		return ""
	}
	return abs
}

// CountBlocksAfterSeq 数块流里 seq 严格大于 afterSeq 的行数。
//
// 提示文案按「行」给出读取量，而它原本数的是交换行；块流一行一块，
// 两类行数差一个数量级，必须换算，否则「读最后 2 行」只能读到两个块。
// 块流不存在（S2d-2 之前的历史会话）时返回 0，由调用方回落主日志。
func (m *Manager) CountBlocksAfterSeq(key string, afterSeq int) (int, error) {
	// 不加 m.mu：块流 append-only，读到半行写坏的尾行会被下面的解析跳过；
	// 而这里在发送路径上、文件可达数 MB，占着锁只会挡住其他会话的读写。
	path, err := m.blocksPath(key)
	if err != nil {
		return 0, err
	}
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	// 块行可达 700KB，不能用默认 64KB token 的 bufio.Scanner；整份读入再按行切。
	count := 0
	for _, line := range strings.Split(string(payload), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var head struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal([]byte(line), &head); err != nil {
			continue // 坏行跳过，与 parseBlocks 一致
		}
		if head.Seq > afterSeq {
			count++
		}
	}
	return count, nil
}

func exchangePathForKey(key string) (string, error) {
	return filepath.ToSlash(fmt.Sprintf(exchangeFileTpl, key)), nil
}

func blocksPathForKey(key string) (string, error) {
	return filepath.ToSlash(fmt.Sprintf(blocksFileTpl, key)), nil
}
