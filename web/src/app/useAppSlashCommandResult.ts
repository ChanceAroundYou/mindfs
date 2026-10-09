import { useCallback } from "react";
import type { SlashCommandResult } from "./appSession";

/**
 * 把「当前该显示哪条 slash command 结果」这条查找规则从 App.tsx 摘出来。
 */
export type UseAppSlashCommandResultParams = {
  slashCommandResults: Record<string, SlashCommandResult>;
  rootSessionKey: (rootId: string, sessionKey: string) => string;
};

/**
 * 两级命中：给了 sessionKey 就直接查 `root::session` 复合键；没给 sessionKey 时
 * 在整张表里按 root 过滤、取 createdAt 最新的一条 —— 这一支对应「slash 结果刚写进去、
 * 会话还没选中」的窗口期，不兜的话用户会看到结果一闪而过。
 *
 * `value.sessionKey.startsWith(prefix)` 里的 prefix 是 `${resolvedRoot}::`，
 * 所以只认本 root 的结果，别的项目的同键结果不会串进来。
 */
export function useAppSlashCommandResult(params: UseAppSlashCommandResultParams) {
  const { slashCommandResults, rootSessionKey } = params;

  const slashCommandResultForSession = (
    rootID: string | null | undefined,
    session: { key?: string; session_key?: string } | null | undefined,
  ) => {
    const sessionKey = session?.key || session?.session_key || "";
    const resolvedRoot = rootID || "";
    if (!resolvedRoot) {
      return null;
    }
    if (!sessionKey) {
      let best: SlashCommandResult | null = null;
      for (const value of Object.values(slashCommandResults)) {
        if (value.rootId !== resolvedRoot || !value.sessionKey.startsWith("transient-")) {
          continue;
        }
        if (!best || (value.createdAt || 0) > (best.createdAt || 0)) {
          best = value;
        }
      }
      return best;
    }
    return slashCommandResults[rootSessionKey(resolvedRoot, sessionKey)] || null;
  };

  return {
    slashCommandResultForSession,
  };
}
