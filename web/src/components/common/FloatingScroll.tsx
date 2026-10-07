import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

/**
 * 浮动滚动条容器：隐藏原生滚动条，改用浮在内容上的细拇指。
 *
 * 为什么不用原生滚动条：全局 `::-webkit-scrollbar { width: 6px }` 会占宽度，
 * 把每列卡片挤窄 6px（用户 2026-10-07 要求「滚动条不要占宽度，浮动在任务卡上面」）。
 * `overflow: overlay` 已废弃且 Firefox 不支持，跨端行为不一致，所以自绘。
 *
 * 结构三层：包裹层（position:relative，不滚动）→ 滚动容器（原生滚动条隐藏）→ 浮动拇指。
 * 拇指是包裹层的绝对定位子节点，`pointerEvents: none`，不挡点击。
 *
 * 可见性走 overlay 语义：悬停或滚动时可见，停止滚动 1.2s 后淡出。
 * 触屏设备没有悬停，滚动时出现、停手后淡出。
 */
export function FloatingScroll({ children, style }: {
  children: React.ReactNode;
  /** 滚动容器样式（padding / gap / 布局方向）；overflow 与滚动条由本组件接管 */
  style?: React.CSSProperties;
}) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const idleTimer = useRef<number | null>(null);
  const [thumb, setThumb] = useState<{ top: number; height: number } | null>(null);
  const [hovered, setHovered] = useState(false);
  const [scrolling, setScrolling] = useState(false);

  // 拇指几何：百分比相对包裹层（高度恒等于滚动容器的 clientHeight）。
  // 内容不溢出时 thumb = null，不渲染。用函数式 setState + 数值比较避免无谓重渲染。
  const update = useCallback(() => {
    const el = scrollRef.current;
    if (!el) return;
    const { clientHeight, scrollHeight, scrollTop } = el;
    if (scrollHeight <= clientHeight) {
      setThumb(null);
      return;
    }
    const top = (scrollTop / scrollHeight) * 100;
    const height = (clientHeight / scrollHeight) * 100;
    setThumb((prev) => (
      prev && Math.abs(prev.top - top) < 0.01 && Math.abs(prev.height - height) < 0.01
        ? prev
        : { top, height }
    ));
  }, []);

  const scheduleIdle = useCallback(() => {
    setScrolling(true);
    if (idleTimer.current !== null) window.clearTimeout(idleTimer.current);
    idleTimer.current = window.setTimeout(() => setScrolling(false), 1200);
  }, []);

  const handleScroll = useCallback(() => {
    update();
    scheduleIdle();
  }, [update, scheduleIdle]);

  // 挂载时算一次；children 引用变化（卡片异步加载、展开/折叠）时重算 ——
  // 滚动容器自身高度由 flex 给死，ResizeObserver 抓不到内容变高。
  useLayoutEffect(() => { update(); }, [update, children]);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const observer = new ResizeObserver(update);
    observer.observe(el);
    return () => observer.disconnect();
  }, [update]);

  useEffect(() => () => {
    if (idleTimer.current !== null) window.clearTimeout(idleTimer.current);
  }, []);

  const showThumb = thumb !== null && (hovered || scrolling);

  return (
    <div
      style={{ position: "relative", flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
    >
      <div
        ref={scrollRef}
        onScroll={handleScroll}
        className="mindfs-floating-scroll"
        style={{ flex: 1, minHeight: 0, overflowY: "auto", scrollbarWidth: "none", ...style }}
      >
        {children}
      </div>
      {thumb !== null ? (
        <div
          aria-hidden="true"
          style={{
            position: "absolute",
            right: 2,
            top: `${thumb.top}%`,
            height: `${thumb.height}%`,
            width: 4,
            borderRadius: 2,
            background: "rgba(100, 116, 139, 0.4)",
            pointerEvents: "none",
            opacity: showThumb ? 0.9 : 0,
            transition: "opacity 0.15s ease-out",
          }}
        />
      ) : null}
    </div>
  );
}
