# Changelog

本文件记录 MindFS 的功能变更。格式参考
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循语义化版本。

## [v0.6.0] - 2026-11-12

### Added

- **对话式任务系统**：任务交互从阶段式重构为对话式。
  - 用户在任务详情面板发送留言，Agent 自动选择 Workflow 执行并返回总结回复。
  - Agent 回复卡片实时显示进度（步骤 i/n）与状态（等待中 / 执行中 / 已完成 / 失败）。
  - 支持「查看执行详情」进入 Workflow 运行视图，以及从节点跳转到对应会话。
  - 历史任务提供兼容视图：总结回复 + 可折叠的原有阶段列表。
- **Workflow 元数据**：Workflow 支持描述、使用场景与标签。
  - `/api/workflows/available` 与 `/api/workflows/recommend` 支持 AI 自动推荐。
  - 用户可选择「自动选择 / 指定工作流 / 禁用工作流」。
- **Activity API**：
  - `POST /api/tasks/{id}/activities` —— 创建留言并触发 Agent 回复。
  - `GET /api/tasks/{id}/activities` —— 获取任务活动流。
  - `GET /api/tasks/{id}/activities/{activityId}` —— 获取单条活动。
  - WebSocket 广播 `task.activity.created` / `task.activity.updated`。

### Changed

- 任务详情面板从阶段列表改为对话流；新建任务默认走「用户输入 → Agent 回复」的内置流程。
- Workflow 定位为 Agent 的内部执行工具，不再要求用户先选模板或手工编排阶段。
- 历史任务的阶段编辑与逐段执行入口从详情面板移除，保留只读的阶段审计视图。

### Migration

- 历史任务无需手动迁移：带流水快照的任务自动以兼容模式显示。
- 新创建的任务使用对话式交互。
- 原有的任务数据、阶段运行记录与会话绑定保持不变；兼容视图只读，不写库。