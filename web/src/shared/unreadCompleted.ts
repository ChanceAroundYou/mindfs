const LEGACY_KEY = "mindfs.unreadCompleted";
const KEY = "mindfs.unreadCompleted.v2";

/**
 * 「完成未读」的键必须带节点与项目：同名项目/同名会话键在不同节点上是不同的会话，
 * 只按 sessionKey 存会让 A 节点的完成点亮 B 节点的同名会话（跨节点误标）。
 */
export function unreadKey(nodeId: string, rootId: string, sessionKey: string): string {
  return `${String(nodeId || "").trim()}::${String(rootId || "").trim()}::${String(sessionKey || "").trim()}`;
}

/** 会话项的键：_nodeId / root_id / key 三者取齐，与 unreadKey 同一形状。 */
export function unreadKeyOfSession(session: any, fallbackRootId = ""): string {
  const nodeId = String(session?._nodeId || "").trim();
  const rootId = String(session?.root_id || fallbackRootId || "").trim();
  const sessionKey = String(session?.key || session?.session_key || "").trim();
  if (!sessionKey) return "";
  return unreadKey(nodeId, rootId, sessionKey);
}

function read(): Set<string> {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return new Set();
    return new Set(JSON.parse(raw) as string[]);
  } catch {
    return new Set();
  }
}

function write(set: Set<string>) {
  try { localStorage.setItem(KEY, JSON.stringify([...set])); } catch {}
}

const listeners = new Set<(s: Set<string>) => void>();
function notify() { const s = read(); listeners.forEach((l) => l(s)); }

export function unreadCompletedAdd(key: string) {
  if (!key) return;
  const s = read();
  s.add(key);
  write(s);
  notify();
}

export function unreadCompletedRemove(key: string) {
  if (!key) return;
  const s = read();
  if (s.delete(key)) { write(s); notify(); }
}

export function unreadCompletedSubscribe(cb: (s: Set<string>) => void): () => void {
  listeners.add(cb);
  cb(read());
  const onStorage = (e: StorageEvent) => { if (e.key === KEY) cb(read()); };
  window.addEventListener("storage", onStorage);
  return () => { listeners.delete(cb); window.removeEventListener("storage", onStorage); };
}

/** v1 只按 sessionKey 存，无从推断节点/项目，直接丢弃（不迁移会把误标带过来）。 */
try { localStorage.removeItem(LEGACY_KEY); } catch {}
