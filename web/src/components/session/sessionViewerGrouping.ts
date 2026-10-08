import type { RelatedFile } from "../../services/session";

export function groupRelatedFiles(displayFiles: RelatedFile[], rootPath: string | null | undefined, t: (key: any) => string) {
  return (() => {
    const currentRootPath = String(rootPath || "").replace(/[\\/]+$/, "");
    const repoGroups = displayFiles.reduce<
      Array<{
        key: string;
        repoPath: string;
        repoName: string;
        repoKind: string;
        headGroups: Array<{ key: string; head: string; files: typeof displayFiles }>;
      }>
    >((groups, file) => {
    const head = file.head || "";
    const rawRepoPath = file.repo_path || "";
    const normalizedRepoPath = String(rawRepoPath || "").replace(/[\\/]+$/, "");
    const isCurrentRepoRecord =
      !rawRepoPath ||
      file.repo_name === t("session.currentProject") ||
      (!!currentRootPath && normalizedRepoPath === currentRootPath);
    const repoPath = isCurrentRepoRecord ? "" : rawRepoPath;
    const rawRepoKind = file.repo_kind || "";
    const repoKind = isCurrentRepoRecord && rawRepoKind !== "plain" ? "" : rawRepoKind;
    const repoKey = `${repoKind}\0${repoPath}`;
    let repoGroup = groups.find((group) => group.key === repoKey);
    if (!repoGroup) {
      repoGroup = {
        key: repoKey,
        repoPath,
        repoName: isCurrentRepoRecord
          ? t("session.currentProject")
          : file.repo_name || repoPath.split(/[\\/]/).filter(Boolean).pop() || t("session.currentProject"),
        repoKind,
        headGroups: [],
      };
      groups.push(repoGroup);
    }
    const headKey = `${repoKey}\0${head}`;
    const existing = repoGroup.headGroups.find((group) => group.key === headKey);
    if (existing) {
      existing.files.push(file);
    } else {
      repoGroup.headGroups.push({
        key: headKey,
        head,
        files: [file],
      });
    }
    return groups;
    }, []);
    return repoGroups.flatMap((repoGroup) =>
      repoGroup.headGroups.map((headGroup) => ({
        key: headGroup.key,
        head: headGroup.head,
        repoPath: repoGroup.repoPath,
        repoName: repoGroup.repoName,
        repoKind: repoGroup.repoKind,
        files: headGroup.files,
      })),
    );
  })();
}
