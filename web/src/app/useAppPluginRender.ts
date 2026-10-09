import { useMemo, type CSSProperties } from "react";

import { toPluginInput } from "./appMisc";
import type { FilePayload } from "../services/file/types";
import type { PluginManager, ViewPlugin } from "../plugins/manager";

export type UseAppPluginRenderParams = {
  file: FilePayload | null;
  pluginBypass: boolean;
  matchedPlugin: ViewPlugin | null;
  pluginQuery: Record<string, string>;
  pluginManagerRef: React.RefObject<PluginManager>;
};

/**
 * 把当前文件交给插件跑一遍，并把插件声明的主题变量转成 CSS 自定义属性。
 *
 * `pluginRender` 每次文件或查询参数变了都重跑插件进程，`pluginThemeVars`
 * 直接由它的结果派生 —— 所以两者必须同一次渲染里算出来，拆开会让主题变量
 * 落后一帧（插件换主题时闪一下）。
 *
 * 插件进程抛错不往上抛，转成 `{ output: null, error }`：插件是用户装的第三方
 * 代码，崩了不该把整个文件视图带走，只在面板里显示错误。
 */
export function useAppPluginRender(params: UseAppPluginRenderParams) {
  const { file, pluginBypass, matchedPlugin, pluginQuery, pluginManagerRef } =
    params;

  const pluginRender = useMemo(() => {
    if (!file || pluginBypass || !matchedPlugin) return null;
    const input = toPluginInput(file, pluginQuery);
    try {
      const output = pluginManagerRef.current.run(matchedPlugin, input);
      return { plugin: matchedPlugin, output, error: "" };
    } catch (err: any) {
      return {
        plugin: matchedPlugin,
        output: null,
        error: String(err?.message || err || "plugin process failed"),
      };
    }
  }, [file, pluginBypass, matchedPlugin, pluginQuery]);

  const pluginThemeVars = useMemo(() => {
    const theme = pluginRender?.plugin?.theme;
    if (!theme) return null;
    return {
      "--vp-overlay-bg": theme.overlayBg,
      "--vp-surface-bg": theme.surfaceBg,
      "--vp-surface-bg-elevated": theme.surfaceBgElevated,
      "--vp-text": theme.text,
      "--vp-text-muted": theme.textMuted,
      "--vp-border": theme.border,
      "--vp-primary": theme.primary,
      "--vp-primary-text": theme.primaryText,
      "--vp-radius": theme.radius,
      "--vp-shadow": theme.shadow,
      "--vp-focus-ring": theme.focusRing,
      "--vp-danger": theme.danger,
      "--vp-warning": theme.warning,
      "--vp-success": theme.success,
    } as CSSProperties;
  }, [pluginRender]);

  return { pluginRender, pluginThemeVars };
}