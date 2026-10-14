# MindFS 开发规范

> 本目录是代码组织与开发流程规则的唯一正文。架构事实、安全边界与部署权限以仓库根 `AGENTS.md` 为准。

| 目录 | 范围 | 索引 |
|---|---|---|
| `common/` | 两端通用（提交规范、fork 定制） | 见下方 |
| `frontend/` | `web/src` + `web/tests` | [README](frontend/README.md) |
| `backend/` | `server/` + `cli/`（Go） | [README](backend/README.md) |

## common

- [提交与 fork 定制](common/commit-and-customization.md) — Conventional Commits（中文）、`Scope: G-X` 与 upstream customization 登记要求。

## 执行方式

- 规则对新代码和存量代码统一适用；Active 计划可定义本次工作的范围与验收，不能豁免规则。
- 自动门禁覆盖可机械判定的要求；Reviewer 清单覆盖需要结合行为或架构事实判断的要求。
- 具体任务可以增加约束或缩小范围，不能降低仓库规则。
