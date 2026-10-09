import { useCallback, type MutableRefObject } from "react";

import { buildURLSearch, type URLState } from "./appPath";

export type UseAppReplaceURLStateParams = {
  mainViewRef: MutableRefObject<URLState["view"]>;
  currentRootIdRef: MutableRefObject<string | null>;
  currentRootNodeIdRef: MutableRefObject<string | null>;
};

/**
 * 把「当前该长什么样的地址栏」一次性写进 history（replaceState，不压栈）。
 *
 * 主面板模式默认取当前状态：调用点一律紧跟在 switchMainView 之后（或本就不切面板），
 * 因此这里补默认值即可让 URL 与按钮高亮保持同源，无需逐个调用点传参。
 */
export function useAppReplaceURLState(params: UseAppReplaceURLStateParams) {
  const { mainViewRef, currentRootIdRef, currentRootNodeIdRef } = params;

  const replaceURLState = useCallback((next: URLState) => {
    const merged: URLState = { view: mainViewRef.current, ...next };
    // 多节点同名项目：URL 始终携带当前选中节点，防止刷新/回退还原到错误节点
    if (
      merged.root &&
      merged.root === currentRootIdRef.current &&
      currentRootNodeIdRef.current &&
      !merged.node
    ) {
      merged.node = currentRootNodeIdRef.current;
    }
    const search = buildURLSearch(merged);
    const target = `${window.location.pathname}${search}`;
    window.history.replaceState(null, "", target);
  }, []);

  return { replaceURLState };
}