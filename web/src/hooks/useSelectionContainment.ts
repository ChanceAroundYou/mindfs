import { useEffect, type RefObject } from "react";
import { clampSelectionToRoot } from "../shared/selectionClamp";

/**
 * 把拖选限制在 `ref` 指向的容器里：一端在容器内、另一端被拖到容器外的选区，
 * 会被拉回容器边界（语义见 `shared/selectionClamp.ts`）。
 *
 * 挂在聊天滚动容器上：向上拖到会话标题栏时，选区只到正文顶部为止，标题、header、
 * 侧栏、输入条都不会被带进复制结果。
 *
 * **拖选进行中不改写选区**：浏览器拖到容器外时每次 mousemove 都会重新把末端放到指针
 * 位置，这时 `removeAllRanges`+`addRange` 会和拖选状态互相打架（实测最终选区被清空）。
 * 所以按指针按下/抬起记一个状态，只在抬手后（以及非拖选的键盘扩选）收紧一次。
 */
export function useSelectionContainment(ref: RefObject<HTMLElement | null>): void {
  useEffect(() => {
    if (typeof document === "undefined") return;
    let pointerDown = false;
    const clampSelection = () => {
      if (pointerDown) return;
      clampSelectionToRoot(ref.current, document.getSelection());
    };
    const handlePointerDown = () => {
      pointerDown = true;
    };
    const handlePointerRelease = () => {
      pointerDown = false;
      clampSelection();
    };
    document.addEventListener("selectionchange", clampSelection);
    document.addEventListener("pointerdown", handlePointerDown, true);
    document.addEventListener("pointerup", handlePointerRelease, true);
    document.addEventListener("pointercancel", handlePointerRelease, true);
    window.addEventListener("blur", handlePointerRelease);
    return () => {
      document.removeEventListener("selectionchange", clampSelection);
      document.removeEventListener("pointerdown", handlePointerDown, true);
      document.removeEventListener("pointerup", handlePointerRelease, true);
      document.removeEventListener("pointercancel", handlePointerRelease, true);
      window.removeEventListener("blur", handlePointerRelease);
    };
  }, [ref]);
}