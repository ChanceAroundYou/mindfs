export function scrollToRelatedDivider({ behavior = "auto", activeScrollRef, interactionMode, relatedFilesDividerRef, relatedFiles, relatedFilesCollapsed }: any) {
  const container = activeScrollRef?.current;
  if (!container) return;
  const maxTop = Math.max(0, container.scrollHeight - container.clientHeight);
  let top = maxTop;
  const divider = interactionMode === "drawer" ? relatedFilesDividerRef.current : null;
  if (divider && relatedFiles.length > 0 && !relatedFilesCollapsed) {
    const dividerTop = divider.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop;
    top = Math.max(0, Math.min(maxTop, dividerTop - container.clientHeight * 0.08));
  }
  container.scrollTo({ top, behavior });
}
