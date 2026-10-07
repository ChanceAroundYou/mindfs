// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import {
  completeOnboarding,
  dismissOnboarding,
  shouldAutoStartOnboarding,
} from "../../services/prefs/onboarding";
import { OnboardingTour } from "../shell/OnboardingTour";
import type { Dispatch, SetStateAction } from "react";

export interface OnboardingGateProps {
  completeOnboarding: any;
  dismissOnboarding: any;
  handleOnboardingStepChange: (stepId: string) => void;
  isMobile: boolean;
  onboardingOpen: boolean;
  setIsLeftOpen: Dispatch<SetStateAction<boolean>>;
  setIsRightOpen: Dispatch<SetStateAction<boolean>>;
  setOnboardingMainContentViewRoot: Dispatch<SetStateAction<string | null>>;
  setOnboardingOpen: Dispatch<SetStateAction<boolean>>;
}

export function OnboardingGate(props: OnboardingGateProps) {
  const completeOnboarding = props.completeOnboarding;
  const dismissOnboarding = props.dismissOnboarding;
  const handleOnboardingStepChange = props.handleOnboardingStepChange;
  const isMobile = props.isMobile;
  const onboardingOpen = props.onboardingOpen;
  const setIsLeftOpen = props.setIsLeftOpen;
  const setIsRightOpen = props.setIsRightOpen;
  const setOnboardingMainContentViewRoot = props.setOnboardingMainContentViewRoot;
  const setOnboardingOpen = props.setOnboardingOpen;

  return (
    <>
    {!isMobile ? <OnboardingTour
            open={onboardingOpen}
            isMobile={isMobile}
            onStepChange={handleOnboardingStepChange}
            onComplete={() => {
              completeOnboarding();
              setOnboardingOpen(false);
              setOnboardingMainContentViewRoot(null);
              if (isMobile) {
                setIsLeftOpen(false);
                setIsRightOpen(false);
              }
            }}
            onDismiss={() => {
              dismissOnboarding();
              setOnboardingOpen(false);
              setOnboardingMainContentViewRoot(null);
              if (isMobile) {
                setIsLeftOpen(false);
                setIsRightOpen(false);
              }
            }}
          /> : null}
    </>
  );
}
