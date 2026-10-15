# 后端存量事项

- [ ] `server/internal/api/agent_config.go` 裸 error 绕过 `apperr` 中文消息层：`errors.New("backup id required")`、`"backup not found"`、`errAgentConfigConflict` 等业务文案匹配不上 `apperr.Classify` 的关键字兜底，原样进 `respondError` payload，用户看到英文原文。修法：包一层 `apperr`（参考同文件已有的 `apperr.Wrap` 用法）。下次动 agent_config 相关代码时顺手修（2026-10-15 后端体检发现，LOW）。

长期约束见同目录规则文档；历史拆分记录保留在对应计划和提交记录中。
