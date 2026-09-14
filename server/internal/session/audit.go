package session

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
)

// SessionAudit 是一次**只读**体检的结果：只报告，不修任何东西。
// 用途是回答"这个会话文件到底有没有结构性毛病"，供人工决定要不要清理。
type SessionAudit struct {
	Rows             int   `json:"rows"`
	MaxSeq           int   `json:"max_seq"`
	SeqGaps          []int `json:"seq_gaps,omitempty"`
	SeqDuplicates    []int `json:"seq_duplicates,omitempty"`
	DamagedLines     int   `json:"damaged_lines"`
	NulRepairedLines int   `json:"nul_repaired_lines"`
	AuxRows          int   `json:"aux_rows"`
	AuxOrphanSeqs    []int `json:"aux_orphan_seqs,omitempty"`
}

// AuditSession 读原始文件（不走内存缓存）做一次体检。
// 持 m.mu 是为了拿到一致快照：审计期间不允许写入插进来。
func (m *Manager) AuditSession(key string) (SessionAudit, error) {
	var out SessionAudit
	if strings.TrimSpace(key) == "" {
		return out, errors.New("session key required")
	}
	path, err := m.exchangePath(key)
	if err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	payload, err := m.root.ReadMetaFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	counts := map[int]int{}
	total := 0
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry, ok, repaired := parseExchangeLine(line)
		if !ok {
			out.DamagedLines++
			continue
		}
		if repaired {
			out.NulRepairedLines++
		}
		seq := entry.Seq
		if seq <= 0 {
			seq = total + 1
		}
		if seq > total {
			total = seq
		}
		counts[seq]++
		out.Rows++
	}
	out.MaxSeq = total
	for seq := 1; seq <= total; seq++ {
		if counts[seq] == 0 {
			out.SeqGaps = append(out.SeqGaps, seq)
		}
	}
	for seq, count := range counts {
		if count > 1 {
			out.SeqDuplicates = append(out.SeqDuplicates, seq)
		}
	}
	sort.Ints(out.SeqDuplicates)

	auxPath, err := m.auxPath(key)
	if err != nil {
		return out, err
	}
	auxPayload, err := m.root.ReadMetaFile(auxPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	for _, line := range strings.Split(string(auxPayload), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var aux ExchangeAux
		if err := json.Unmarshal([]byte(line), &aux); err != nil {
			if trimmed := strings.TrimLeft(line, "\x00"); trimmed != line {
				if err := json.Unmarshal([]byte(trimmed), &aux); err != nil {
					out.DamagedLines++
					continue
				}
				out.NulRepairedLines++
			} else {
				out.DamagedLines++
				continue
			}
		}
		out.AuxRows++
		if aux.Seq <= 0 || counts[aux.Seq] == 0 {
			out.AuxOrphanSeqs = append(out.AuxOrphanSeqs, aux.Seq)
		}
	}
	return out, nil
}
