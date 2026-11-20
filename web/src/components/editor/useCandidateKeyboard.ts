import type React from "react";
import type { CandidateItem } from "../../shared/candidateTypes";

type UseCandidateKeyboardParams = {
  candidates: CandidateItem[];
  activeCandidateIndex: number;
  setActiveCandidateIndex: React.Dispatch<React.SetStateAction<number>>;
  setCandidates: React.Dispatch<React.SetStateAction<CandidateItem[]>>;
  applyCandidate: (candidate: CandidateItem) => void;
};

/**
 * @ / # / 候选下拉的键盘契约 —— 与对话输入框（useActionBarHandlers）逐条对齐：
 * ↑↓ 循环高亮、Tab 选中、Esc 关闭、Enter 选中当前项。
 *
 * 两个入口（新建任务面板、任务详情输入框）共用这一份，避免一边能按 Enter
 * 选、另一边按 Enter 却把半截 `@foo` 发出去。
 */
export function useCandidateKeyboard({
  candidates,
  activeCandidateIndex,
  setActiveCandidateIndex,
  setCandidates,
  applyCandidate,
}: UseCandidateKeyboardParams) {
  const handleCandidateKeyDown = (event: React.KeyboardEvent<HTMLDivElement>): boolean => {
    if (candidates.length === 0) return false;
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActiveCandidateIndex((prev) => (prev < 0 ? 0 : (prev + 1) % candidates.length));
      return true;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActiveCandidateIndex((prev) => (prev < 0 ? candidates.length - 1 : (prev - 1 + candidates.length) % candidates.length));
      return true;
    }
    if (event.key === "Tab") {
      event.preventDefault();
      if (activeCandidateIndex >= 0) applyCandidate(candidates[activeCandidateIndex]);
      return true;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setCandidates([]);
      setActiveCandidateIndex(0);
      return true;
    }
    return false;
  };

  const handleCandidateEnter = (event: KeyboardEvent | null): boolean => {
    if (candidates.length === 0) return false;
    // 输入法组合中的回车是「确认候选词」，Shift+Enter 是换行，都不抢。
    const native = event as (KeyboardEvent & { isComposing?: boolean; keyCode?: number }) | null | undefined;
    if (native?.isComposing || native?.keyCode === 229 || event?.shiftKey) return false;
    const index = activeCandidateIndex >= 0 ? activeCandidateIndex : 0;
    const candidate = candidates[index];
    if (!candidate) return false;
    event?.preventDefault();
    event?.stopPropagation();
    applyCandidate(candidate);
    return true;
  };

  return { handleCandidateKeyDown, handleCandidateEnter };
}
