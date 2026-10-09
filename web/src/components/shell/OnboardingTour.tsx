import React from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import { TOUR_STEPS } from "./onboardingSteps";
import { useOnboardingTour } from "./useOnboardingTour";

type OnboardingTourProps = {
  open: boolean;
  isMobile: boolean;
  onStepChange?: (stepId: string) => void;
  onComplete: () => void;
  onDismiss: () => void;
};

export function OnboardingTour({
  open,
  isMobile,
  onStepChange,
  onComplete,
  onDismiss,
}: OnboardingTourProps) {
  const { t } = useI18n();
  const {
    stepIndex,
    targetRect,
    cardRef,
    step,
    goNext,
    goPrev,
  } = useOnboardingTour({ open, isMobile, onStepChange, onComplete, onDismiss });

  if (!open || !step || typeof document === "undefined") return null;

  const positionTarget = targetRect;
  const cardWidth = Math.min(360, window.innerWidth - 24);
  const cardLeft = isMobile
    ? 12
    : Math.max(12, Math.min(
        window.innerWidth - cardWidth - 12,
        positionTarget ? positionTarget.left + positionTarget.width / 2 - cardWidth / 2 : window.innerWidth / 2 - cardWidth / 2,
      ));
  const placeBelow = positionTarget ? positionTarget.bottom + 220 < window.innerHeight : false;
  const cardPosition: React.CSSProperties = isMobile
    ? { left: 12, right: 12, bottom: "calc(env(safe-area-inset-bottom, 0px) + 12px)" }
    : {
        left: cardLeft,
        top: positionTarget
          ? placeBelow
            ? positionTarget.bottom + 14
            : Math.max(12, positionTarget.top - 214)
          : "50%",
        transform: positionTarget ? undefined : "translateY(-50%)",
        width: cardWidth,
      };

  const overlayZIndex = step.interactive ? 900 : 5000;
  const spotlightZIndex = overlayZIndex + 1;
  const blockerStyle: React.CSSProperties = {
    position: "fixed",
    zIndex: overlayZIndex,
    pointerEvents: "auto",
  };

  return createPortal(
    <div role="dialog" aria-modal="true" aria-label={t("onboarding.dialogLabel")}>
      {step.interactive && targetRect ? (
        <>
          <div style={{ ...blockerStyle, top: 0, left: 0, right: 0, height: targetRect.top }} />
          <div style={{ ...blockerStyle, top: targetRect.bottom, left: 0, right: 0, bottom: 0 }} />
          <div style={{ ...blockerStyle, top: targetRect.top, left: 0, width: targetRect.left, height: targetRect.height }} />
          <div style={{ ...blockerStyle, top: targetRect.top, left: targetRect.right, right: 0, height: targetRect.height }} />
        </>
      ) : (
        <div style={{ ...blockerStyle, inset: 0 }} />
      )}
      {targetRect ? (
        <div
          aria-hidden="true"
          style={{
            position: "fixed",
            zIndex: spotlightZIndex,
            top: targetRect.top,
            left: targetRect.left,
            width: targetRect.width,
            height: targetRect.height,
            borderRadius: 12,
            border: "2px solid var(--accent-color)",
            boxShadow: "0 0 0 9999px rgba(15, 23, 42, 0.62), 0 0 0 5px rgba(59, 130, 246, 0.18)",
            pointerEvents: "none",
            transition: "all 0.22s cubic-bezier(0.2, 0.8, 0.2, 1)",
          }}
        />
      ) : (
        <div aria-hidden="true" style={{ position: "fixed", inset: 0, zIndex: spotlightZIndex, background: "rgba(15, 23, 42, 0.62)", pointerEvents: "none" }} />
      )}
      <div
        ref={cardRef}
        tabIndex={-1}
        style={{
          position: "fixed",
          zIndex: 5002,
          ...cardPosition,
          boxSizing: "border-box",
          border: "1px solid var(--panel-border)",
          borderRadius: 14,
          background: "var(--panel-bg)",
          color: "var(--text-primary)",
          boxShadow: "0 20px 60px rgba(15, 23, 42, 0.28)",
          padding: "16px",
          outline: "none",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, marginBottom: 10 }}>
          <span style={{ fontSize: 11, color: "var(--accent-color)", fontWeight: 750 }}>
            {t("onboarding.progress", { current: stepIndex + 1, total: TOUR_STEPS.length })}
          </span>
          <button type="button" onClick={onDismiss} style={quietButtonStyle}>
            {t("onboarding.skip")}
          </button>
        </div>
        <div style={{ fontSize: 17, lineHeight: 1.35, fontWeight: 750, marginBottom: 7 }}>{t(step.titleKey)}</div>
        <div style={{ fontSize: 13, lineHeight: 1.65, color: "var(--text-secondary)", minHeight: 43 }}>{t(step.bodyKey)}</div>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 10, marginTop: 16 }}>
          <button
            type="button"
            disabled={stepIndex === 0}
            onClick={goPrev}
            style={{ ...secondaryButtonStyle, opacity: stepIndex === 0 ? 0.45 : 1 }}
          >
            {t("onboarding.previous")}
          </button>
          <button
            type="button"
            onClick={goNext}
            style={primaryButtonStyle}
          >
            {stepIndex === TOUR_STEPS.length - 1 ? t("onboarding.finish") : t("onboarding.next")}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}

const quietButtonStyle: React.CSSProperties = {
  border: "none",
  background: "transparent",
  color: "var(--text-secondary)",
  padding: "4px 0",
  cursor: "pointer",
  fontSize: 12,
};

const secondaryButtonStyle: React.CSSProperties = {
  height: 34,
  border: "1px solid var(--border-color)",
  borderRadius: 9,
  background: "transparent",
  color: "var(--text-primary)",
  padding: "0 13px",
  cursor: "pointer",
  fontSize: 12,
  fontWeight: 650,
};

const primaryButtonStyle: React.CSSProperties = {
  ...secondaryButtonStyle,
  border: "1px solid var(--accent-color)",
  background: "var(--accent-color)",
  color: "#fff",
  minWidth: 78,
};
