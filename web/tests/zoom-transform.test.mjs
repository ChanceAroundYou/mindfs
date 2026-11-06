import assert from "node:assert/strict";
import { test } from "vitest";
import {
  ZOOM_MAX_SCALE,
  ZOOM_MIN_SCALE,
  gestureMetrics,
  zoomAt,
} from "../src/shared/zoomTransform.ts";

test("zoom-transform", async () => {
  const initial = { x: 0, y: 0, scale: 1 };
  // 锚点不动：以 (100,50) 为锚放大 2 倍，该点在内容坐标里仍投影到 (100,50)。
  assert.deepEqual(zoomAt(initial, 2, { x: 100, y: 50 }), { x: -100, y: -50, scale: 2 });
  // 双指中点从 from 移到 to：factor=1 时等于纯平移。
  assert.deepEqual(zoomAt(initial, 1, { x: 10, y: 20 }, { x: 30, y: 50 }), { x: 20, y: 30, scale: 1 });
  // 上下限夹取。
  assert.equal(zoomAt(initial, 1000, { x: 0, y: 0 }).scale, ZOOM_MAX_SCALE);
  assert.equal(zoomAt(initial, 0.0001, { x: 0, y: 0 }).scale, ZOOM_MIN_SCALE);
  // 往返可逆。
  const zoomed = zoomAt(initial, 3, { x: 12, y: 24 });
  assert.deepEqual(zoomAt(zoomed, 1 / 3, { x: 12, y: 24 }), initial);
  // 手势中点与距离。
  assert.deepEqual(gestureMetrics([{ x: 0, y: 0 }, { x: 6, y: 8 }]), { center: { x: 3, y: 4 }, distance: 10 });
  assert.deepEqual(gestureMetrics([{ x: 6, y: 8 }]), { center: { x: 6, y: 8 }, distance: 0 });
});
