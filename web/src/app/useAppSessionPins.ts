import { useCallback, useMemo, useSyncExternalStore } from "react";

import { readPins, subscribePins } from "../services/pins";
import { applyPinnedSnapshotToSessions as applyPinSnapshotRaw } from "../services/session/sessionListMerge";

/**
 * 会话置顶覆盖层（2026-10-05 的「排序决策权只在这里」）。
 *
 * 置顶只用 store 一份（services/pins.ts），按**项目 id**（不含节点）分桶。
 *
 * 为什么 worker 的列表也要盖：置顶是控制面，worker 上 /api/pins 是 403，
 * 列表里没有置顶区。用户看到的是主节点那张表，所以从这里盖过去。
 *
 * 键为什么是纯 rootId：置顶是跨设备的一致性偏好，PC 上的会话在 home 置顶，
 * 就要在 PC 的列表里显示为置顶 —— 键里带节点 id 就等于又按机器分片了。
 *
 * **排序决策权只在这里**（决策 2026-10-05）：原先第三个参数（列表回包自带的
 * pinnedKeys）也参与排序，于是两个数据源都能重排列表、谁后到谁说了算 ——
 * 那是右侧狂跳的来源之一。现在列表回包只管数据，置顶一律从 store 取。
 *
 * 整个 hook 无参：store 订阅是模块级的（services/pins），App 里那份
 * useSyncExternalStore 只被这个 memo 用，所以一并搬了进来。
 */
export function useAppSessionPins() {
  const pinsSnapshot = useSyncExternalStore(subscribePins, readPins, readPins);

  const sessionPinsByRoot = useMemo(() => {
    const byRoot = new Map<string, Map<string, string>>();
    for (const [scoped, at] of Object.entries(pinsSnapshot.sessions || {})) {
      // 键形如 rootID::sessionKey：只取第一段当项目 id，其余整段当会话 key。
      // 项目 id 本身不含 "::"（basename），所以按第一处分隔切是安全的。
      const sep = scoped.indexOf("::");
      if (sep <= 0) continue;
      const rootId = scoped.slice(0, sep);
      const sessionKey = scoped.slice(sep + 2);
      if (!sessionKey) continue;
      let bucket = byRoot.get(rootId);
      if (!bucket) {
        bucket = new Map<string, string>();
        byRoot.set(rootId, bucket);
      }
      bucket.set(sessionKey, at);
    }
    return byRoot;
  }, [pinsSnapshot]);

  const applyPinSnapshot = useCallback(
    <T extends { root_id?: string; pinned_at?: string | null }>(
      items: T[],
      rootId: string,
    ): T[] => {
      const overlay = sessionPinsByRoot.get(rootId);
      if (!overlay || overlay.size === 0) {
        return applyPinSnapshotRaw(items, rootId, []);
      }
      return applyPinSnapshotRaw(items, rootId, Array.from(overlay.keys()), overlay);
    },
    [sessionPinsByRoot],
  );

  return { applyPinSnapshot };
}
