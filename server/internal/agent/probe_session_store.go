package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)


func (s *probeSessionStore) Get(agentName string) (ProbeSessionBinding, bool) {
	if s == nil {
		return ProbeSessionBinding{}, false
	}
	key := strings.TrimSpace(agentName)
	if key == "" {
		return ProbeSessionBinding{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	binding, ok := s.data[key]
	if !ok || strings.TrimSpace(binding.AgentSessionID) == "" {
		return ProbeSessionBinding{}, false
	}
	return binding, true
}

func (s *probeSessionStore) PutBinding(agentName string, binding ProbeSessionBinding) error {
	if s == nil {
		return nil
	}
	key := strings.TrimSpace(agentName)
	if key == "" {
		return errors.New("agent required")
	}
	binding.AgentSessionID = strings.TrimSpace(binding.AgentSessionID)
	if binding.AgentSessionID == "" {
		return errors.New("agent session id required")
	}
	if binding.ProbeCount <= 0 {
		binding.ProbeCount = 1
	}
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = time.Now().UTC()
	} else {
		binding.CreatedAt = binding.CreatedAt.UTC()
	}
	if binding.UpdatedAt.IsZero() {
		binding.UpdatedAt = time.Now().UTC()
	} else {
		binding.UpdatedAt = binding.UpdatedAt.UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = make(map[string]ProbeSessionBinding)
	}
	s.data[key] = binding
	return s.saveLocked()
}

func (s *probeSessionStore) Delete(agentName string) error {
	if s == nil {
		return nil
	}
	key := strings.TrimSpace(agentName)
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) == 0 {
		return nil
	}
	if _, ok := s.data[key]; !ok {
		return nil
	}
	delete(s.data, key)
	return s.saveLocked()
}

func (s *probeSessionStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return nil
	}
	var data map[string]ProbeSessionBinding
	if err := json.Unmarshal(payload, &data); err != nil {
		return err
	}
	if data == nil {
		data = make(map[string]ProbeSessionBinding)
	}
	s.data = data
	return nil
}

func (s *probeSessionStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		if retryErr := os.Rename(tmp, s.path); retryErr != nil {
			return err
		}
	}
	return nil
}

// GetStatus 获取缓存的 Agent 状态
