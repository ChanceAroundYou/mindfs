// NodeRun payload → 给人看的段落。
//
// 运行视图不是调试器：input/output 是节点之间的数据契约，直接 JSON.stringify
// 会把 outcome/verification 这些簿记字段原样丢给用户（任务 #66 实测）。这里只
// 挑出人真正关心的内容：
//   - summary/result/message/... 作为正文；
//   - changes 字符串数组作为变更清单；
//   - verification 等扁平对象作为“检查项：结果”；
//   - 其余字符串字段按 key 列出。
// 原始 JSON 不进这里 —— 由调用方放进 details 折叠区，需要时仍可查。

export type ReadablePayloadField = {
  key: string;
  value: string;
};

export type ReadablePayload = {
  /** 一段话：总结/结果/提示词。 */
  text: string;
  /** 变更清单；目前只认 changes。 */
  items: string[];
  /** 其它可读的键值（含 verification 展平后的项）。 */
  fields: ReadablePayloadField[];
  /** 原始 JSON；字符串 payload 没有可折叠内容。 */
  raw: string | null;
};

const TEXT_KEYS = ["summary", "result", "message", "text", "title", "detail", "prompt"];
const METADATA_KEYS = new Set([
  "outcome",
  "reason",
  "worktree",
  "worktree_error",
  "resumed_after_restart",
  "comment_id",
]);

const EMPTY: ReadablePayload = { text: "", items: [], fields: [], raw: null };

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
): ReadablePayload {
  if (value === null || value === undefined) return EMPTY;
  if (typeof value === "string") {
    const text = value.trim();
    return text === "" ? EMPTY : { text, items: [], fields: [], raw: null };
  }
  if (typeof value !== "object" || Array.isArray(value)) return EMPTY;

  const payload = value as Record<string, unknown>;
  const text = TEXT_KEYS.map((key) => nonEmptyString(payload[key])).find((item) => item !== "") ?? "";
  const items = stringItems(payload.changes);
  const skip = new Set<string>([...TEXT_KEYS, "changes"]);
  const fields = flattenFields(payload, skip);
  const hasReadable = text !== "" || items.length > 0 || fields.length > 0;
  const raw = hasReadable || Object.keys(payload).length > 0
    ? JSON.stringify(payload, null, 2)
    : null;
  return { text, items, fields, raw };
}
