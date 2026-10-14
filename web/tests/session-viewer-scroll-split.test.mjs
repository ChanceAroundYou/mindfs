import assert from "node:assert/strict";
import { closeSync, openSync, readFileSync } from "./source-map-fs.mjs";
import { resolve } from "node:path";
import { test } from "vitest";

test("session-viewer-scroll-split", async () => {
const root = new URL("../", import.meta.url);
const readPhysical = (path) => {
  const fd = openSync(resolve(root.pathname, path), "r");
  try { return readFileSync(fd, "utf8"); } finally { closeSync(fd); }
};
const viewer = readPhysical("src/components/session/SessionViewer.tsx");
const scroll = readPhysical("src/components/session/useSessionViewerScroll.ts");
const meta = readPhysical("src/components/session/buildTimelineItemMeta.ts");
const styles = readPhysical("src/components/session/sessionViewerStyles.ts");

assert.ok(viewer.split("\n").length - 1 < 500, "the physical viewer stays below the hard line");
assert.match(viewer, /useSessionViewerScrollState\(activeScrollRef\)/);
assert.match(viewer, /useSessionViewerScrollPositionEffects\(/);
assert.match(viewer, /useSessionViewerStickinessEffect\(/);
assert.match(viewer, /useTargetSeqScroll\([\s\S]*?targetSeqScrollKeyRef[\s\S]*?cancelTargetSeqScroll/);
assert.match(scroll, /distanceFromBottom < 40/);
assert.match(scroll, /movedUp/);
assert.match(scroll, /refreshCurrentUserMessageIndex\(\)/);
assert.match(meta, /for \(let i = 0; i < timeline\.length; i \+= 1\)/);
assert.match(viewer, /<style>\{sessionViewerAnimationStyles\}<\/style>/);
assert.match(styles, /@keyframes sessionLoadingPulse/);

console.log("session-viewer-scroll-split.test.mjs: OK");
});
