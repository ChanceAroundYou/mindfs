/**
 * 胶囊里所有 token 数字共用的格式：小于 10k 保留可读的原值，>= 10k 一律用 k。
 *
 * 这里显示的是趋势，不是账单：6,129,539 与 6130k 传达的信息一样，但后者
 * 不会让人误以为我们掌握了精确到个位的真实 token 数。
 */
export function formatTokenAmount(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "0";
  const rounded = Math.round(value);
  if (rounded < 10_000) {
    return rounded.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  }
  const thousands = rounded / 1000;
  const text = thousands < 100 ? thousands.toFixed(1) : String(Math.round(thousands));
  const trimmed = text.includes(".") ? text.replace(/0+$/, "").replace(/\.$/, "") : text;
  return `${trimmed}k`;
}
