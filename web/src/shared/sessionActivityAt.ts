function timeValue(value: string | null | undefined): number {
  return Date.parse(String(value || "")) || 0;
}

export function sessionActivityAt(
  item: { updated_at?: string; activity_at?: string },
): string {
  return timeValue(item.activity_at) > timeValue(item.updated_at)
    ? String(item.activity_at || "")
    : String(item.updated_at || "");
}
