// 从 App.tsx 抽出的渲染区域（机械搬移，JSX 逐字未改）。
import { useI18n } from "../../i18n";
import type { Dispatch, SetStateAction } from "react";
import { e2eeService, type E2EEState } from "../../services/net/e2ee";
import {
  bootstrapService,
  type BootstrapState,
} from "../../services/net/bootstrap";

export interface E2eePromptProps {
  bootstrapState: BootstrapState;
  e2eePromptBusy: boolean;
  e2eePromptError: string;
  e2eeSecretInput: string;
  e2eeState: E2EEState;
  setE2eePromptError: Dispatch<SetStateAction<string>>;
  setE2eeSecretInput: Dispatch<SetStateAction<string>>;
  submitE2EESecret: () => Promise<void>;
  t: ReturnType<typeof useI18n>["t"];
}

export function E2eePrompt(props: E2eePromptProps) {
  const bootstrapState = props.bootstrapState;
  const e2eePromptBusy = props.e2eePromptBusy;
  const e2eePromptError = props.e2eePromptError;
  const e2eeSecretInput = props.e2eeSecretInput;
  const e2eeState = props.e2eeState;
  const setE2eePromptError = props.setE2eePromptError;
  const setE2eeSecretInput = props.setE2eeSecretInput;
  const submitE2EESecret = props.submitE2EESecret;
  const t = props.t;

  return (
    <>
    {bootstrapState.phase === "needs_pairing" &&
            e2eeState.required &&
            !e2eeState.unlocked ? (
            <div
              style={{
                position: "fixed",
                inset: 0,
                background: "rgba(15, 23, 42, 0.46)",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                padding: "24px",
                zIndex: 2000,
              }}
            >
              <div
                style={{
                  width: "min(460px, 100%)",
                  background: "#fff",
                  borderRadius: "20px",
                  padding: "24px",
                  boxShadow: "0 28px 80px rgba(15, 23, 42, 0.22)",
                  display: "flex",
                  flexDirection: "column",
                  gap: "14px",
                }}
    	          >
    	            <div style={{ fontSize: "20px", fontWeight: 700, color: "#0f172a" }}>
    	              {t("e2ee.title")}
    	            </div>
    	            <input
    	              type="text"
                  value={e2eeSecretInput}
                  onChange={(event) => {
                    setE2eeSecretInput(event.target.value);
                    if (e2eePromptError) {
                      setE2eePromptError("");
                    }
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && !e2eePromptBusy) {
                      void submitE2EESecret();
                    }
                  }}
    	              placeholder={t("e2ee.placeholder")}
                  autoFocus
                  spellCheck={false}
                  style={{
                    width: "100%",
                    borderRadius: "14px",
                    border: "1px solid rgba(148, 163, 184, 0.4)",
                    padding: "14px 16px",
                    fontSize: "14px",
                    outline: "none",
                  }}
                />
                {e2eePromptError ? (
                  <div style={{ color: "#dc2626", fontSize: "13px" }}>
                    {e2eePromptError}
                  </div>
                ) : null}
                <div style={{ display: "flex", justifyContent: "space-between", gap: "12px" }}>
                  <button
                    type="button"
                    onClick={() => {
                      setE2eeSecretInput("");
                      setE2eePromptError("");
                    }}
                    style={{
                      border: "none",
                      background: "transparent",
                      color: "#64748b",
                      padding: 0,
                      cursor: "pointer",
                    }}
                  >
                    {t("e2ee.clear")}
                  </button>
                  <button
                    type="button"
                    onClick={() => void submitE2EESecret()}
                    disabled={e2eePromptBusy}
                    style={{
                      border: "none",
                      borderRadius: "999px",
                      background: e2eePromptBusy ? "#94a3b8" : "#0f172a",
                      color: "#fff",
                      padding: "10px 18px",
                      cursor: e2eePromptBusy ? "not-allowed" : "pointer",
                      fontWeight: 600,
                    }}
                  >
                    {e2eePromptBusy ? t("e2ee.verifying") : t("e2ee.continue")}
                  </button>
                </div>
              </div>
            </div>
          ) : null}
    </>
  );
}
