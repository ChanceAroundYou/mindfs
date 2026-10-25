# 单字段化：activity_at 合并进 updated_at

状态：Plan（待批准）。

## 目标

只保留 updated_at 一个字段；流式期间前端乐观推进；后端 EndSessionTurn 触摸 updated_at。

## 实施

已完成：后端 TouchUpdatedAt + EndSessionTurn；前端删除 activity_at；merge 简化。
file_path">
/home/xiaokubao/projects/mindfs/.worktree/task-52/docs/plans/session-activity-unified.md