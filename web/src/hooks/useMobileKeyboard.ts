import { useEffect, useState } from "react";

/**
 * 是否是「手机软键盘」环境。
 *
 * 与布局的 `isMobile`（按窗口宽度算）不完全等价：回车语义关心的是**有没有 Ctrl 键**，
 * 所以主判据是 `(pointer: coarse)`，再用 768px 宽度兜底（窄窗口 / 调试用手机模拟器
 * 但没开 touch 的情况）。只在客户端求值，SSR 与单测首帧按桌面处理。
 */
export function useMobileKeyboard(): boolean {
  const [mobile, setMobile] = useState(false);

  useEffect(() => {
    const media = typeof window.matchMedia === "function" ? window.matchMedia("(pointer: coarse)") : null;
    const check = () => setMobile(Boolean(media?.matches) || window.innerWidth < 768);
    check();
    media?.addEventListener?.("change", check);
    window.addEventListener("resize", check);
    return () => {
      media?.removeEventListener?.("change", check);
      window.removeEventListener("resize", check);
    };
  }, []);

  return mobile;
}
