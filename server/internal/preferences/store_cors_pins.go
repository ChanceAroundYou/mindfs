package preferences

import (
	"errors"
	"strings"
)

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}


func (s *Store) IsCORSOriginAllowed(origin string) bool {
	if s == nil {
		return false
	}
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	// ponytail: exact origin match (scheme+host+port), case-insensitive; "*" means allow all
	allowed := s.CORSAllowOrigins()
	for _, entry := range allowed {
		e := strings.TrimSpace(entry)
		if e == "*" {
			return true
		}
		if strings.EqualFold(e, origin) {
			return true
		}
	}
	return false
}


func (s *Store) CORSPreferences() CORSPreferences {
	if s == nil {
		return CORSPreferences{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := s.data.CORS
	out := make([]string, len(cp.AllowOrigins))
	copy(out, cp.AllowOrigins)
	cp.AllowOrigins = out
	return cp
}


func (s *Store) UpdateCORSPreferences(mode string, allowOrigins []string) error {
	if s == nil {
		return nil
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "open"
	}
	switch mode {
	case "open", "auto", "allow_all", "all", "*", "allowlist", "whitelist", "disabled", "off", "closed", "same_origin":
	default:
		return errors.New("invalid cors mode: use open/auto/allowlist/disabled")
	}
	normalized := make([]string, 0, len(allowOrigins))
	seen := map[string]struct{}{}
	for _, raw := range allowOrigins {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if v != "*" {
			// basic origin shape check: must parse as URL with scheme+host
			// allow bare origin like https://host or https://host:port
			if !strings.Contains(v, "://") {
				return errors.New("allow_origins must be origins like https://host or *")
			}
		}
		low := strings.ToLower(v)
		if _, ok := seen[low]; ok {
			continue
		}
		seen[low] = struct{}{}
		if v == "*" {
			normalized = []string{"*"}
			break
		}
		normalized = append(normalized, v)
	}
	if (mode == "allowlist" || mode == "whitelist") && len(normalized) == 0 {
		return errors.New("allowlist mode requires at least one allow_origins entry")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := CORSPreferences{Mode: mode, AllowOrigins: normalized}
	if s.data.CORS.Mode == next.Mode && equalStringSlices(s.data.CORS.AllowOrigins, next.AllowOrigins) {
		return nil
	}
	s.data.CORS = next
	return s.saveLocked()
}


func (s *Store) SessionProjectPins() map[string]int64 {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int64, len(s.data.SessionProjectPins))
	for k, v := range s.data.SessionProjectPins {
		out[k] = v
	}
	return out
}


func (s *Store) UpdateSessionProjectPins(pins map[string]int64) error {
	if s == nil {
		return nil
	}
	if pins == nil {
		pins = map[string]int64{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(pins) == len(s.data.SessionProjectPins) {
		same := true
		for k, v := range pins {
			if s.data.SessionProjectPins[k] != v {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	next := make(map[string]int64, len(pins))
	for k, v := range pins {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		next[k] = v
	}
	s.data.SessionProjectPins = next
	return s.saveLocked()
}


func (s *Store) CORSMode() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.data.CORS.Mode)
}


func (s *Store) CORSAllowOrigins() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.data.CORS.AllowOrigins))
	copy(out, s.data.CORS.AllowOrigins)
	return out
}

