import { useSyncExternalStore } from "react";
import { nodeHealthSnapshot, subscribeNodeHealth, type NodeHealthSnapshot } from "../services/net/nodeHealth";

/**
 * 订阅节点可达性。跨域共用（文件树、会话列表、空状态都会读），所以放 `hooks/`。
 *
 * 快照对象在服务层每次变更时重建一次，因此这里不需要再包一层 memo：
 * 没变更时 `nodeHealthSnapshot()` 返回同一个引用，`useSyncExternalStore` 不会重渲染。
 */
export function useNodeHealth(): NodeHealthSnapshot {
  return useSyncExternalStore(subscribeNodeHealth, nodeHealthSnapshot, nodeHealthSnapshot);
}