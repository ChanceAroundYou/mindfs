// NodeRun payload → 给人看的段落。
//
// 运行视图不是调试器，也**不是第二个对话流**（任务 #87 第 3 条：节点输出里的
// summary/result/message 这些自然语言段落，和上面 Router 回复的原文没差别，
// 详情去会话里看）。这里只留「回复里看不到、但确实有用」的东西：
//   - changes 字符串数组作为变更清单；
//   - verification 等扁平对象作为“检查项：结果”；
//   - 其余字符串字段按 key 列出。
// 原始 JSON、输入与调试字段都不进这里：运行视图不是调试器。
//
// 唯一例外是 HumanGate 行的 prompt —— 那是审批问题本身，不是回复正文。

export type ReadablePayloadField = {
  key: string;
  value: string;
};

export type ReadablePayload = {
  /** 只在 HumanGate 行有值：审批问题（prompt）。 */
  text: string;
  /** 变更清单；目前只认 changes。 */
  items: string[];
  /** 其它可读的键值（含 verification 展平后的项）。 */
  fields: ReadablePayloadField[];
};

export type ReadablePayloadOptions = {
  /** true 时把 prompt 当作正文取出（只给 HumanGate 行用）。 */
  gatePrompt?: boolean;
};

// 自然语言段落：与 Router 回复同源，运行视图不再重复展示。
const PROSE_KEYS = ["summary", "result", "message", "text", "title", "detail"];
const GATE_TEXT_KEYS = ["prompt"];
const METADATA_KEYS = new Set([
  "outcome",
  "reason",
  "worktree",
  "worktree_error",
  "resumed_after_restart",
  "comment_id",
]);

const EMPTY: ReadablePayload = { text: "", items: [], fields: [] };

function nonEmptyString(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function valueText(value: unknown): string {
  if (typeof value === "string") return value.trim();
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return "";
}

function stringItems(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value
    .map((item) => nonEmptyString(item))
    .filter((item) => item !== "");
}

function flattenFields(
  value: Record<string, unknown>,
  skip: Set<string>,
): ReadablePayloadField[] {
  const fields: ReadablePayloadField[] = [];
  for (const key of Object.keys(value).sort()) {
    if (skip.has(key) || METADATA_KEYS.has(key)) continue;
    const raw = value[key];
    if (key === "verification" && raw && typeof raw === "object" && !Array.isArray(raw)) {
      const nested = raw as Record<string, unknown>;
      for (const nestedKey of Object.keys(nested).sort()) {
        const text = valueText(nested[nestedKey]);
        if (text !== "") fields.push({ key: nestedKey, value: text });
      }
      continue;
    }
    const text = valueText(raw);
    if (text !== "") fields.push({ key, value: text });
  }
  return fields;
}

export function readablePayload(
  value: Record<string, unknown> | string | null | undefined,
  options?: ReadablePayloadOptions,
): ReadablePayload {
  if (value === null || value === undefined) return EMPTY;
  // 字符串 payload 只会是节点输出本身（= 回复原文），运行视图不再展示它。
  if (typeof value !== "object" || Array.isArray(value)) return EMPTY;

  const payload = value as Record<string, unknown>;
  const textKeys = options?.gatePrompt ? GATE_TEXT_KEYS : [];
  const text = textKeys.map((key) => nonEmptyString(payload[key])).find((item) => item !== "") ?? "";
  const items = stringItems(payload.changes);
  const skip = new Set<string>([...PROSE_KEYS, ...GATE_TEXT_KEYS, "changes"]);
  const fields = flattenFields(payload, skip);
  return { text, items, fields };
}
