// 工作流编辑器的「尺寸/视图」判定 —— 抽成纯函数，因为三条都是会回归的规则：
//
// 1. 编辑器只属于看板这一次停留：看板面板本身在 files/chat 态不渲染，而 open 标记
//    住在 app 层 hook 里会活过整段离开，用户切回来会直接看到编辑器而不是看板。
// 2. 手机上没有「节点旁边」：390px 的屏放不下 400px 的贴节点卡片，属性面板改成
//    贴底抽屉（全宽），与桌面共用一个组件、只换 frame。
// 3. 手机上 AI 面板默认收起（否则画布被挤到首屏以下），但提案/报错一出现必须展开，
//    否则「生成完了什么都没发生」。

import type { CSSProperties } from "react";
import type { MainViewMode } from "../../app/appPath";

/** 编辑器的入口只在单项目看板工具栏上，所以它只属于 board 这一次停留。
 *  工作台虽然也是看板样式的面板，但它是另一个面（没有编辑器入口），
 *  切过去同样要收起来 —— 否则从工作台点开项目，看到的还是编辑器。 */
export function boardKeepsWorkflowEditor(mainView: MainViewMode): boolean {
  return mainView === "board";
}

export type InspectorLayout = "anchored" | "sheet";

/** 抽屉贴视口的边距，与 WorkflowNodeInspector 的实际渲染保持一致。 */
export const SHEET_MARGIN = 8;
export const SHEET_MAX_HEIGHT = "70vh";

/** 属性面板的定位框：桌面贴节点，手机贴底全宽（忽略 position）。 */
export function inspectorFrame(layout: InspectorLayout, position: { x: number; y: number }): CSSProperties {
  if (layout === "sheet") {
    return {
      left: SHEET_MARGIN,
      right: SHEET_MARGIN,
      bottom: SHEET_MARGIN,
      top: "auto",
      maxWidth: "none",
      maxHeight: SHEET_MAX_HEIGHT,
    };
  }
  return { left: position.x, top: position.y, maxWidth: "400px", maxHeight: "80vh" };
}

export type AuthoringPanelGate = {
  isMobile: boolean;
  /** 用户手动展开了没有（只有手机上看这个值）。 */
  open: boolean;
  createMode: boolean;
  hasProposal: boolean;
  hasError: boolean;
};

/** 桌面恒展开；手机默认收起，但这三种情况一定要能看见内容。 */
export function authoringPanelExpanded(gate: AuthoringPanelGate): boolean {
  if (!gate.isMobile) return true;
  return gate.open || gate.createMode || gate.hasProposal || gate.hasError;
}

export type DraftPanelState = {
  isMobile: boolean;
  /** 只有手机上看这个值：抽屉展开了没有。 */
  open: boolean;
};

/**
 * 草稿列表的框：桌面是固定宽度的竖栏，手机是浮在画布上方的左侧抽屉。
 *
 * 用户 2026-11-20 的原话是「把工作流列表做成浮动的左侧栏，可以展开收起、
 * 竖向排列」，并选定「收起后完全隐藏 + 悬浮把手」：所以窄屏不再占用纵向
 * 空间、也不再挤压画布宽度，收起时整块移出可视区（visibility 一并隐藏，
 * 避免不可见元素还能被点到）。
 */
export function draftPanelFrame(state: DraftPanelState): CSSProperties {
  if (!state.isMobile) {
    return { display: "flex", flexDirection: "column", width: 190, flexShrink: 0, overflowY: "auto" };
  }
  return {
    position: "absolute",
    left: 0,
    top: 0,
    bottom: 0,
    width: "min(240px, 72vw)",
    zIndex: 20,
    display: "flex",
    flexDirection: "column",
    gap: 6,
    overflowY: "auto",
    overflowX: "hidden",
    transform: state.open ? "translateX(0)" : "translateX(-110%)",
    visibility: state.open ? "visible" : "hidden",
    transition: "transform 160ms ease",
    boxShadow: "0 8px 24px rgba(0, 0, 0, 0.25)",
    // 抽屉自己的滚动条同样不该露出来（用户同时要求「隐藏右侧滚动条」）。
    scrollbarWidth: "none",
  };
}
