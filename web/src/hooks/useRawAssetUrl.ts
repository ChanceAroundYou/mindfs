import { useMemo } from "react";
import { buildRawAssetURL } from "../services/file";

/**
 * 路径式原始资源 URL（HTML 预览 / 视频播放用）。
 *
 * 组件层不许直接 import services（web/tests/reviewer-architecture-rules.test.mjs），
 * 所以这一步由 hook 兜住：组件只管要一个 URL。
 */
export function useRawAssetUrl(rootId?: string, path?: string, nodeId?: string): string {
  return useMemo(
    () => (rootId && path ? buildRawAssetURL({ rootId, path, nodeId }) : ""),
    [rootId, path, nodeId],
  );
}
