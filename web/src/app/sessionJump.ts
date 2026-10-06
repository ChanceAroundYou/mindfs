/**
 * 会话跳转的 root 归属解析（CUSTOM(G-Z)）。
 *
 * 会话窗口/列表响应体**不带 root_id**（root 只是请求参数），所以「这次跳转属于哪个项目」
 * 必须由调用方钉死：卡片/文件视图声明的 root 优先，其次会话自身记的 root，最后才退回当前根。
 * 少了这层，第二次点同一张卡片会命中内存缓存里那份无 root 的会话对象，归属退化成「当前根」，
 * 请求打到错误项目上 404 —— 用户看到的就是「点了跳到别的项目里的同名空白会话」。
 */

export type SessionJumpSession = {
  key?: string;
  session_key?: string;
  root_id?: string;
  task_id?: string;
  [k: string]: unknown;
};

/** 跳转目标根：显式 root（卡片/文件视图）优先；缺失才退回会话自带 root，最后才是当前根。 */
export function resolveSessionJumpRoot(
  explicitRoot: string | null | undefined,
  session: SessionJumpSession | null | undefined,
  currentRoot: string | null | undefined,
): string {
  return String(explicitRoot || session?.root_id || currentRoot || "");
}

/** 组装跳转会话对象：无论命中缓存/列表，root_id 一律强制为解析出的 root；无 key/root 返回 null。 */
export function buildSessionJumpTarget(params: {
  sessionKey: string;
  rootOverride?: string | null;
  taskId?: string;
  matched?: SessionJumpSession | null;
  cached?: SessionJumpSession | null;
  currentRoot?: string | null;
}): (SessionJumpSession & { key: string; session_key: string; root_id: string }) | null {
  const sessionKey = String(params.sessionKey || "").trim();
  if (!sessionKey) {
    return null;
  }
  const matched = params.matched || null;
  const cached = params.cached || null;
  const root = resolveSessionJumpRoot(
    params.rootOverride,
    cached || matched,
    params.currentRoot,
  );
  if (!root) {
    return null;
  }
  // 缓存优先（它带 exchanges 等重字段），matched 只用来补缓存没有的元数据（name/model）。
  const base = { ...(matched || {}), ...(cached || {}) } as SessionJumpSession;
  return {
    ...base,
    key: sessionKey,
    session_key: sessionKey,
    root_id: root,
    ...(params.taskId ? { task_id: params.taskId } : {}),
  };
}
