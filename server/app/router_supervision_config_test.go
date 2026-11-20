package app

import (
	"testing"

	"mindfs/server/internal/kanban"
)

// 主动监管配置必须从 StartOptions 走到 kanban.RouterConfig：cli/cmd 只负责
// 解 config.json，app 负责归一化，中间少传一个字段就等于配置写了不生效。
func TestRouterConfigFromOptionsCarriesSupervision(t *testing.T) {
	cfg := routerConfigFromOptions(StartOptions{
		RouterMode:                         "coordinate",
		RouterSupervisionMode:              "shadow",
		RouterSupervisionMaxRetriesPerNode: 3,
		RouterSupervisionMaxActionsPerHour: 4,
	})
	if cfg.Supervision.Mode != kanban.RouterSupervisionShadow ||
		cfg.Supervision.MaxRetriesPerNode != 3 || cfg.Supervision.MaxActionsPerHour != 4 {
		t.Fatalf("supervision = %+v", cfg.Supervision)
	}

	// 未配置 = off + 默认预算，不能因为空串被当成 on。
	off := routerConfigFromOptions(StartOptions{})
	if off.Supervision.Mode != kanban.RouterSupervisionOff {
		t.Fatalf("default supervision mode = %q, want off", off.Supervision.Mode)
	}
	if off.Supervision.MaxRetriesPerNode != 2 || off.Supervision.MaxActionsPerHour != 6 {
		t.Fatalf("default supervision budget = %+v", off.Supervision)
	}
}
