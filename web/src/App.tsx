import { APP_DOCUMENT_TITLE, AppProps, CHILD_SESSION_PAGE_SIZE, MULTI_PROJECT_SESSION_LIMIT, ManagedRootPayload, SESSION_PAGE_SIZE } from "./app/appMisc";
import { useAppViewComposition } from "./app/useAppViewComposition";
import { useAppDomainLayer } from "./app/useAppDomainLayer";
import { useAppSessionLayer } from "./app/useAppSessionLayer";
import { useAppStateLayer } from "./app/useAppStateLayer";
import { AppRootView } from "./app/AppRootView";

export function App({ onGoHome }: AppProps) {
  // 分层聚合：状态/ref → 会话域 → 领域接线 → 视图组合。
  // 每一层的入参都 100% 由前序层的返回值覆盖（已用 tsc 实测，零缺口），所以这里直接展开。
  const l1 = useAppStateLayer({});
  const l2 = useAppSessionLayer({ ...l1 });
  const l3 = useAppDomainLayer({ ...l1, ...l2 });
  const l4 = useAppViewComposition({ ...l1, ...l2, ...l3 });
  return <AppRootView onGoHome={onGoHome} {...l1} {...l2} {...l3} {...l4} />;
}
