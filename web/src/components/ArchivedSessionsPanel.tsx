import React from "react";
import { MultiProjectSessionList } from "./SessionList";
import type { ProjectSessionGroup, SessionItem } from "./SessionList";
import { useBackLayer } from "../app/useBackNavigation";
import { useI18n } from "../i18n";

/**
 * 「已归档对话」视图：在**右栏自己的列里**升起，不做全屏浮层。
 *
 * 为什么不复用 BottomSheet / 不用 position:fixed 的遮罩：那一套是全视口的，
 * 会把主面板和左栏一起盖住。这里要的是「右栏内部浮起一层」—— 所以遮罩和面板
 * 都用 absolute，落在右栏那根 flex 列（AppShell 的 rightStyle 已带
 * position: relative）里。主面板和左栏一点不碰，遮罩底下的会话列表透出来。
 *
 * 收起按钮放在标题栏右侧、**朝下**：面板是从栏底上浮上来的，向下的 chevron
 * 本身就是「收回栏底」。不朝右 —— 右栏已经被「关掉右栏」那条 rail 管着了，
 * 这里要的是退回上一级（会话列表），不是关栏；朝右还会和那条 rail 抢同一个语义。
 *
 * ⚠️ 7253855 的提交信息写「收起按钮改为指向右（原先朝下）」，而那次 diff 实际是把
 * 朝右改成了朝下 —— 消息与代码是反的，注释随后也跟着写成了「朝右」，三处对不上。
 * 以代码和本注释为准（archived-panel.test.mjs 里有一条按形状钉住方向的断言），
 * 别按那句提交信息把它翻回去。
 *
 * **进出都靠 transition，不靠条件卸载。** 早先那版是 `{open && <Panel/>}`：
 * 关闭时整层瞬间消失（没有退场），打开时 scrim 无过渡直接满不透明、sheet 却在
 * 淡入 —— 两者叠加就是闪烁。现在常驻 DOM，`isOpen` 只切两处终态样式，
 * 遮罩淡入淡出、面板上浮下沉，同一条曲线同一时长。
 */

// 遮罩留出的顶部空白：面板比整栏短一截，透出底下的列表。
const TOP_GAP = 56;
// 进出用同一条**线性**曲线。曲线形态在这件事上是过度设计：真正让进场
// 「几乎瞬间」的不是曲线，是进场那一帧没有过渡起点（见下面 mounted 的注释）。
// 起点补上之后两侧就都是同一串 CSS 的镜像，线性足够 —— 而且线性天生自反。
const TRANSITION_MS = 240;
const TRANSITION_EASE = "linear";

type ArchivedSessionsPanelProps = {
  isOpen: boolean;
  onClose: () => void;
  groups: ProjectSessionGroup[];
  loading?: boolean;
  selectedKey?: string;
  onSelect?: (session: SessionItem) => void;
  onArchive?: (session: SessionItem, archived: boolean) => Promise<boolean> | boolean;
  emptyText?: React.ReactNode;
};

export function ArchivedSessionsPanel({
  isOpen,
  onClose,
  groups,
  loading = false,
  selectedKey = "",
  onSelect,
  onArchive,
  emptyText,
}: ArchivedSessionsPanelProps) {
  const { t } = useI18n();
  // 返回键 / 边缘侧滑退回会话列表：视图虽然不遮挡什么，但它确实占了右栏的
  // 一级位置，留在返回栈里才和右栏那条 rail 的行为一致。只在展开时挂。
  useBackLayer(isOpen, onClose);

  // 面板常驻 DOM，isOpen 直接当终态样式用。两侧于是都是「已绘制元素改样式」，
  // 天然对称，没有先后帧、没有卸载竞态可言。
  //
  // 早先这里拆成 entered + mounted 两态，靠 rAF 隔一帧再 setEntered。那是错的：
  // 进场起点要靠那一帧的「闭合样式已被绘制」撑着，浏览器不一定给你这一帧 ——
  // 给不到就是同一次绘制里既挂载又切终态，起点不存在，transition 根本不启动，
  // 面板直接「在」。表现就是进场瞬间、退场慢慢。

  // 没有归档的会话就什么都别画，别把空视图推给用户
  const total = groups.reduce((sum, g) => sum + (g.sessions?.length || 0), 0);
  // 进出共用：mask 和 sheet 的每条 transition 都是这一串
  const timing = `${TRANSITION_MS}ms ${TRANSITION_EASE}`;

  return (
    // 定位上下文由调用方（占满右栏整列的 relative wrapper）提供。
    // 这一层必须 absolute 盖满整列：写成 flex item 会跟会话列表并排，
    // 变成「上下分栏」而不是「浮在上面」。
    // 收起后仍占着整列但不透明、也不吃点击，靠 pointerEvents 让位给底下的列表。
    <div
      data-archived-panel="layer"
      style={{
        position: "absolute",
        inset: 0,
        zIndex: 2,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        pointerEvents: isOpen ? "auto" : "none",
      }}
    >
      {/* 遮罩：半透明黑，盖住右栏自身内容，主面板/左栏不受影响 */}
      <div
        data-archived-panel="scrim"
        onClick={onClose}
        style={{
          position: "absolute",
          inset: 0,
          zIndex: 1,
          background: "rgba(0, 0, 0, 0.42)",
          opacity: isOpen ? 1 : 0,
          transition: `opacity ${timing}`,
        }}
      />
      {/* 面板本体：从底部上浮（translateY），盖在遮罩之上 */}
      <div
        data-archived-panel="sheet"
        style={{
          position: "absolute",
          left: 0,
          right: 0,
          bottom: 0,
          top: TOP_GAP,
          zIndex: 2,
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
          borderTopLeftRadius: "12px",
          borderTopRightRadius: "12px",
          boxShadow: "0 -8px 24px rgba(0, 0, 0, 0.22)",
          background: "var(--mindfs-topbar-bg, var(--sidebar-bg))",
          // 只做竖直方向的位移：开着不动，收起沉到栏底之下。
          // 不带 scale —— 缩放是第二个方向的运动，和位移叠在一起读起来
          // 就是「在缩放」，抢了「在升起」的主戏。位移 + 淡入淡出已经够了。
          transform: isOpen ? "translateY(0)" : "translateY(48px)",
          opacity: isOpen ? 1 : 0,
          transition: `transform ${timing}, opacity ${timing}`,
        }}
      >
        <ArchivedHeader
          title={t("sessionList.archivedPanel.title")}
          count={total}
          onClose={onClose}
        />
        <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
          <MultiProjectSessionList
            groups={groups}
            selectedKey={selectedKey}
            loading={loading}
            emptyText={emptyText ?? t("sessionList.archivedPanel.empty")}
            hideHeader
            onSelect={onSelect}
            onArchive={onArchive}
          />
        </div>
      </div>
    </div>
  );
}

function ArchivedHeader({
  title,
  count,
  onClose,
}: {
  title: string;
  count: number;
  onClose: () => void;
}) {
  const { t } = useI18n();
  return (
    <div
      data-archived-panel="header"
      style={{
        display: "flex",
        alignItems: "center",
        gap: "6px",
        padding: "0 8px 0 10px",
        height: "44px",
        flexShrink: 0,
        borderBottom: "1px solid var(--border-color)",
        boxSizing: "border-box",
      }}
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        aria-hidden="true"
        style={{ color: "var(--text-secondary)", flexShrink: 0 }}
      >
        <path
          fill="currentColor"
          d="M3 4.5A1.5 1.5 0 0 1 4.5 3h15A1.5 1.5 0 0 1 21 4.5V6H3zm-.5 3A1.5 1.5 0 0 0 1 8.5V19a2 2 0 0 0 2 2h18a2 2 0 0 0 2-2V8.5a2 2 0 0 0-1.5-1.5zm5 3.5a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h10a1 1 0 0 0 1-1v-7a1 1 0 0 0-1-1zm2.5 3h5v2h-5z"
        />
      </svg>
      <span
        style={{
          flex: 1,
          minWidth: 0,
          fontSize: "12px",
          fontWeight: 600,
          color: "var(--text-primary)",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {title}
      </span>
      <span style={{ fontSize: "11px", color: "var(--text-secondary)" }}>{count}</span>
      <button
        type="button"
        data-archived-panel="collapse"
        aria-label={t("sessionList.archivedPanel.collapse")}
        title={t("sessionList.archivedPanel.collapse")}
        onClick={onClose}
        style={{
          width: "26px",
          height: "26px",
          minWidth: "26px",
          border: "none",
          borderRadius: "7px",
          padding: 0,
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          cursor: "pointer",
          background: "transparent",
          color: "var(--text-secondary)",
        }}
        onMouseEnter={(e) => {
          e.currentTarget.style.background = "rgba(0,0,0,0.06)";
        }}
        onMouseLeave={(e) => {
          e.currentTarget.style.background = "transparent";
        }}
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <polyline points="3.5 8 8 12.5 12.5 8" />
        </svg>
      </button>
    </div>
  );
}
