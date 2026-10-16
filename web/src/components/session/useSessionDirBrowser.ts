import { useCallback, useEffect, useState } from "react";
import { appURL } from "../../services/net/base";
import { protectedJSON } from "../../services/net/api";
import { useI18n } from "../../i18n/index";
import type { LocalDirBrowserState, LocalDirItem } from "../../shared/projectAddTypes";

/**
 * 会话标题栏「修改目录」用到的目录访问逻辑。
 *
 * 放在 components/session/ 下就地 colocate（单域逻辑），而不是写进 .tsx：
 * 视图层不许 import services/（docs/agent/rules/frontend/layering.md 铁律①）。
 *
 * 两个能力各自独立：
 *   - useSessionDirExists：菜单里那行路径要不要标红。目录不存在时 /api/local_dirs
 *     报 404，所以不需要另加一个存在性接口。
 *   - useSessionDirBrowser：选路径弹层的浏览状态。只保留「浏览 + 选中一个目录」，
 *     刻意不复用 useProjectDirectoryBrowser —— 那个 hook 绑着 projectAddMode /
 *     项目注册表刷新 / 建 worktree 一串只对「新建项目」成立的分支。
 */

type LocalDirsPayload = {
  path?: string;
  parent?: string;
  volumes?: LocalDirItem[];
  items?: LocalDirItem[];
};

async function fetchLocalDirs(path: string, nodeId?: string): Promise<LocalDirsPayload> {
  const trimmed = String(path || "").trim();
  const params = trimmed ? new URLSearchParams({ path: trimmed }) : undefined;
  return await protectedJSON<LocalDirsPayload>(appURL("/api/local_dirs", params, nodeId));
}

/**
 * 探测一个目录还在不在。enabled=false（没开菜单 / 目录是项目主 checkout）时不发请求。
 * 返回值只在 enabled 时可信，调用方别把它当全局缓存用。
 */
export function useSessionDirExists(path: string, enabled: boolean, nodeId?: string): boolean {
  const [missing, setMissing] = useState(false);
  useEffect(() => {
    if (!enabled || !path) {
      setMissing(false);
      return;
    }
    let cancelled = false;
    void fetchLocalDirs(path, nodeId)
      .then(() => {
        if (!cancelled) setMissing(false);
      })
      .catch(() => {
        if (!cancelled) setMissing(true);
      });
    return () => {
      cancelled = true;
    };
  }, [path, enabled, nodeId]);
  return missing;
}

export function useSessionDirBrowser(initialPath: string, nodeId?: string) {
  const { t } = useI18n();
  const [state, setState] = useState<LocalDirBrowserState>({
    path: initialPath,
    parent: "",
    volumes: [],
    items: [],
    loading: true,
    selectedPath: "",
    adding: false,
    error: "",
  });
  const [input, setInput] = useState(initialPath);

  const load = useCallback(
    async (path: string) => {
      const trimmed = String(path || "").trim();
      setInput(trimmed);
      setState((prev) => ({ ...prev, loading: true, error: "", path: trimmed }));
      try {
        const payload = await fetchLocalDirs(trimmed, nodeId);
        setState({
          path: String(payload.path || trimmed),
          parent: String(payload.parent || ""),
          volumes: Array.isArray(payload.volumes) ? payload.volumes : [],
          items: Array.isArray(payload.items) ? payload.items : [],
          loading: false,
          selectedPath: "",
          adding: false,
          error: "",
        });
      } catch (err) {
        setState((prev) => ({
          ...prev,
          loading: false,
          items: [],
          error: err instanceof Error ? err.message : t("session.dirPicker.loadFailed"),
        }));
      }
    },
    [nodeId, t],
  );

  useEffect(() => {
    void load(initialPath);
    // 只在挂载时按默认目录拉一次；之后由用户导航 / 输入驱动。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { state, input, setInput, load };
}
