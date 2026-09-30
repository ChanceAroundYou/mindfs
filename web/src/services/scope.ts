// 多节点作用域复合键：空 nodeId 时输出与历史单节点格式逐字节一致（零迁移、旧数据兼容）。
// 键段规则：根级复合键用 "::"（n::r）；会话键用 "::"（n::r::k）；目录/子项用单 ":"（n::r:path）。
// 2 段键只可能由空 nodeId 产生、3 段键只可能由非空 nodeId 产生，二者永不相等。
const S = "::"; // 作用域分隔符（根、会话）
const D = ":"; // 目录分隔符（沿袭历史格式）

function seg(value: string | null | undefined): string {
  return String(value ?? "").trim();
}

/** 根级复合键：空 nodeId → 裸 rootId（历史格式） */
export function scopeKey(nodeId: string | null | undefined, rootId: string): string {
  const n = seg(nodeId);
  const r = seg(rootId);
  return n ? `${n}${S}${r}` : r;
}

/** 会话复合键：空 nodeId → root::key（历史格式） */
export function scopeSessionKey(
  nodeId: string | null | undefined,
  rootId: string,
  sessionKey: string,
): string {
  const n = seg(nodeId);
  const r = seg(rootId);
  const k = String(sessionKey ?? "");
  return n ? `${n}${S}${r}${S}${k}` : `${r}${S}${k}`;
}

/** 目录复合键：base 为 scopeKey，子目录再加 ":path"（"."/空 视为根目录本身） */
function scopedDirKey(nodeId: string | null | undefined, rootId: string, path: string): string {
  const base = scopeKey(nodeId, rootId);
  const p = String(path ?? "");
  return p && p !== "." ? `${base}${D}${p}` : base;
}

/** 目录树缓存键（childrenByPath / entriesByPath），. 表示根目录 */
export function treeKey(
  nodeId: string | null | undefined,
  rootId: string,
  dir: string,
): string {
  return scopedDirKey(nodeId, rootId, dir || ".");
}

/** 文件树展开键：根 = scopeKey，子目录 = scopeKey:path */
export function expandKey(
  nodeId: string | null | undefined,
  rootId: string,
  path: string,
  isRoot: boolean,
): string {
  return isRoot ? scopeKey(nodeId, rootId) : scopedDirKey(nodeId, rootId, path);
}

/** 选中目录键：与展开键同构 */
export function dirSelKey(
  nodeId: string | null | undefined,
  rootId: string,
  path: string,
  isRoot: boolean,
): string {
  return isRoot ? scopeKey(nodeId, rootId) : scopedDirKey(nodeId, rootId, path);
}

/**
 * 判断一个会话复合键属于哪个节点。
 *
 * 用途：多节点刷新时只重算「本轮成功拉到的那几个节点」的键，其余节点的值原样保留
 * （节点不可达时把它的条目当成没有在回复 = 灯被误灭；切节点时整体替换 = 灯全灭）。
 * 段数规则见文件头——2 段必是空 nodeId，3 段必是非空 nodeId，故按段数判定不会误伤。
 */
export function sessionKeyNodeId(sessionScopeKey: string | null | undefined): string {
  const k = String(sessionScopeKey ?? "");
  if (!k) return "";
  const parts = k.split(S);
  return parts.length >= 3 ? parts[0] : "";
}

/** 会话键别名 */
export function sessionScope(
  nodeId: string | null | undefined,
  rootId: string,
  sessionKey: string,
): string {
  return scopeSessionKey(nodeId, rootId, sessionKey);
}
