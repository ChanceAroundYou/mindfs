import type {
  SessionEventHandler,
  SessionServiceEvent,
  StreamEvent,
} from "./sessionTypes";

// 无人订阅时缓存 `session.stream` 事件的**容量上限**。
//
// 这些事件是给「还没挂上来的订阅者」留的（事件可能早于 React 订阅到达）。此前只 push、
// 没有阈值 —— 长回合 + 用户始终不打开那个会话 = 内存无界增长（冲突⑬）。
//
// 保留**最近** N 条即可：挂载会话时会做**整轮快照重建**（`session.ready` → reset 帧，
// 服务端把当前回合的事件整份重发），所以丢掉更早的那些不影响正确性。
const PENDING_STREAM_LIMIT = 200;

/**
 * 订阅登记 + 事件派发：谁订了哪个会话、批处理帧按什么形状展开、派给谁。
 *
 * 从 `SessionService` 抽出（2026-10-08）：这一层不碰 socket、不碰 pendingMessages，
 * 只认「一帧已经解完密的事件」。所以它不需要 WS 状态，也不该和重连计时器待在一个类里。
 */
export class SessionDispatcher {
  private handlers = new Map<string, Set<SessionEventHandler>>();
  private pendingStreams = new Map<string, StreamEvent[]>();
  private activeStreams = new Set<string>();
  private listeners = new Set<(event: SessionServiceEvent) => void>();

  private readonly getNodeId: () => string | null;

  constructor(getNodeId: () => string | null) {
    this.getNodeId = getNodeId;
  }

  /** 当前 socket 归属的节点。派发时读一次 —— 它会随 connect/切节点变。 */
  private get nodeId(): string | null {
    return this.getNodeId();
  }

  emitDecrypted(
    type: string,
    sessionKey: string,
    payload: Record<string, unknown>,
    msg: any,
  ) {
    const nextPayload: Record<string, unknown> = { ...payload };
    // 前端传输级打标：后端暂不发 _nodeId 时，以当前 socket 归属节点兜底，使 App 侧守卫生效
    const socketNid = String(this.nodeId || "").trim();
    if (socketNid && !nextPayload["_nodeId"] && !nextPayload["nodeId"]) {
      (nextPayload as any)["_nodeId"] = socketNid;
    }
    // **形状归一：批处理帧在这里拆开，不要让两条分发路径各认一种形状。**
    // 服务端挂载会话时发的是一条整批 `{reset:true, events:[…]}`（见 stream_hub.go 的
    // buildSessionStreamBatchResponse）。曾经只有全局 listeners 那条路认识 `events` 数组，
    // 而 per-session 的 `onStream` 读 `payload.event` —— 于是每个批处理帧都让
    // useSessionStream 抛一次 `Cannot read properties of undefined (reading 'type')`，
    // 重放期间的状态机（isStreaming/streamVersion）整个瞎掉。
    //
    // 现在统一在这里展开：先广播一次 `session.stream.reset`（缓存侧据此**清空瞬时尾巴**，
    // 这就是「快照重建」的落点），再把每个事件各自走完整的 emit + handlers 流程。
    // 同步循环 → React 批处理成一次渲染，服务端做整批的初衷（不要几百次渲染）不变。
    //
    // 判据只看**形状**（`events` 是不是数组），不看 `reset` 标志 —— 两者是不同的东西：
    // 形状决定「怎么展开」，`reset` 只决定「展开前要不要清尾巴」。绑在一起的话，
    // 任何一个不带 `reset` 的批帧都会漏进下面那条 `payload.event` 路径上，重新变成
    // `onStream(undefined)`。分开之后，旧服务端的批帧也能被正确展开。
    if (type === "session.stream" && Array.isArray(nextPayload.events)) {
      if (nextPayload.reset === true) {
        this.emit({ type: "session.stream.reset", sessionKey, payload: nextPayload });
      }
      for (const one of nextPayload.events as StreamEvent[]) {
        const single: Record<string, unknown> = { ...nextPayload, event: one };
        delete single.reset;
        delete single.events;
        this.emitDecrypted("session.stream", sessionKey, single, msg);
      }
      return;
    }

    // **状态机先更新，再派发给监听者**（2026-10-06 修正）。顺序反过来写过，后果是
    // 监听者在 `session.done` 里读 `isSessionStreaming()` 拿到的是「删除之前」的值 ——
    // 恒为 `true`，而它正是「这一轮结束该不该重锚定」的判据（useRealtimeEvents 的 done
    // 处理器）。于是正常回合的重锚定恒被跳过，而同一处理器更早一步已经把瞬时尾巴清了
    // ⇒ 正在完成的正文瞬间消失，只剩已落盘的用户行；切走再回来才由 viewer 的 init 补回。
    //
    // 契约：状态反映**正在派发的这一帧**，不是上一帧。谁在监听者里问「现在还在流吗」，
    // 拿到的必须是这一帧之后的事实。
    if (sessionKey) {
      this.updateActiveStreamState(type, sessionKey, nextPayload);
    }
    this.emit({ type, sessionKey, payload: nextPayload });
    if (!sessionKey) return;

    const handlers = this.handlers.get(sessionKey);
    if ((!handlers || handlers.size === 0) && type === "session.stream") {
      const event = nextPayload.event as StreamEvent;
      if (event) {
        const queued = this.pendingStreams.get(sessionKey) || [];
        queued.push(event);
        // 有上限（冲突⑬）：只留最近 N 条 —— 更早的由挂载时的整轮快照重建补回。
        if (queued.length > PENDING_STREAM_LIMIT) {
          queued.splice(0, queued.length - PENDING_STREAM_LIMIT);
        }
        this.pendingStreams.set(sessionKey, queued);
      }
      return;
    }
    if (!handlers || handlers.size === 0) return;

    switch (type) {
      case "session.stream":
        for (const handler of handlers) {
          handler.onStream?.(nextPayload.event as StreamEvent);
        }
        break;
      case "session.done":
        for (const handler of handlers) {
          handler.onDone?.();
        }
        break;
      case "session.error":
        for (const handler of handlers) {
          handler.onError?.(msg.error?.message || "Unknown error");
        }
        break;
    }
  }

  subscribeEvents(listener: (event: SessionServiceEvent) => void) {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  emit(event: SessionServiceEvent) {
    for (const listener of this.listeners) {
      listener(event);
    }
  }

  private updateActiveStreamState(
    type: string,
    sessionKey: string,
    payload: Record<string, unknown>,
  ) {
    if (type === "session.done" || type === "session.error") {
      this.activeStreams.delete(sessionKey);
      return;
    }
    if (type !== "session.stream") return;
    const event = payload.event as StreamEvent | undefined;
    if (!event) return;
    if (event.type === "error") {
      this.activeStreams.delete(sessionKey);
      return;
    }
    if (event.type !== "message_done") {
      this.activeStreams.add(sessionKey);
    }
  }

  isSessionStreaming(sessionKey: string) {
    return this.activeStreams.has(sessionKey);
  }

  subscribe(sessionKey: string, handler: SessionEventHandler) {
    let set = this.handlers.get(sessionKey);
    if (!set) {
      set = new Set<SessionEventHandler>();
      this.handlers.set(sessionKey, set);
    }
    set.add(handler);

    const queued = this.pendingStreams.get(sessionKey);
    if (queued && queued.length > 0) {
      for (const event of queued) {
        handler.onStream?.(event);
      }
      this.pendingStreams.delete(sessionKey);
    }

    return () => {
      const current = this.handlers.get(sessionKey);
      if (!current) return;
      current.delete(handler);
      if (current.size === 0) {
        this.handlers.delete(sessionKey);
      }
    };
  }
}
