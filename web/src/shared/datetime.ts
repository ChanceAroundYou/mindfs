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
export function formatCompactSize(size?: number): string {
  if (!size || size < 0) return "";
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
