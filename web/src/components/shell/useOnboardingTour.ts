import React, { useEffect, useRef, useState } from "react";
import { TOUR_STEPS, type TourStep } from "./onboardingSteps";

export type TargetRect = Pick<DOMRect, "top" | "left" | "right" | "bottom" | "width" | "height">;

export function useOnboardingTour({
  open,
  isMobile,
  onStepChange,
  onComplete,
  onDismiss,
}: {
  open: boolean;
  isMobile: boolean;
  onStepChange?: (stepId: string) => void;
  onComplete: () => void;
  onDismiss: () => void;
}) {
  const [stepIndex, setStepIndex] = useState(0);
  const [targetRect, setTargetRect] = useState<TargetRect | null>(null);
  const cardRef = useRef<HTMLDivElement | null>(null);

  const step: TourStep | undefined = TOUR_STEPS[stepIndex];

  useEffect(() => {
    if (!open) return;
    setStepIndex(0);
  }, [open]);

  useEffect(() => {
    if (!open || !step) return;
    onStepChange?.(step.id);

    let frame = 0;
    let settleTimer = 0;
    const updateRect = () => {
      const element = document.querySelector<HTMLElement>(step.target)
        ?? (step.fallbackTarget ? document.querySelector<HTMLElement>(step.fallbackTarget) : null);
      const rect = element?.getBoundingClientRect();
      if (rect && rect.width > 0 && rect.height > 0) {
        const padding = step.padding ?? 5;
        setTargetRect({
          top: step.pinToTop ? 0 : Math.max(6, rect.top - padding),
          left: Math.max(6, rect.left - padding),
          right: Math.min(window.innerWidth - 6, rect.right + padding),
          bottom: step.pinToTop
            ? rect.height + padding * 2
            : Math.min(window.innerHeight - 6, rect.bottom + padding),
          width: Math.min(window.innerWidth - 12, rect.width + padding * 2),
          height: Math.min(window.innerHeight - 12, rect.height + padding * 2),
        });
      } else {
        setTargetRect(null);
      }
    };
    const scheduleUpdate = () => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(updateRect);
    };
    scheduleUpdate();
    settleTimer = window.setTimeout(scheduleUpdate, 320);
    window.addEventListener("resize", scheduleUpdate);
    window.addEventListener("scroll", scheduleUpdate, true);
    return () => {
      window.cancelAnimationFrame(frame);
      window.clearTimeout(settleTimer);
      window.removeEventListener("resize", scheduleUpdate);
      window.removeEventListener("scroll", scheduleUpdate, true);
    };
  }, [onStepChange, open, step]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onDismiss();
      if (event.key === "ArrowLeft" && stepIndex > 0) setStepIndex((value) => value - 1);
      if (event.key === "ArrowRight") {
        if (stepIndex === TOUR_STEPS.length - 1) onComplete();
        else setStepIndex((value) => value + 1);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onComplete, onDismiss, open, stepIndex]);

  useEffect(() => {
    if (open) cardRef.current?.focus();
  }, [open, stepIndex]);

  const goNext = () => {
    if (stepIndex === TOUR_STEPS.length - 1) onComplete();
    else setStepIndex((value) => value + 1);
  };

  const goPrev = () => {
    setStepIndex((value) => Math.max(0, value - 1));
  };

  return {
    stepIndex,
    targetRect,
    cardRef,
    step,
    goNext,
    goPrev,
  };
}
