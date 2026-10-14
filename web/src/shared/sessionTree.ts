/**
 * 会话树的纯计算：子树收集 + 子会话展开态回收。
 *
 * 放这里而不是塞进 SessionList.tsx / App.tsx：两个 App 状态树（sessions 与
 * multiProjectSessionGroups）都要用到子树收集，删除和归档也要共用同一份，
 * 而组件文件无法被 tests/*.test.mjs 直接 import（见 session-list-merge.test.mjs
 * 的转译加载方式）。
 */

export type SessionTreeItem = {
  key?: string;
  session_key?: string;
  parent_session_key?: string;
};

function itemKeyOf(item: SessionTreeItem): string {
  return String(item?.key || item?.session_key || "");
}

function parentKeyOf(item: SessionTreeItem): string {
  return String(item?.parent_session_key || "").trim();
}

/**
 * 收集 rootKey 及其**全部后代**（子会话）的 key 集合。
 *
 * 后端 DELETE /api/sessions/{key} 已按 parent_session_key 级联删掉整棵子树，
 * 本地状态必须摘掉同一批 key —— 漏掉的子会话会被树构建判成「父不在集合里」而
 * 提升成顶层整宽行，面板因此撑爆且无法收起。
 *
 * 传入的 items 必须覆盖**所有**本地会话来源（当前项目列表 + 各多项目分组），
 * 只传其中之一就会漏摘。
 *
 * 不跟随 fork 会话：fork 只是 source 里记了来源，不是父子关系，
 * 删父不该删 fork（与后端 TestDeleteSessionKeepsForkSession 一致）。
 * 本函数按 parent_session_key 判定，fork 走这条路自然不会被收进来。
 */
export function collectSessionSubtreeKeys(
  items: SessionTreeItem[],
  rootKey: string,
): string[] {
  const root = String(rootKey || "").trim();
  if (!root) {
    return [];
  }
  const childrenByParent = new Map<string, string[]>();
  for (const item of items) {
    const parent = parentKeyOf(item);
    const key = itemKeyOf(item);
    if (!parent || !key) {
      continue;
    }
    const bucket = childrenByParent.get(parent);
    if (bucket) {
      bucket.push(key);
    } else {
      childrenByParent.set(parent, [key]);
    }
  }
  const out: string[] = [];
  const seen = new Set<string>();
  const visit = (key: string) => {
    if (seen.has(key)) {
      return;
    }
    seen.add(key);
    for (const child of childrenByParent.get(key) || []) {
      visit(child);
    }
    out.push(key);
  };
  visit(root);
  return out;
}

/**
 * 回收子会话展开态：丢掉 stateKey 对应会话已不在 liveKeys 里的条目。
 *
 * stateKey 形如 `nodeId::rootId:sessionKey`。这些 map 从不清理，父会话删除后条目仍在，
 * 同 key 的会话再次出现（重名导入、repoint、同步重建）就立刻沿用「已展开」，
 * 一次性铺出全部子会话 —— 即「删完收不回去」的成因；条目也会随操作数无限累积。
 *
 * 判定用 liveKeys 权威集合而非解析 stateKey 字符串：前缀里同时含 `::` 和 `:`，
 * 按分隔符切不可靠。
 */
export function pruneChildState<T>(
  state: Record<string, T>,
  liveKeys: Set<string>,
): Record<string, T> {
  const next: Record<string, T> = {};
  let changed = false;
  for (const [stateKey, value] of Object.entries(state)) {
    const colon = stateKey.lastIndexOf(":");
    const sessionKey = colon >= 0 ? stateKey.slice(colon + 1) : stateKey;
    if (liveKeys.has(sessionKey)) {
      next[stateKey] = value;
    } else {
      changed = true;
    }
  }
  return changed ? next : state;
}
