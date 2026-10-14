import { useCallback, useEffect, useRef, useState } from "react";
import { protectedJSON } from "../services/net/api";
import { appURL } from "../services/net/base";
import type { LocalDirBrowserState } from "../shared/projectAddTypes";

export function useFileOperationMove({
  root,
  rootPath,
  path,
  onComplete,
}: {
  root: string;
  rootPath?: string;
  path: string;
  onComplete: () => void | Promise<void>;
}) {
  const request = useRef(0);
  const busy = useRef(false);
  const [dirs, setDirs] = useState<LocalDirBrowserState>({ path: "", items: [], loading: true, selectedPath: "", adding: false, error: "" });

  const browse = useCallback(async (target: string) => {
    if (busy.current) return;
    const id = ++request.current;
    setDirs((prev) => ({ ...prev, loading: true, error: "" }));
    try {
      const result = await protectedJSON<Pick<LocalDirBrowserState, "path" | "parent" | "items" | "volumes">>(appURL("/api/local_dirs", new URLSearchParams({ path: target })));
      if (id === request.current) setDirs({ ...result, loading: false, selectedPath: "", adding: false, error: "" });
    } catch (err) {
      if (id === request.current) setDirs((prev) => ({ ...prev, loading: false, error: err instanceof Error ? err.message : String(err) }));
    }
  }, []);

  useEffect(() => {
    const parent = path.replace(/\\/g, "/").split("/").slice(0, -1).join("/");
    const initialPath = /^(?:\/|[A-Za-z]:)/.test(path)
      ? parent || "/"
      : `${(rootPath || "").replace(/[\\/]+$/, "")}/${parent}`;
    void browse(initialPath);
    return () => { request.current += 1; };
  }, [browse, path, rootPath]);

  const submit = async () => {
    if (busy.current || dirs.loading || dirs.error) return;
    busy.current = true;
    setDirs((prev) => ({ ...prev, adding: true, error: "" }));
    try {
      await protectedJSON(appURL("/api/file/operation"), {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ root, path, action: "move", destination: dirs.path }),
      });
      await onComplete();
    } catch (err) {
      setDirs((prev) => ({ ...prev, error: err instanceof Error ? err.message : String(err) }));
    } finally {
      busy.current = false;
      setDirs((prev) => ({ ...prev, adding: false }));
    }
  };

  return { dirs, browse, submit };
}
