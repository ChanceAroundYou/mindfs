import { useEffect, useMemo, useState } from "react";
import { fetchGitRelatedFileDiff } from "../services/git";

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
    );
    if (!rootId || targets.length === 0) {
      setStatsByKey({});
      return;
    }

    let cancelled = false;
    void Promise.all(
      targets.map(async ([key, file]) => {
        try {
          // 关联文件按其所属会话的节点路由：同名根在多节点上重名时，裸 rootId 查表
          // 会把其它节点文件的 diff 请求串到当前节点（实测 400：repo_path 是另一台机器路径）
          const diff = await fetchGitRelatedFileDiff(rootId, file, nodeId || undefined);
          // 后端判定「自记录基线以来该文件没有任何变更」时 source=none、status 为空，
          // 此时不该显示 +0 -0 徽标。
          if (!diff.status) return null;
          return [
            key,
            {
              status: diff.status,
              additions: diff.additions,
              deletions: diff.deletions,
            },
          ] as const;
        } catch {
          return null;
        }
      }),
    ).then((results) => {
      if (cancelled) return;
      setStatsByKey(
        Object.fromEntries(
          results.filter(
            (entry): entry is NonNullable<typeof entry> => entry !== null,
          ),
        ),
      );
    });

    return () => {
      cancelled = true;
    };
  }, [filesSignature, refreshKey, rootId, nodeId]);

  return statsByKey;
}
