import React from "react";
import { fetchAgents } from "../../services/agents";
import { getRootNodeId } from "../../services/net/rootNode";
import { fetchIdleSessionResourceReleasePreference, fetchSessionNamingPreference, updateIdleSessionResourceReleasePreference, updateNewProjectMetaLocationPreference, updateSessionNamingPreference } from "../../services/prefs/preferences";

export function useFileTreePreferences(ctx: any) {
  const { t, rootId, sendShortcut, sendShortcutDraft, onSendShortcutChange, agentConfigFlowVersion, setIdleReleaseOpen, setAgentConfigNotice, setAgentConfigFlow, setAgentLifecycleOpen, setIsMenuOpen, setSessionNamingOpen, setSessionNamingBusy, setSessionNamingError, setSessionNamingAgents, setSessionNamingAgent, setSessionNamingModel, setSessionNamingDisabled, sessionNamingAgent, sessionNamingBusy, sessionNamingDisabled, sessionNamingModel, setIdleReleaseBusy, setIdleReleaseError, setIdleReleaseHours, idleReleaseBusy, idleReleaseHours, setSendShortcutDraft, setSendShortcutError, setSendShortcutOpen, newProjectMetaLocation, setNewProjectMetaLocation, newProjectMetaLocationBusy, setNewProjectMetaLocationBusy } = ctx;
const openSessionNaming = React.useCallback(() => {
  setIdleReleaseOpen(false);
  agentConfigFlowVersion.current += 1;
  setAgentConfigNotice("");
  setAgentConfigFlow(null);
  setAgentLifecycleOpen(false);
  setIsMenuOpen(false);
  setSessionNamingOpen(true);
  setSessionNamingBusy(true);
  setSessionNamingError("");
  Promise.all([fetchAgents(true, getRootNodeId(rootId || "") as any), fetchSessionNamingPreference()])
    .then(([items, preference]) => {
      const installed = items.filter((item) => item.installed);
      setSessionNamingAgents(installed);
      const selected = installed.find((item) => item.name === preference.agent) || installed[0];
      const selectedModel = selected?.models?.find((item) => item.id === preference.model)?.id || "";
      setSessionNamingAgent(selected?.name || "");
      setSessionNamingModel(selectedModel);
      setSessionNamingDisabled(preference.disabled);
    })
    .catch((error) => {
      setSessionNamingError(error instanceof Error ? error.message : t("sessionNaming.loadFailed"));
    })
    .finally(() => setSessionNamingBusy(false));
}, [t]);

const saveSessionNaming = React.useCallback(async () => {
  if ((!sessionNamingAgent && !sessionNamingDisabled) || sessionNamingBusy) return;
  setSessionNamingBusy(true);
  setSessionNamingError("");
  try {
    await updateSessionNamingPreference({
      agent: sessionNamingAgent,
      model: sessionNamingModel,
      disabled: sessionNamingDisabled,
    });
    setSessionNamingOpen(false);
  } catch (error) {
    setSessionNamingError(error instanceof Error ? error.message : t("sessionNaming.saveFailed"));
  } finally {
    setSessionNamingBusy(false);
  }
}, [sessionNamingAgent, sessionNamingBusy, sessionNamingDisabled, sessionNamingModel, t]);

const openIdleSessionResourceRelease = React.useCallback(() => {
  agentConfigFlowVersion.current += 1;
  setAgentConfigNotice("");
  setAgentConfigFlow(null);
  setAgentLifecycleOpen(false);
  setIsMenuOpen(false);
  setIdleReleaseOpen(true);
  setIdleReleaseBusy(true);
  setIdleReleaseError("");
  fetchIdleSessionResourceReleasePreference()
    .then((preference) => setIdleReleaseHours(String(preference.hours)))
    .catch((error) => {
      setIdleReleaseError(error instanceof Error ? error.message : t("idleSessionResourceRelease.loadFailed"));
    })
    .finally(() => setIdleReleaseBusy(false));
}, [t]);

const openSendShortcut = React.useCallback(() => {
  agentConfigFlowVersion.current += 1;
  setAgentConfigNotice("");
  setAgentConfigFlow(null);
  setAgentLifecycleOpen(false);
  setSessionNamingOpen(false);
  setIdleReleaseOpen(false);
  setIsMenuOpen(false);
  setSendShortcutDraft(sendShortcut);
  setSendShortcutError("");
  setSendShortcutOpen(true);
}, [sendShortcut]);

const saveSendShortcut = React.useCallback(() => {
  onSendShortcutChange?.(sendShortcutDraft);
  setSendShortcutOpen(false);
}, [onSendShortcutChange, sendShortcutDraft]);

const saveIdleSessionResourceRelease = React.useCallback(async () => {
  if (idleReleaseBusy) return;
  const hours = Number(idleReleaseHours);
  if (!Number.isInteger(hours) || hours <= 0) {
    setIdleReleaseError(t("idleSessionResourceRelease.invalidHours"));
    return;
  }
  setIdleReleaseBusy(true);
  setIdleReleaseError("");
  try {
    const preference = await updateIdleSessionResourceReleasePreference({ hours });
    setIdleReleaseHours(String(preference.hours));
    setIdleReleaseOpen(false);
  } catch (error) {
    setIdleReleaseError(error instanceof Error ? error.message : t("idleSessionResourceRelease.saveFailed"));
  } finally {
    setIdleReleaseBusy(false);
  }
}, [idleReleaseBusy, idleReleaseHours, t]);

const toggleNewProjectMetaLocation = React.useCallback(async () => {
  if (newProjectMetaLocationBusy) return;
  const previous = newProjectMetaLocation;
  const next = previous === "home" ? "project" : "home";
  setNewProjectMetaLocation(next);
  setNewProjectMetaLocationBusy(true);
  try {
    setNewProjectMetaLocation(await updateNewProjectMetaLocationPreference(next));
  } catch {
    setNewProjectMetaLocation(previous);
  } finally {
    setNewProjectMetaLocationBusy(false);
  }
}, [newProjectMetaLocation, newProjectMetaLocationBusy]);

  return { openSessionNaming, saveSessionNaming, openIdleSessionResourceRelease, openSendShortcut, saveSendShortcut, saveIdleSessionResourceRelease, toggleNewProjectMetaLocation };
}
