import { useCallback } from "react";
import { unscopedRootId } from "./appUtils";
import type { SessionItem } from "./appSession";
import type { Session } from "../services/session/sessionTypes";

/**
 * 反查「某个裸会话键属于哪个项目」。
 */
export type UseAppRootResolutionParams = {
  currentRootIdRef: React.RefObject<string | null>;
  sessionCacheRef: React.RefObject<Record<string, Session>>;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
  boundSessionByRootRef: React.RefObject<Record<string, string | null>>;
  drawerSessionByRootRef: React.RefObject<Record<string, SessionItem | null>>;
};

/**
 * 顺序即优先级：当前项目命中就直接用它；否则扫「每个项目绑定的那个会话键」
 * （boundSessionByRootRef），再扫「每个项目抽屉里那个会话」（drawerSessionByRootRef），
 * 最后才退到缓存键的 `::` 后缀匹配。
 *
 * 两个 ref 表里的键都是 scoped（`node::root`），所以命中后必须过 unscopedRootId
 * 剥掉节点前缀 —— 直接返回会让调用方拿着 scoped 键去查 registry，查不到。
 */
export function useAppRootResolution(params: UseAppRootResolutionParams) {
  const { currentRootIdRef, sessionCacheRef, rootSessionKey, boundSessionByRootRef, drawerSessionByRootRef } = params;

  const resolveRootForSessionKey = useCallback(
    (sessionKey: string): string | null => {
      if (!sessionKey) return null;
      const currentRoot = currentRootIdRef.current;
      if (
        currentRoot &&
        sessionCacheRef.current[rootSessionKey(currentRoot, sessionKey)]
      ) {
        return currentRoot;
      }
      for (const [rootID, key] of Object.entries(
        boundSessionByRootRef.current,
      )) {
        if (key === sessionKey) {
          return unscopedRootId(rootID);
        }
      }
      for (const [rootID, session] of Object.entries(
        drawerSessionByRootRef.current,
      )) {
        if (session?.key === sessionKey) {
          return unscopedRootId(rootID);
        }
      }
      const suffix = `::${sessionKey}`;
      const matched = Object.keys(sessionCacheRef.current).find((key) =>
        key.endsWith(suffix),
      );
      if (!matched) return null;
      return unscopedRootId(matched.slice(0, matched.length - suffix.length));
    },
    [rootSessionKey],
  );

  return {
    resolveRootForSessionKey,
  };
}
