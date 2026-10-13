import { useRef, useState } from "react";
import { useResponsive } from "./appMisc";

/** 响应式壳层、onboarding 与侧栏显示状态。 */
export function useAppResponsiveState() {
  const { isMobile, isTablet } = useResponsive();
  const [isLeftOpen, setIsLeftOpen] = useState(() => window.innerWidth >= 768);
  const [isRightOpen, setIsRightOpen] = useState(() => window.innerWidth >= 768);
  const [onboardingOpen, setOnboardingOpen] = useState(false);
  const [onboardingMainContentViewRoot, setOnboardingMainContentViewRoot] = useState<string | null>(null);
  const onboardingAutoStartRef = useRef(false);

  return {
    isMobile, isTablet, isLeftOpen, setIsLeftOpen, isRightOpen, setIsRightOpen,
    onboardingOpen, setOnboardingOpen, onboardingMainContentViewRoot, setOnboardingMainContentViewRoot,
    onboardingAutoStartRef,
  };
}
