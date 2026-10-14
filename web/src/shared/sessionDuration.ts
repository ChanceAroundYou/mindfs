// 早于这个时刻的时间戳一律当成「没有时间戳」：服务端 time.Time 的零值会序列化成
// "0001-01-01T00:00:00Z"（struct 类型的 `omitempty` 不生效），真当时刻用会把时长
// 算成两千年。
const MIN_VALID_EVENT_MS = Date.parse("2000-01-01T00:00:00Z");

/**
 * 流事件的时间：服务端给了可用时间戳就用它，否则回退到本地接收时刻。
 *
 * 为什么必须优先服务端：手机切后台再恢复时走的是**重放**路径，本地接收时刻是「恢复
 * 那一刻」而不是「事件发生那一刻」，用它算回复时长会把整段后台时间算进去。
 */
export function streamEventTimestamp(
  event: { timestamp?: string } | null | undefined,
  receivedAt: string,
): string {
  const raw = event?.timestamp;
  if (typeof raw === "string" && raw) {
    const parsed = Date.parse(raw);
    if (Number.isFinite(parsed) && parsed >= MIN_VALID_EVENT_MS) {
      return raw;
    }
  }
  return receivedAt;
}

export function formatSessionDuration(startISO: string | undefined, endISO: string | undefined): string {
  const start = Date.parse(startISO || "");
  const end = Date.parse(endISO || "");
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
    return "";
  }

  const seconds = Math.floor((end - start) / 1000);
  if (seconds <= 0) {
    return "";
  }
  if (seconds <= 100) {
    return `(${seconds}s)`;
  }
  return `(${Math.floor(seconds / 60)}m)`;
}
