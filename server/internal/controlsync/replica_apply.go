package controlsync

// 这里放「把副本里的记录写进权威存储」这一条共用路径。
//
// 两个调用方：在线追平时的账户可用性（applyOnlineUsers），以及离线交接（Handover）。
// 两者之前在行为上并不同步 —— 在线根本不写权威存储，交接按 ListRecords 的 key 序
// 盲写 —— 于是同一批记录在两种模式下会得到两种结果。共用一份实现比各自修一遍安全。

import (
	"context"
	"fmt"
	"sort"
)

// applyReplicaDomain 把某个域的副本记录按域自己的顺序写进权威存储。
//
// 顺序由 domainApplyOrder 决定，不再用 ListRecords 的 key 升序：key 是记录身份，
// 不是依赖顺序。users 域就是现成的反例 —— "@primary" 按 key 排在所有账户前面，
// 先应用它会因为「引用了一个本机还不存在的账户」直接失败。
//
// pending 里的记录跳过：那些是本地改过、还没回传的，用主节点的值盖掉等于吞掉用户改动。
// 集成在 Suppress 里执行：Apply 写的是权威存储，采集器在快照上分不清这次写入来自远端，
// 不抑制就会把它当成本地改动再推回去。
func (r *Runtime) applyReplicaDomain(ctx context.Context, domain string) error {
	if r == nil || r.registry == nil {
		return nil
	}
	d, err := r.registry.Get(domain)
	if err != nil {
		return err
	}
	records, err := r.store.ListRecords(ctx, domain)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	pending, err := r.store.PendingKeys(ctx, domain)
	if err != nil {
		return err
	}
	sortRecordsForApply(domain, records)
	apply := func() error {
		for _, rec := range records {
			if pending[rec.Key] {
				continue
			}
			if err := d.Apply(ctx, rec); err != nil {
				return fmt.Errorf("controlsync: apply %s/%s: %w", rec.Domain, rec.Key, err)
			}
		}
		return nil
	}
	if r.capturer == nil {
		return apply()
	}
	return r.capturer.Suppress(ctx, []string{domain}, apply)
}

// sortRecordsForApply 按域的依赖顺序重排记录。
//
// users：账户本身的记录必须排在主账户标记（@primary）之前 —— SetPrimary 要求目标
// 账户已经在表里。其余域没有内部依赖，保持 key 序（稳定、可复现）即可。
func sortRecordsForApply(domain string, records []Record) {
	if domain != DomainUsers {
		sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
		return
	}
	sort.SliceStable(records, func(i, j int) bool {
		iPrimary := records[i].Key == usersPrimaryKey
		jPrimary := records[j].Key == usersPrimaryKey
		if iPrimary != jPrimary {
			return jPrimary
		}
		return records[i].Key < records[j].Key
	})
}
