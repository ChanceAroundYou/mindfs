package controlsync

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"mindfs/server/internal/nodes"
)

// NodesDomain 同步节点连接表。
//
// 合并单位是**节点的连接地址**（协议 + 主机 + 端口 + 部署前缀），不是 id：
// 每台机器给同一个节点起的 id 可以不同，但地址是同一个 —— 按 id 合并会让
// 「fn 在本机叫 fn、在 pc 上叫 node-2」变成两条互不相干的记录。
type NodesDomain struct {
	store *nodes.Store
}

func NewNodesDomain(store *nodes.Store) *NodesDomain {
	return &NodesDomain{store: store}
}

func (d *NodesDomain) Name() string { return DomainNodes }

func (d *NodesDomain) Snapshot(ctx context.Context) ([]Record, error) {
	if d == nil || d.store == nil {
		return nil, errors.New("controlsync: nodes store required")
	}
	list := d.store.List()
	out := make([]Record, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, node := range list {
		key := NodeBaseKey(node.RouteBase())
		if key == "" {
			return nil, fmt.Errorf("controlsync: node %q has no url or relay url", node.ID)
		}
		if seen[key] {
			return nil, fmt.Errorf("controlsync: duplicate node url %q", key)
		}
		seen[key] = true
		rec, err := newRecord(DomainNodes, key, node)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (d *NodesDomain) Apply(ctx context.Context, rec Record) error {
	if d == nil || d.store == nil {
		return errors.New("controlsync: nodes store required")
	}
	if rec.Domain != DomainNodes {
		return fmt.Errorf("controlsync: nodes domain got %q record", rec.Domain)
	}
	list := d.store.List()
	idx := -1
	for i, node := range list {
		if NodeBaseKey(node.RouteBase()) == rec.Key {
			idx = i
			break
		}
	}
	if rec.Deleted {
		if idx < 0 {
			return nil // 幂等：已经不存在就是成功
		}
		return d.store.Replace(append(list[:idx], list[idx+1:]...))
	}
	node, err := decodePayload[nodes.NodeConnection](rec)
	if err != nil {
		return err
	}
	node.ID = strings.TrimSpace(node.ID)
	node.Name = strings.TrimSpace(node.Name)
	node.URL = strings.TrimSpace(node.URL)
	node.Color = strings.TrimSpace(node.Color)
	node.RelayURL = strings.TrimSpace(node.RelayURL)
	node.InstanceID = strings.TrimSpace(node.InstanceID)
	if node.ID == "" || node.Name == "" || (node.URL == "" && node.RelayURL == "") {
		// store.Replace 会静默丢掉缺字段的条目 —— 那样同步就会无声地少一条节点。
		return fmt.Errorf("controlsync: node payload missing id/name/url (key %q)", rec.Key)
	}
	if got := NodeBaseKey(node.RouteBase()); got != rec.Key {
		return fmt.Errorf("controlsync: node key %q does not match payload url key %q", rec.Key, got)
	}
	for i, other := range list {
		if i != idx && other.ID == node.ID {
			return fmt.Errorf("controlsync: node id %q already used by %q", node.ID, NodeBaseKey(other.RouteBase()))
		}
	}
	if idx >= 0 {
		list[idx] = node
	} else {
		list = append(list, node)
	}
	return d.store.Replace(list)
}

// NodeBaseKey 把节点地址归一成跨机器身份：小写协议与主机、去掉末尾斜杠、
// 丢掉 query 与 fragment（它们不参与路由）。
func NodeBaseKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	var b strings.Builder
	b.WriteString(strings.ToLower(u.Scheme))
	b.WriteString("://")
	b.WriteString(strings.ToLower(u.Host))
	b.WriteString(strings.TrimRight(u.Path, "/"))
	return b.String()
}
