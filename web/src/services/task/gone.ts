/**
 * 「这张任务在服务端已经不存在了」的判据。
 *
 * 看板上的卡可能是幽灵：服务端删掉之后客户端没收到 `task.deleted`（删它的是
 * 另一台设备、或者当时本页断线），IndexedDB 缓存又会把这张卡继续喂回来。
 * 此时任何任务级动作都打在一条不存在的行上，服务端原样回 `sql.ErrNoRows`：
 *
 *   POST   /api/tasks/{id}/complete  → 400 {"error":"sql: no rows in result set"}
 *   DELETE /api/tasks/{id}?root_id=  → 404 {"error":"sql: no rows in result set"}
 *
 * 只认这句话、不认状态码：delete 按 ErrNoRows 回 404，而 move 系列
 * （complete / cancel / reopen / pause）统一回 400，同一个事实两种码；反过来
 * 404 也可能是「root 没找到 / 路由不存在」，单凭它会把好卡误删。
 *
 * `name` 走结构化匹配而不是 `instanceof APIError`：这个判据要被组件层复用，
 * 不想为一个字符串比较把 net/api 整条依赖链（bootstrap / authGate）拖进来。
 * APIError 构造时就写了 `name = "APIError"`。
 */
export function isTaskGoneError(error: unknown): boolean {
  if (!error || typeof error !== "object") return false;
  const candidate = error as { name?: unknown; message?: unknown };
  if (candidate.name !== "APIError") return false;
  return /no rows in result set/i.test(String(candidate.message || ""));
}
