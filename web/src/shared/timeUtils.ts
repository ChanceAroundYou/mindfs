/**
 * 将时间戳转换为友好的相对时间显示
 * @param timestamp Unix 时间戳（毫秒）
 * @param locale 语言环境（'zh-CN' | 'en-US'）
 * @returns 相对时间字符串
 */
export function formatRelativeTime(timestamp: number, locale: string = "zh-CN"): string {
  const now = Date.now();
  const diff = now - timestamp;
  const seconds = Math.floor(diff / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  const isZh = locale.startsWith("zh");

  // 5分钟内：刚刚
  if (minutes < 5) {
    return isZh ? "刚刚" : "Just now";
  }

  // 1小时内：X分钟前
  if (minutes < 60) {
    return isZh ? `${minutes}分钟前` : `${minutes}m ago`;
  }

  // 今天：HH:MM
  const date = new Date(timestamp);
  const today = new Date();
  if (
    date.getFullYear() === today.getFullYear() &&
    date.getMonth() === today.getMonth() &&
    date.getDate() === today.getDate()
  ) {
    const hh = String(date.getHours()).padStart(2, "0");
    const mm = String(date.getMinutes()).padStart(2, "0");
    return `${hh}:${mm}`;
  }

  // 昨天：昨天 HH:MM
  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (
    date.getFullYear() === yesterday.getFullYear() &&
    date.getMonth() === yesterday.getMonth() &&
    date.getDate() === yesterday.getDate()
  ) {
    const hh = String(date.getHours()).padStart(2, "0");
    const mm = String(date.getMinutes()).padStart(2, "0");
    return isZh ? `昨天 ${hh}:${mm}` : `Yesterday ${hh}:${mm}`;
  }

  // 今年：MM-DD HH:MM
  if (date.getFullYear() === today.getFullYear()) {
    const month = String(date.getMonth() + 1).padStart(2, "0");
    const day = String(date.getDate()).padStart(2, "0");
    const hh = String(date.getHours()).padStart(2, "0");
    const mm = String(date.getMinutes()).padStart(2, "0");
    return `${month}-${day} ${hh}:${mm}`;
  }

  // 更早：YYYY-MM-DD
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

/**
 * 获取完整时间字符串（用于 title 属性）
 */
export function formatFullTime(timestamp: number, locale: string = "zh-CN"): string {
  return new Date(timestamp).toLocaleString(locale);
}
