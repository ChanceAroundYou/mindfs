import { useState } from "react";
import { useResponsive } from "./appMisc";

/** 响应式壳层与侧栏显示状态。 */
export function useAppResponsiveState() {
  const { isMobile, isTablet } = useResponsive();
  const [isLeftOpen, setIsLeftOpen] = useState(() => window.innerWidth >= 768);
  const [isRightOpen, setIsRightOpen] = useState(() => window.innerWidth >= 768);
  return {
    isMobile, isTablet, isLeftOpen, setIsLeftOpen, isRightOpen, setIsRightOpen,
  };
}
