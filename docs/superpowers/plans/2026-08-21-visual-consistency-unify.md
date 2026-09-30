# 视觉深度统一 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans

**Goal:** 统一四处带框项目标签、中性灰底纹、选中态为浅灰长条+节点主题色文字、夜间模式补齐、低饱和六色循环

**Architecture:** 新增中性 token `--node-badge-bg` / `--node-row-selected-bg` 覆盖四主题；`NodeBadgeHeader`/`rootBadgeStyle` 去 tint 仅保留文字色为节点色；`FileTree`/`SessionList`/`SessionViewer` 选中态改用新 token；`PALETTE` 重排为紫黄蓝红灰绿低饱和并做一次旧色迁移

**Tech Stack:** React/TS, CSS variables, web/src/services/nodeRegistry

## Global Constraints
- 四主题均需覆盖: :root / @media(prefers-color-scheme:dark) / [data-theme=dark] / [data-theme=meadow] / [data-theme=moss]
- 同项目同色: 左侧项目、右侧会话项目、中间顶部项目文本色均为 _nodeColor
- 底纹统一中性灰，不随节点 tint
- 选中态: 浅中性灰长条背景 + 所属节点主题色文字

---
### Task 1: 中性 token 与 PALETTE

**Files:**
- Modify: `web/src/index.css:5-54,174-281,313-463`
- Modify: `web/src/services/nodeRegistry.ts:3,108-196`

**Interfaces:**
- Produces: CSS vars `--node-badge-bg`, `--node-row-selected-bg`; PALETTE 新顺序

- [ ] Step 1: 在 index.css 五处主题块新增中性 token
```css
--node-badge-bg: rgba(148,163,184,0.14);
--node-row-selected-bg: rgba(148,163,184,0.18);
```
- 暗色/ meadow/moss 适当提高 alpha 保证对比度
- [ ] Step 2: PALETTE 改为低饱和 紫黄蓝红灰绿
```ts
export const PALETTE = ["#7c6bd6","#c9b84a","#6a8dc2","#c66a7a","#8a8f99","#7aae8a"] as const;
```
- [ ] Step 3: getNodes() 内对旧 PALETTE 精确命中做一次性映射到新索引，未命中保留原值，写回 localStorage
- [ ] Step 4: typecheck + 手测明暗主题切换无 tint

### Task 2: 带框标签统一去 tint

**Files:**
- Modify: `web/src/components/NodeBadgeHeader.tsx:3-10,43-60`
- Modify: `web/src/components/rootBadgeStyle.ts:3-14`
- Modify: `web/src/components/FileTree.tsx:2381-2384`
- Modify: `web/src/components/SessionViewer.tsx:2383-2395`
- Modify: `web/src/App.tsx:11128-11145,13787-13810` (传参)

**Interfaces:**
- Consumes: _nodeColor from managedRootByIdRef
- Produces: 中间面板 SessionViewer 顶部项目标签带 nodeColor

- [ ] Step 1: NodeBadgeHeader.badgeBg 改为 return "var(--node-badge-bg)"
- [ ] Step 2: FileTree 2383 处 background 改为 var(--node-badge-bg)，保留 color: nodeColor
- [ ] Step 3: SessionViewer 新增 prop nodeColor?: string，顶部 button style 设 color: nodeColor || var(--root-badge-text), background: var(--node-badge-bg)
- [ ] Step 4: App.tsx 两处 SessionViewer 传入 nodeColor={managedRootByIdRef.current[rootId]?._nodeColor}
- [ ] Step 5: DefaultListView/FileViewer/GitDiffViewer 的面包屑 root 保持中性，不跟节点色（避免误伤）

### Task 3: 选中态统一为浅灰条+节点色文字

**Files:**
- Modify: `web/src/components/FileTree.tsx:2286-2353` (renderEntries)
- Modify: `web/src/components/SessionList.tsx:87-107,1216-1220,1487-1522,1173-1200` (SessionCard 新增 nodeColor)
- Modify: `web/src/App.tsx:4608-4620` (透传 _nodeColor 到会话分组)

**Interfaces:**
- Consumes: group._nodeColor / entry._nodeColor
- Produces: selected 时 background var(--node-row-selected-bg), color nodeColor

- [ ] Step 1: FileTree 中将 renderEntries 签名增加 groupColor 参透传，或直接取 entry._nodeColor；isSelected 时 background -> var(--node-row-selected-bg), color -> nodeColor
- [ ] Step 2: SessionList SessionCard 新增 prop nodeColor?: string，rowBackground -> var(--node-row-selected-bg)，标题 color -> selected?nodeColor:var(--text-primary)
- [ ] Step 3: MultiProjectSessionList 中将 group._nodeColor 传给 SessionCardMemo
- [ ] Step 4: 单列表 SessionList 选中态同样走中性 token（若无 nodeColor 则回落 var(--accent-color) 兼容）
- [ ] Step 5: 验证浅灰条在暗色/ meadow/moss 下可见且不刺眼

### Task 4: 验证与回归

- [ ] `yarn typecheck` 通过
- [ ] `make build-web` 产物正常
- [ ] 手测: 左项目/右会话项目/中顶部项目同色；所有带框底纹为中性灰无 tint；选中文件/会话为浅灰条+节点色字；四主题切换正常；新建节点颜色按新 PALETTE 顺序

