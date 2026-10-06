import { useEffect, useMemo, useRef, useState } from "react";
import { fetchRelatedFileStatsBatch } from "../services/git";

export type RelatedFileStat = {
  status: string;
  additions: number;
  deletions: number;
};

export type RelatedFileStatTarget = {
  path: string;
  head?: string;
  repo_path?: string;
  repo_kind?: string;
};

export function relatedFileStatKey(file: RelatedFileStatTarget): string {
  return [
    file.repo_kind || "",
    file.repo_path || "",
    file.head || "",
    file.path,
  ].join("\0");
}

/**
 * refreshKey 变化后的合并窗口。
 *
 * refreshKey 由 git status 派生，而 git status 是**每次文件变更**都要重拉的，于是
 * agent 干活时它会连着变。窗口取 300ms，与 scheduleMultiProjectSessionReload 同档：
 * 足够把一串变更合并成一次请求，又短到用户感知不到徽标延迟。
 */
const RELATED_FILE_STATS_DEBOUNCE_MS = 300;

export function useRelatedFileStats(
  rootId: string | null | undefined,
  files: RelatedFileStatTarget[],
  refreshKey = "",
  nodeId?: string,
): Record<string, RelatedFileStat> {
  const filesSignature = useMemo(
    () =>
      files
        .filter((file) => file.path && file.repo_kind !== "plain")
        .map((file) => relatedFileStatKey(file))
        .sort()
        .join("\n"),
    [files],
  );
  const [statsByKey, setStatsByKey] = useState<Record<string, RelatedFileStat>>({});

  // 上一轮的请求参数：只有「这批文件真的换了」才清空已显示的徽标。
  // 单纯 refreshKey 变化（git status 变了）不该让徽标闪一下再回来。
  const activeSignatureRef = useRef("");

  useEffect(() => {
    const targets = Array.from(
      new Map(
        files
          .filter(
            (file) =>
              file.path &&
              file.repo_kind !== "plain" &&
              // A recorded base or repository is required to recover a diff
              // after the main working tree becomes clean.
              Boolean(file.head || file.repo_path),
          )
          .map((file) => [relatedFileStatKey(file), file]),
      ).entries(),
    ).map(([id, file]) => ({
      id,
      path: file.path,
      head: file.head,
      repo_path: file.repo_path,
      repo_kind: file.repo_kind,
    }));

    if (!rootId || targets.length === 0) {
      activeSignatureRef.current = "";
      setStatsByKey({});
      return;
    }

    const signature = targets.map((target) => target.id).join("\u0001");
    if (activeSignatureRef.current !== signature) {
      // 换了一批文件：旧徽标对新列表没有意义，先清掉再拉。
      activeSignatureRef.current = signature;
      setStatsByKey({});
    }

    let cancelled = false;
    const timer = window.setTimeout(() => {
      void fetchRelatedFileStatsBatch(rootId, targets, nodeId || undefined)
        .then((next) => {
          if (cancelled) return;
          // 合并而不是整体替换：与本仓库 mergeReplyingStateByNode 的同一原则 ——
          // 只覆盖本轮真的拿到的键，拉失败时不要把已在显示的徽标抹掉。
          setStatsByKey((prev) => ({ ...prev, ...next }));
        })
        .catch(() => {
          // 徽标是装饰性的，拉不到就维持现状，不打扰用户。
        });
    }, RELATED_FILE_STATS_DEBOUNCE_MS);

    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [filesSignature, refreshKey, rootId, nodeId]);

  return statsByKey;
}
