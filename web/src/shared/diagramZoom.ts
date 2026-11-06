/**
 * 图表预览的缩放别名 —— 数学本体在 `shared/zoomTransform.ts`（图片预览共用）。
 *
 * 保留 `zoomDiagramAt` / `diagramGesture` / `DiagramTransform` / `DiagramPoint`
 * 这几个旧名字：`DiagramPreview.tsx`、`tests/diagram-zoom.test.mjs` 与
 * `source-map-modules-components.mjs` 都按它们写。要改名另开一轮，别在这里抽掉。
 */
export type { ZoomPoint as DiagramPoint, ZoomTransform as DiagramTransform } from "./zoomTransform";
export { gestureMetrics as diagramGesture, zoomAt as zoomDiagramAt } from "./zoomTransform";
