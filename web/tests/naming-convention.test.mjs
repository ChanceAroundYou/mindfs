// 命名规范扫描：文件名 = 主 export 名、.ts 无 JSX、Utils/Helpers 后缀语义。
// 用法：node --test tests/naming-convention.test.mjs
// 说明：第一项用 esbuild 精确判定（泛型 Set<string>/Promise<void> 不算 JSX）；
// 第三项识别多 export 文件（FileOperations 等）与 Memo/default 导出变体。
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, basename, extname } from "node:path";
import { fileURLToPath } from "node:url";
import { transformSync } from "esbuild";

const SRC = join(fileURLToPath(import.meta.url), "..", "..", "src");

function walk(dir, out = []) {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) {
      if (e === "node_modules") continue;
      walk(p, out);
    } else if (e.endsWith(".ts") || e.endsWith(".tsx")) {
      out.push(p);
    }
  }
  return out;
}

// 用 esbuild 以 .ts 语义解析：能通过 = 无 JSX；失败且错误含 JSX 特征 = 真含 JSX。
// 泛型（Set<string>、Promise<void>、=>）在 .ts 下合法通过，不误报。
function tsHasJsx(text) {
  try {
    transformSync(text, { loader: "ts" });
    return false;
  } catch (err) {
    const msg = String(err?.message || err);
    return /expected ">"|unterminated|JSX|jsx/i.test(msg);
  }
}

test("naming: .ts files contain no JSX", () => {
  const violations = [];
  for (const f of walk(SRC)) {
    if (extname(f) !== ".ts") continue;
    if (f.endsWith(".d.ts")) continue;
    const text = readFileSync(f, "utf8");
    if (tsHasJsx(text)) {
      violations.push(`${f}: parses as TSX but not TS (contains JSX)`);
    }
  }
  assert.deepEqual(violations, [], `TSX-in-.ts violations:\n${violations.join("\n")}`);
});

test("naming: *Utils.ts exports no JSX-returning function", () => {
  const violations = [];
  for (const f of walk(SRC)) {
    const base = basename(f);
    if (!/Utils\.ts$/.test(base)) continue;
    const text = readFileSync(f, "utf8");
    if (tsHasJsx(text)) {
      violations.push(`${f}: *Utils.ts must be pure functions (move JSX helpers to *Helpers.tsx)`);
    }
  }
  assert.deepEqual(violations, [], `Utils-with-JSX violations:\n${violations.join("\n")}`);
});

test("naming: main export matches file name (components/hooks)", () => {
  // 存量多 export 文件白名单（见 .claude/rules/frontend/TODO.md 存量命名违规项）：
  // 这些文件导出多个同级组件/函数，无单一主 export，细拆是 file-size 规则的事。
  // 白名单只放行存量，新增文件不在此列的一律按规则卡。
  const GRANDFATHERED = new Set([
    "hooks/useBackNavigation.ts", // 导出 useBackLayer/registerBackLayer 等一组导航原语
    "components/agent/AgentSelectorParts.tsx", // AgentMenuPortal + 菜单常量簇
    "components/file/FileOperations.tsx", // FileMenuIcon/FileOperationItems/MoreMenuButton 等
    "components/common/MarkdownCodeBlocks.tsx", // MermaidBlock/MarkdownCodeBlock(+Memo)
    "components/session/SessionViewerAttachments.tsx", // 导出 AttachmentImage（单体，文件名待改）
    "components/session/SessionViewerAuxiliaryTimeline.tsx", // 导出 renderAuxiliaryTimelineItem
    "components/session/SessionViewerCards.tsx", // PlanUpdateCard/CompactNoticeCard/TodoUpdateCard 簇
    "components/session/SessionViewerTokenViews.tsx", // TurnTokenUsage/ContextWindowBadge
    "components/session/SessionViewerToolItem.tsx", // 导出 renderToolTimelineItem
    "components/task/ScheduledCronEditor.tsx", // CronEditor + StopIcon/RunNowIcon/DeleteIcon
  ]);
  const violations = [];
  const grandfatheredHit = [];
  for (const f of walk(SRC)) {
    const base = basename(f).replace(/\.(tsx?)$/, "");
    const isComponent = /^[A-Z]/.test(base);
    const isHook = /^use[A-Z]/.test(base);
    if (!isComponent && !isHook) continue;
    // 类型/上下文/过滤器文件：主 export 是 type / 常量簇，按命名规则本就合规，跳过
    if (/Types$|Context$|Filters$/.test(base)) continue;
    const rel = f.slice(SRC.length + 1);
    if (GRANDFATHERED.has(rel)) {
      grandfatheredHit.push(rel);
      continue;
    }
    const text = readFileSync(f, "utf8");
    // 1) 直接导出：function/const/class 名 == 文件名
    const directRe = new RegExp(`export\\s+(function|const|class|type|interface)\\s+${base}\\b`);
    // 2) 解构再导出：export { X }（X == 文件名，如 formatCompactSize 经 re-export）
    const reExportRe = new RegExp(`export\\s*\\{[^}]*\\b${base}\\b`);
    // 3) default 导出（TokenEditor）：export default X
    const defaultRe = /export\s+default\b/;
    // 4) Memo 变体（SessionCard → SessionCardMemo）：export const ${base}\w* = memo(
    const memoRe = new RegExp(`export\\s+const\\s+${base}\\w*\\s*=\\s*memo\\s*\\(`);
    // 5) 文件含同名前缀的多个导出（FileOperations/MarkdownCodeBlocks/SessionViewerCards/
    //    ScheduledCronEditor 等多 export 文件）：任一导出以文件名为前缀即算合规，
    //    细拆是 file-size 规则的事，不在本测试卡
    const prefixRe = new RegExp(`export\\s+(function|const)\\s+${base}[A-Z]\\w*\\b`);
    if (
      !directRe.test(text) &&
      !reExportRe.test(text) &&
      !defaultRe.test(text) &&
      !memoRe.test(text) &&
      !prefixRe.test(text)
    ) {
      violations.push(`${f}: expected main export '${base}'`);
    }
  }
  assert.deepEqual(violations, [], `name-mismatch violations:\n${violations.join("\n")}`);
  // 白名单防腐：存量 10 个都在，少了说明有文件被顺手改名了，测试要跟着更新
  assert.equal(
    grandfatheredHit.length,
    GRANDFATHERED.size,
    `grandfathered files changed (expected ${GRANDFATHERED.size}, hit ${grandfatheredHit.length}): ` +
      [...GRANDFATHERED].filter((x) => !grandfatheredHit.includes(x)).join(", "),
  );
});
