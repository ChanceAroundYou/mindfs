package kanban

// 巡检去重登记簿在 ActivityService 上的接线（S5）。
//
// 放在本文件而不是 activity_service.go：后者已在硬线之上（存量登记在
// docs/agent/rules/backend/TODO.md），新代码不往里加行。

// SetInspectionDedupe 挂上跨实例共享的巡检去重登记簿。装配期调用一次。
// nil 不覆盖默认实例：老装配 / 单测至少还能用本地那份（只是跨实例不共享）。
func (s *ActivityService) SetInspectionDedupe(d *InspectionDedupe) {
	if s == nil || d == nil {
		return
	}
	s.inspectionDedupe = d
}

// inspectionDedupeFn 返回可用的登记簿。nil 安全：没有装配时退化成「永远不判重」，
// 宁可多出一条巡检汇报，也不要因为缺装配把每一条都吞掉。
func (s *ActivityService) inspectionDedupeFn() *InspectionDedupe {
	if s == nil {
		return nil
	}
	return s.inspectionDedupe
}
