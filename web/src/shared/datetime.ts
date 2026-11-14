/**
 * 文件列表紧凑时间：同年显示 MM-DD HH:mm，跨年显示 YYYY-MM-DD。
 */
export function formatCompactTime(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const now = new Date();
  const sameYear = date.getFullYear() === now.getFullYear();
  const month = `${date.getMonth() + 1}`.padStart(2, "0");
  const day = `${date.getDate()}`.padStart(2, "0");
  const hours = `${date.getHours()}`.padStart(2, "0");
  const minutes = `${date.getMinutes()}`.padStart(2, "0");
  if (sameYear) {
    return `${month}-${day} ${hours}:${minutes}`;
  }
  return `${date.getFullYear()}-${month}-${day}`;
}

/**
 * 紧凑文件大小：B / KB / MB / GB / TB，自动选择单位。
 */
export function formatCompactSize(size?: number): string {  if (!size || size < 0) return "";
  if (size < 1024) return `${size} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = size / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  const fractionDigits = value >= 100 ? 0 : value >= 10 ? 1 : 2;
  return `${value.toFixed(fractionDigits).replace(/\.0+$|(\.\d*[1-9])0+$/, "$1")} ${units[unitIndex]}`;
}

/**
 * 会话/留言气泡的相对时间：5 分钟内「刚刚」，1 小时内「N 分钟前」，当天 HH:MM，
 * 昨天「昨天 HH:MM」，今年 MM-DD HH:MM，更早 YYYY-MM-DD。
 *
 * 文件列表的 formatCompactTime 是另一套语义（同年 MM-DD HH:mm/跨年 YYYY-MM-DD），
 * 两者别合并：文件只看日期，留言看「多久以前」。
 */
export function formatRelativeTime(timestamp: number, locale: string = "zh-CN"): string {
  const now = Date.now();
  const diff = now - timestamp;
  const seconds = Math.floor(diff / 1000);
  const minutes = Math.floor(seconds / 60);

  const isZh = locale.startsWith("zh");
  if (minutes < 5) return isZh ? "刚刚" : "Just now";
  if (minutes < 60) return isZh ? `${minutes}分钟前` : `${minutes}m ago`;

  const date = new Date(timestamp);
  const today = new Date();
  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  const clock = (value: Date) =>
    `${String(value.getHours()).padStart(2, "0")}:${String(value.getMinutes()).padStart(2, "0")}`;

  if (sameDay(date, today)) return clock(date);

  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (sameDay(date, yesterday)) return isZh ? `昨天 ${clock(date)}` : `Yesterday ${clock(date)}`;

  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  if (date.getFullYear() === today.getFullYear()) return `${month}-${day} ${clock(date)}`;
  return `${date.getFullYear()}-${month}-${day}`;
}

/** 完整时间字符串（hover title 用）。 */
export function formatFullTime(timestamp: number, locale: string = "zh-CN"): string {
  return new Date(timestamp).toLocaleString(locale);
}
