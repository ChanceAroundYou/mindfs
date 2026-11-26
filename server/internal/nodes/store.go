package nodes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mindfs/server/internal/config"
)

const nodesFileName = "nodes.json"

type NodeConnection struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// URL 是直连基址（用户可填，可为空）。
	URL   string `json:"url"`
	Color string `json:"color"`
	// RelayURL 是主控替这台机器转发时用的兜底基址（<主控>/relay/<name>）。
	// 它是**同一节点上的第二通道**，不是另一个节点：直连 URL 为空时才用它。
	// 由 relay 服务按 InstanceID 合并写入；零值 = 没有 relay 通道，老 nodes.json 免迁移。
	RelayURL string `json:"relayUrl,omitempty"`
	// InstanceID 是这台设备的稳定身份（edge 的 control_sync.instance_id）。
	// 主控据此把 relay 地址并回既有节点行；手工添加的节点为空。
	InstanceID string `json:"instanceId,omitempty"`
	// Remote 标记这是**另一台机器**上的节点。跨节点请求不带 user=
	// （账户表每台机器独立，id 带不过去），由服务端回落到对方主节点；
	// 认用户名那条老路仍然有效，但调用方不必再猜对方有没有这个账户。
	//
	// 零值 false = 本机节点，行为与加此字段之前逐字节一致，老 nodes.json 免迁移。
	Remote bool `json:"remote,omitempty"`
}

// RouteBase 返回这个节点实际可用的基址：直连优先，relay 兜底。
func (n NodeConnection) RouteBase() string {
	if u := strings.TrimSpace(n.URL); u != "" {
		return u
	}
	return strings.TrimSpace(n.RelayURL)
}

type Store struct {
	mu   sync.RWMutex
	path string
	data []NodeConnection
}

func NewStore() (*Store, error) {
	configDir, err := config.MindFSConfigDir()
	if err != nil {
		return nil, err
	}
	return NewStoreAt(configDir)
}

// NewStoreAt 把节点表放在指定目录（多账户：每个账户一套节点）。
func NewStoreAt(configDir string) (*Store, error) {
	store := &Store{path: filepath.Join(configDir, nodesFileName)}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	var data []NodeConnection
	if err := json.Unmarshal(b, &data); err != nil {
		return err
	}
	normalized := normalize(data)
	s.data = normalized
	// 归一可能丢掉遗留 relay:* 行或合并同 InstanceID 的行；长度变了就落盘，
	// 让「清理旧 relay:* 数据」真正写回文件，而不是等下一次写操作。
	if len(normalized) != len(data) {
		_ = s.saveLocked()
	}
	return nil
}

func normalize(in []NodeConnection) []NodeConnection {
	out := make([]NodeConnection, 0, len(in))
	seen := map[string]struct{}{}
	for _, n := range in {
		n.ID = strings.TrimSpace(n.ID)
		n.Name = strings.TrimSpace(n.Name)
		n.URL = strings.TrimSpace(strings.TrimRight(n.URL, "/"))
		n.Color = strings.TrimSpace(n.Color)
		n.RelayURL = strings.TrimSpace(strings.TrimRight(n.RelayURL, "/"))
		n.InstanceID = strings.TrimSpace(n.InstanceID)
		// 旧实现把 relay 地址发布成 ID=relay:<slug> 的独立节点行，同一台机器于是出现两个节点。
		// 现在 relay 是节点上的 relayUrl 字段；遗留的 relay:* 行直接丢弃，隧道重连后按新方案重建。
		if strings.HasPrefix(n.ID, "relay:") {
			continue
		}
		if n.ID == "" || n.Name == "" || (n.URL == "" && n.RelayURL == "") {
			continue
		}
		if _, ok := seen[n.ID]; ok {
			continue
		}
		seen[n.ID] = struct{}{}
		out = append(out, n)
	}
	return mergeByInstance(out)
}

// mergeByInstance 把共享同一非空 InstanceID 的节点行合并成一行：保留直连 URL，
// 把 relay 兜底与颜色/Remote 并过去。「直连行」与「relay-only 行」因此收敛成同一台机器的一行。
func mergeByInstance(in []NodeConnection) []NodeConnection {
	out := make([]NodeConnection, 0, len(in))
	index := map[string]int{}
	for _, n := range in {
		if n.InstanceID == "" {
			out = append(out, n)
			continue
		}
		i, ok := index[n.InstanceID]
		if !ok {
			index[n.InstanceID] = len(out)
			out = append(out, n)
			continue
		}
		prim := &out[i]
		relay := prim.RelayURL
		if relay == "" {
			relay = n.RelayURL
		}
		if prim.URL == "" && n.URL != "" {
			// 直连那一行胜出：换成它的地址/名字，但把已记下的 relay 与颜色/Remote 带上。
			remote := prim.Remote || n.Remote
			color := n.Color
			if color == "" {
				color = prim.Color
			}
			n.RelayURL = relay
			n.Color = color
			n.Remote = remote
			*prim = n
			continue
		}
		prim.RelayURL = relay
		if prim.Color == "" {
			prim.Color = n.Color
		}
		prim.Remote = prim.Remote || n.Remote
	}
	return out
}

// NameConflicts 判定一次整表写入是否**新引入**了重名（trim + 忽略大小写）。
//
// 语义按「只拒绝新增/改名的冲突，不强行修历史数据」定：
//   - 同一对 id 在 existing 里就已经同名并存 → 是历史数据，放行；
//   - 其余任何两个不同 id 同名 → 拒绝，返回其中后一个作为冲突提示。
//
// 为什么按整表比较而不是逐条 upsert：前端保存节点表走 PUT（整表替换），
// 逐条检查会被整体替换绕过。
func NameConflicts(existing, incoming []NodeConnection) (NodeConnection, bool) {
	fold := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	existingNames := make(map[string]string, len(existing))
	for _, n := range existing {
		existingNames[n.ID] = fold(n.Name)
	}
	historicallyPaired := func(a, b string) bool {
		nameA, okA := existingNames[a]
		nameB, okB := existingNames[b]
		return okA && okB && nameA != "" && nameA == nameB
	}

	groups := map[string][]NodeConnection{}
	for _, n := range incoming {
		key := fold(n.Name)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], n)
	}
	for _, group := range groups {
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				if historicallyPaired(group[i].ID, group[j].ID) {
					continue
				}
				return group[j], true
			}
		}
	}
	return NodeConnection{}, false
}

func (s *Store) Get(id string) (NodeConnection, bool) {
	id = strings.TrimSpace(id)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, n := range s.data {
		if n.ID == id {
			return n, true
		}
	}
	return NodeConnection{}, false
}

func (s *Store) List() []NodeConnection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]NodeConnection, len(s.data))
	copy(out, s.data)
	return out
}

func (s *Store) Replace(nodes []NodeConnection) error {
	incoming := normalize(nodes)
	s.mu.Lock()
	defer s.mu.Unlock()
	// 整表替换来自前端缓存，可能比 relay 发布晚一拍（页面先加载、隧道后接通）。
	// 同 ID / 同 instanceId 的行把本机已知的 relay 通道与设备身份带回来，
	// 否则一次改名/改色就会把兜底地址抹掉 —— 现场症状正是「hasee 的 relay 行
	// 在别人改节点表后消失，只能等隧道重连才回来」。
	for i := range incoming {
		if incoming[i].RelayURL != "" && incoming[i].InstanceID != "" {
			continue
		}
		for _, old := range s.data {
			if old.ID != incoming[i].ID && (old.InstanceID == "" || old.InstanceID != incoming[i].InstanceID) {
				continue
			}
			if incoming[i].RelayURL == "" {
				incoming[i].RelayURL = old.RelayURL
			}
			if incoming[i].InstanceID == "" {
				incoming[i].InstanceID = old.InstanceID
			}
			break
		}
	}
	s.data = mergeByInstance(incoming)
	return s.saveLocked()
}

func (s *Store) Upsert(node NodeConnection) (NodeConnection, error) {
	node.ID = strings.TrimSpace(node.ID)
	node.Name = strings.TrimSpace(node.Name)
	node.URL = strings.TrimSpace(strings.TrimRight(node.URL, "/"))
	node.Color = strings.TrimSpace(node.Color)
	node.RelayURL = strings.TrimSpace(strings.TrimRight(node.RelayURL, "/"))
	node.InstanceID = strings.TrimSpace(node.InstanceID)
	if node.ID == "" {
		return NodeConnection{}, errors.New("id required")
	}
	if node.Name == "" {
		return NodeConnection{}, errors.New("name required")
	}
	if node.URL == "" && node.RelayURL == "" {
		return NodeConnection{}, errors.New("url required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data {
		if n.ID == node.ID {
			s.data[i] = node
			s.data = mergeByInstance(s.data)
			return node, s.saveLocked()
		}
	}
	s.data = append(s.data, node)
	s.data = mergeByInstance(s.data)
	return node, s.saveLocked()
}

// SetRelay 把一条 relay 通道并进设备对应的节点行（按 InstanceID 配对）。
//
// 找不到设备时才新建一行 relay-only 节点；绝不产生 relay:* 这样的第二节点。
// 这是「一台设备、直连优先、relay 兜底」的落点：直连 URL 原样保留，只补 RelayURL。
func (s *Store) SetRelay(instanceID, name, relayURL string) (NodeConnection, error) {
	instanceID = strings.TrimSpace(instanceID)
	name = strings.TrimSpace(name)
	relayURL = strings.TrimSpace(strings.TrimRight(relayURL, "/"))
	if instanceID == "" {
		return NodeConnection{}, errors.New("instance id required")
	}
	if relayURL == "" {
		return NodeConnection{}, errors.New("relay url required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for i := range s.data {
		if s.data[i].InstanceID == instanceID || s.data[i].ID == instanceID {
			s.data[i].InstanceID = instanceID
			s.data[i].RelayURL = relayURL
			s.data[i].Remote = true
			found = true
			break
		}
	}
	if !found {
		// 直连行是用户手工加的（id 是随机 uuid），可能没有 InstanceID；relay 只知道
		// instance_id。名字就是 instance_id 且唯一的直连行可以确定是同一台设备，
		// 把身份与 relay 补上去，而不是再建一条 relay-only 行（同一台机器两个节点）。
		if i, ok := uniqueDirectRowByName(s.data, instanceID); ok {
			s.data[i].InstanceID = instanceID
			s.data[i].RelayURL = relayURL
			s.data[i].Remote = true
			found = true
		}
	}
	if !found {
		if name == "" {
			name = instanceID
		}
		s.data = append(s.data, NodeConnection{
			ID:         instanceID,
			Name:       name,
			RelayURL:   relayURL,
			InstanceID: instanceID,
			Remote:     true,
		})
	}
	s.data = mergeByInstance(s.data)
	var out NodeConnection
	for _, n := range s.data {
		if n.InstanceID == instanceID {
			out = n
			break
		}
	}
	return out, s.saveLocked()
}

// uniqueDirectRowByName 找唯一一条「没有身份、名字等于 instanceID」的直连行。
//
// 只在恰好一条时返回；多个同名候选说明无法确定是哪台机器，宁可退回 relay-only
// 行，也不要把通道并到别人身上。
func uniqueDirectRowByName(rows []NodeConnection, instanceID string) (int, bool) {
	idx := -1
	for i := range rows {
		if rows[i].InstanceID != "" || rows[i].URL == "" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(rows[i].Name), instanceID) {
			continue
		}
		if idx >= 0 {
			return -1, false
		}
		idx = i
	}
	return idx, idx >= 0
}

func (s *Store) Remove(id string) (NodeConnection, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return NodeConnection{}, errors.New("id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, n := range s.data {
		if n.ID == id {
			removed := n
			s.data = append(s.data[:i], s.data[i+1:]...)
			return removed, s.saveLocked()
		}
	}
	return NodeConnection{}, errors.New("node not found")
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
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
