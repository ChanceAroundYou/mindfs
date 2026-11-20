import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { RefObject } from "react";
import type { CandidateItem } from "../../shared/candidateTypes";

/**
 * @ / # / 候选下拉（任务侧：新建任务面板 + 任务详情输入框）。
 *
 * **挂到 document.body 上，按输入框外层容器的矩形定位**，不用「父级 relative + absolute」：
 * 任务侧这两处都长在 PanelShell 里，而 PanelShell 的 `.mindfs-panel-body` 是 `overflow:auto`
 * 的滚动容器、`.mindfs-panel-dialog` 还叠了一层 `overflow:hidden`。父级绝对定位向上展开
 * （`bottom: calc(100% + 6px)`，最高 260px）时，菜单会被这两层裁掉 —— 面板正文顶部到
 * 输入框顶部只有约 40px，实测 4 条候选里只有最后一条能看见、前三条的命中测试落在
 * `.mindfs-panel-overlay` / 面板 header 上（2026-11-20 用户报「移动端几乎看不见」）。
 * portal 出去以后没有任何祖先裁剪，也不再受面板 `z-index: 90` 的层叠上下文影响。
 *
 * 定位规则：贴着输入框上方 6px；上方放不下（不足一条候选的高度且不如下方宽裕）就翻到下方。
 * 高度按可用空间夹到 [96, 260]。窗口/滚动容器/visualViewport 变化时重算。
 */
export type TaskInlineCandidateMenuProps = {
  visible: boolean;
  /** 定位锚点：包住编辑器的那层容器（菜单在它上方/下方展开，宽度跟它一致）。 */
  anchorRef: RefObject<HTMLElement | null>;
  candidates: CandidateItem[];
  selectedIndex: number;
  itemRefs: RefObject<(HTMLButtonElement | null)[]>;
  onSelect: (candidate: CandidateItem) => void;
  onSelectedIndexChange: (index: number) => void;
};

const MENU_GAP = 6;
const MENU_MARGIN = 8;
const MENU_MAX_HEIGHT = 260;
const MENU_MIN_HEIGHT = 96;
/**
 * 够放下「像样的」一屏候选（约 6 条）才算这一侧可用。
 * 只有 113px 时仍然向上会把 4 条候选压进一条半的滚动窗口，而下方有 480px 空着 ——
 * 所以宁可翻到下方，也不为了「贴着输入框」牺牲可读性。
 */
const MENU_PREFERRED_SPACE = 200;

type MenuPlacement = {
  left: number;
  width: number;
  maxHeight: number;
  above: boolean;
  /** above 时用 bottom（贴着输入框上沿），below 时用 top。 */
  top: number;
  bottom: number;
};

function measurePlacement(anchor: HTMLElement): MenuPlacement {
  const rect = anchor.getBoundingClientRect();
  // 软键盘弹起只改 visualViewport，不改布局视口；按视觉视口算可用空间才不会算到键盘底下。
  const view = window.visualViewport;
  const viewTop = view ? view.offsetTop : 0;
  const viewLeft = view ? view.offsetLeft : 0;
  const viewWidth = view ? view.width : window.innerWidth;
  const viewHeight = view ? view.height : window.innerHeight;
  const viewBottom = viewTop + viewHeight;
  const viewRight = viewLeft + viewWidth;

  const spaceAbove = rect.top - viewTop - MENU_GAP - MENU_MARGIN;
  const spaceBelow = viewBottom - rect.bottom - MENU_GAP - MENU_MARGIN;
  // 优先向上（贴着输入框）；上面不够一屏就翻到下方；两侧都不够时取更宽裕的一侧。
  const above =
    spaceAbove >= MENU_PREFERRED_SPACE
      ? true
      : spaceBelow >= MENU_PREFERRED_SPACE
        ? false
        : spaceAbove >= spaceBelow;
  const space = above ? spaceAbove : spaceBelow;

  const width = Math.max(160, Math.min(rect.width, viewWidth - MENU_MARGIN * 2));
  const left = Math.min(
    Math.max(rect.left, viewLeft + MENU_MARGIN),
    viewRight - MENU_MARGIN - width
  );
  return {
    left,
    width,
    maxHeight: Math.max(MENU_MIN_HEIGHT, Math.min(MENU_MAX_HEIGHT, space)),
    above,
    top: rect.bottom + MENU_GAP,
    bottom: window.innerHeight - rect.top + MENU_GAP,
  };
}

export function TaskInlineCandidateMenu({
  visible,
  anchorRef,
  candidates,
  selectedIndex,
  itemRefs,
  onSelect,
  onSelectedIndexChange,
}: TaskInlineCandidateMenuProps) {
  const active = visible && candidates.length > 0;
  const anchor = active ? anchorRef.current : null;
  const [placement, setPlacement] = useState<MenuPlacement | null>(null);
  const menuRef = useRef<HTMLDivElement | null>(null);

  const update = useCallback(() => {
    const node = anchorRef.current;
    setPlacement(node ? measurePlacement(node) : null);
  }, [anchorRef]);

  // 首次显示时先量再画（layout effect 里的 setState 会在绘制前落定，不会闪一下原位）。
  useLayoutEffect(() => {
    if (!active) {
      setPlacement(null);
      return;
    }
    update();
  }, [active, update, candidates.length, selectedIndex, anchor]);

  useEffect(() => {
    if (!active) return;
    const onViewportChange = () => update();
    window.addEventListener("resize", onViewportChange);
    // 捕获阶段收滚动：面板正文自己滚、页面滚、祖先任何一层滚都能听到。
    window.addEventListener("scroll", onViewportChange, true);
    const view = window.visualViewport;
    view?.addEventListener("resize", onViewportChange);
    view?.addEventListener("scroll", onViewportChange);
    return () => {
      window.removeEventListener("resize", onViewportChange);
      window.removeEventListener("scroll", onViewportChange, true);
      view?.removeEventListener("resize", onViewportChange);
      view?.removeEventListener("scroll", onViewportChange);
    };
  }, [active, update]);

  if (!active || !placement || typeof document === "undefined") return null;

  return createPortal(
    <div
      ref={menuRef}
      data-testid="task-candidate-menu"
      data-placement={placement.above ? "above" : "below"}
      style={{
        position: "fixed",
        left: `${placement.left}px`,
        width: `${placement.width}px`,
        ...(placement.above ? { bottom: `${placement.bottom}px` } : { top: `${placement.top}px` }),
        maxHeight: `${placement.maxHeight}px`,
        overflowY: "auto",
        border: "1px solid var(--menu-border)",
        borderRadius: "8px",
        background: "var(--menu-bg)",
        boxShadow: "0 12px 28px rgba(15, 23, 42, 0.14)",
        // 面板遮罩是 90；候选项必须盖在它上面，但仍低于全局对话框 / toast（1000+）。
        zIndex: 200,
        overscrollBehavior: "contain",
      }}
    >
      {candidates.map((candidate, index) => (
        <button
          key={`${candidate.type}:${candidate.name}`}
          ref={(node) => {
            itemRefs.current[index] = node;
          }}
          type="button"
          onMouseDown={(event) => {
            event.preventDefault();
            onSelect(candidate);
          }}
          style={{
            width: "100%",
            border: "none",
            borderTop: index === 0 ? "none" : "1px solid var(--menu-divider)",
            background: index === selectedIndex ? "var(--menu-active-bg)" : "transparent",
            color: "var(--text-primary)",
            display: "flex",
            flexDirection: "column",
            alignItems: "flex-start",
            gap: "2px",
            padding: "9px 10px",
            textAlign: "left",
            cursor: "pointer",
          }}
          onMouseEnter={() => onSelectedIndexChange(index)}
        >
          <span style={{ fontSize: "13px", fontWeight: 700 }}>
            {candidate.type === "file" ? `@${candidate.name}` : candidate.type === "prompt" ? `#${candidate.name}` : candidate.type === "slash_command" ? `/${candidate.name}` : candidate.name}
          </span>
          {candidate.description ? (
            <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{candidate.description}</span>
          ) : null}
        </button>
      ))}
    </div>,
    document.body
  );
}
