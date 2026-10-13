import type { RefObject } from "react";
import { ModeSelector, type SessionMode } from "../agent/ModeSelector";
import { AgentSelector } from "../agent/AgentSelector";
import { fetchAgents, restartAgent, type AgentStatus, type ShellStatus } from "../../services/agents";
import { getRootNodeId } from "../../services/net/rootNode";
import { getAgentDefaults, strip1MSuffix, resolveEffortOnSwitch, resolveLongContextOnSwitch } from "./modelUtils";
import { appAccentHexToRgba } from "./styleHelpers";
import { SessionRing } from "./SessionRing";
import { ShellSelector } from "./ShellSelector";
import { CancelIcon, PlusIcon, SendIcon, SpinnerIcon } from "./composerStyles";
import type { PendingAttachment } from "./AttachmentsArea";
import type { SessionInfo } from "./actionTypes";
import type { MessageKey } from "../../i18n/index";

export type ActionBarControlsProps = {
  isMobile: boolean;
  isMultiLine: boolean;
  hasBoundSession: boolean;
  canOpenSessionDrawer: boolean;
  sessionDrawerOpen: boolean;
  detachedBoundSession: boolean;
  accentColorRaw: string;
  accentHex: string;
  currentSession: SessionInfo | null | undefined;
  currentRootId: string | null | undefined;
  onSessionClick?: () => void;
  onDraggingChange: (dragging: boolean) => void;
  onNewSession?: () => void;
  resetForNewSession: () => void;
  onSelectProject?: (rootId: string) => void;
  onSelectSession?: (session: any) => void;
  mode: SessionMode;
  setMode: (mode: SessionMode) => void;
  isModeLocked: boolean;
  setModeSelectorOpen: (open: boolean) => void;
  agent: string;
  model: string;
  agentMode: string;
  effort: string;
  longContext: boolean;
  fastService: "" | "on" | "off";
  agents: AgentStatus[];
  setAgent: (agent: string) => void;
  setModel: (model: string) => void;
  setAgentMode: (mode: string) => void;
  setEffort: (effort: string) => void;
  setFastService: (fastService: "" | "on" | "off") => void;
  setLongContext: (enabled: boolean) => void;
  setAgents: (agents: AgentStatus[]) => void;
  isSelectedAgentUnavailable: boolean;
  setAgentSelectorOpen: (open: boolean) => void;
  shell: string;
  shells: ShellStatus[];
  setShell: (shell: string) => void;
  sending: boolean;
  pendingAttachments: PendingAttachment[];
  attachmentInputRef: RefObject<HTMLInputElement | null>;
  appendPendingAttachments: (files: File[]) => void;
  showCancel: boolean;
  handleCancel: () => void;
  handleSend: () => void;
  cancelling: boolean;
  canSend: boolean;
  t: (key: MessageKey) => string;
};

export function ActionBarControls(props: ActionBarControlsProps) {
  const {
    isMobile,
    isMultiLine,
    hasBoundSession,
    canOpenSessionDrawer,
    sessionDrawerOpen,
    detachedBoundSession,
    accentColorRaw,
    accentHex,
    currentSession,
    currentRootId,
    onSessionClick,
    onDraggingChange,
    onNewSession,
    resetForNewSession,
    onSelectProject,
    onSelectSession,
    mode,
    setMode,
    isModeLocked,
    setModeSelectorOpen,
    agent,
    model,
    agentMode,
    effort,
    longContext,
    fastService,
    agents,
    setAgent,
    setModel,
    setAgentMode,
    setEffort,
    setFastService,
    setLongContext,
    setAgents,
    isSelectedAgentUnavailable,
    setAgentSelectorOpen,
    shell,
    shells,
    setShell,
    sending,
    pendingAttachments,
    attachmentInputRef,
    appendPendingAttachments,
    showCancel,
    handleCancel,
    handleSend,
    cancelling,
    canSend,
    t,
  } = props;
  return (
    <div data-onboarding="input-controls" style={{ position: "absolute", right: isMobile ? "4px" : "8px", bottom: isMultiLine ? "6px" : "50%", transform: isMultiLine ? "none" : "translateY(50%)", display: "flex", alignItems: "center", gap: isMobile ? "0px" : "2px", zIndex: 5, transition: "all 0.2s cubic-bezier(0.4, 0, 0, 2, 1)" }}>
      <SessionRing
        hasBoundSession={hasBoundSession}
        canOpenSessionDrawer={canOpenSessionDrawer}
        sessionDrawerOpen={sessionDrawerOpen}
        detachedBoundSession={detachedBoundSession}
        accentColorRaw={accentColorRaw}
        accentHex={accentHex}
        currentSession={currentSession ?? null}
        currentRootId={currentRootId}
        onSessionClick={onSessionClick ?? (() => {})}
        onDraggingChange={onDraggingChange}
        onNewSession={onNewSession ?? (() => {})}
        onResetForNewSession={resetForNewSession}
        onSelectProject={onSelectProject ?? (() => {})}
        onSelectSession={onSelectSession ?? (() => {})}
      />

      <>
        <ModeSelector mode={mode} onModeChange={setMode} compact={true} disabled={isModeLocked} onboardingId="mode-selector" viewportMenu accentColor={accentHex} onOpenChange={setModeSelectorOpen} />
        {mode !== "command" ? (
          <div>
            <AgentSelector
              agent={agent}
              model={model}
              mode={agentMode}
              effort={effort}
              longContext={longContext}
              agents={agents}
              onAgentChange={(nextAgent, nextModel) => {
                const nextStatus = agents.find((item) => item.name === nextAgent);
                const defaults = getAgentDefaults(nextStatus);
                const explicitModel = String(nextModel || "").trim();
                const nextBaseModel = explicitModel || defaults.model;
                const nextModelInfo = nextStatus?.models?.find(
                  (item) => item.id === nextBaseModel || strip1MSuffix(item.id) === strip1MSuffix(nextBaseModel),
                );
                const nextAvailableEfforts = nextModelInfo?.efforts ?? nextStatus?.efforts ?? [];
                setAgent(nextAgent);
                setModel(nextBaseModel);
                setAgentMode("");
                setEffort(
                  resolveEffortOnSwitch({
                    nextAgent,
                    nextModel: nextBaseModel,
                    prevEffort: effort,
                    defaultEffort: defaults.effort,
                    availableEfforts: nextAvailableEfforts,
                  }),
                );
                setFastService(defaults.fastService);
                // 1M 跟着用户走：下拉传回的是不带后缀的裸 id，不能据此判定"用户关了 1M"。
                setLongContext(
                  resolveLongContextOnSwitch({
                    nextAgent,
                    nextModel: nextBaseModel,
                    prevLongContext: longContext,
                  }),
                );
              }}
              onModeChange={(nextAgentMode) => setAgentMode(nextAgentMode || "")}
              onEffortChange={(nextEffort) => setEffort(nextEffort || "")}
              onLongContextChange={(next) => setLongContext(!!next)}
              fastService={fastService}
              onFastServiceChange={(nextFastService) => setFastService(nextFastService || "")}
              onAgentRestart={async (targetAgent) => {
                const nid = getRootNodeId(currentRootId || "") as any;
                await restartAgent(targetAgent, nid);
                const items = await fetchAgents(true, nid);
                setAgents(items);
              }}
              compact={true}
              warnUnavailable={isSelectedAgentUnavailable}
              defaultExpandOptions
              onboardingId="agent-selector"
              viewportMenu
              onOpenChange={setAgentSelectorOpen}
            />
          </div>
        ) : (
          <ShellSelector
            shell={shell}
            shells={shells}
            onShellChange={setShell}
            compact={true}
          />
        )}
        <button
          data-onboarding="attachment-action"
          type="button"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => attachmentInputRef.current?.click()}
          disabled={!currentRootId || sending}
          style={{
            width: "28px",
            height: "28px",
            borderRadius: "8px",
            border: "none",
            background: pendingAttachments.length > 0
              ? appAccentHexToRgba(accentHex, 0.14)
              : "transparent",
            color: pendingAttachments.length > 0
              ? accentColorRaw
              : "var(--text-secondary)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            cursor: !currentRootId || sending ? "not-allowed" : "pointer",
            opacity: !currentRootId || sending ? 0.35 : 1,
          }}
          title={t("action.addAttachment")}
          aria-label={t("action.addAttachment")}
        >
          <PlusIcon />
        </button>
        <button
          data-onboarding="send-action"
          type="button"
          onMouseDown={(event) => event.preventDefault()}
          onClick={showCancel ? handleCancel : handleSend}
          disabled={showCancel ? cancelling : !canSend}
          style={{ width: "28px", height: "28px", borderRadius: "8px", border: "none", background: showCancel ? "rgba(239,68,68,0.14)" : (canSend ? accentColorRaw : "transparent"), color: showCancel ? "#ef4444" : (canSend ? "#fff" : "var(--text-secondary)"), display: "flex", alignItems: "center", justifyContent: "center", cursor: showCancel ? (cancelling ? "wait" : "pointer") : (canSend ? "pointer" : "not-allowed"), transition: "all 0.2s", opacity: showCancel ? 1 : (canSend ? 1 : 0.3) }}
        >
          {sending || cancelling ? (
            <SpinnerIcon />
          ) : showCancel ? (
            <CancelIcon />
          ) : (
            <SendIcon />
          )}
        </button>
      </>
      <input
        ref={attachmentInputRef}
        type="file"
        multiple
        style={{ display: "none" }}
        onChange={(event) => {
          const selectedFiles = Array.from(event.target.files || []);
          if (selectedFiles.length > 0) {
            appendPendingAttachments(selectedFiles);
          }
          event.currentTarget.value = "";
        }}
      />
    </div>
  );
}
