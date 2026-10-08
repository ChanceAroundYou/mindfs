import React from "react";
import { bootstrapService } from "../../services/net/bootstrap";
import { fetchIdleSessionResourceReleasePreference, fetchNewProjectMetaLocationPreference } from "../../services/prefs/preferences";

export function useFileTreeEffects(ctx: any) {
  const { creatingRootName, previousCreatingRootNameRef, createInputRef, setProtectedAPIReady, isUpdateNotesOpen, updateNotesRef, setIsUpdateNotesOpen, updateActionLabel, updateActionSummary, isMenuOpen, menuRef, setIsMenuOpen, sendShortcutOpen, sendShortcutPopoverRef, setSendShortcutOpen, protectedAPIReady, setIdleReleaseHours, setNewProjectMetaLocation } = ctx;
React.useEffect(() => {
  if (!creatingRootName) {
    previousCreatingRootNameRef.current = creatingRootName;
    return;
  }
  const enteredCreateMode = previousCreatingRootNameRef.current === null;
  createInputRef.current?.focus();
  if (enteredCreateMode) {
    createInputRef.current?.select();
  }
  previousCreatingRootNameRef.current = creatingRootName;
}, [creatingRootName]);

React.useEffect(() => {
  return bootstrapService.subscribe(() => {
    setProtectedAPIReady(bootstrapService.canUseProtectedAPI());
  });
}, []);

React.useEffect(() => {
  if (!isUpdateNotesOpen || typeof document === "undefined") {
    return;
  }

  const handlePointerDown = (event: MouseEvent) => {
    if (updateNotesRef.current && !updateNotesRef.current.contains(event.target as Node)) {
      setIsUpdateNotesOpen(false);
    }
  };

  document.addEventListener("mousedown", handlePointerDown);
  return () => {
    document.removeEventListener("mousedown", handlePointerDown);
  };
}, [isUpdateNotesOpen]);

React.useEffect(() => {
  if (!updateActionLabel || !updateActionSummary) {
    setIsUpdateNotesOpen(false);
  }
}, [updateActionLabel, updateActionSummary]);

React.useEffect(() => {
  if (!isMenuOpen) {
    return;
  }
  const handlePointerDown = (event: MouseEvent) => {
    if (!menuRef.current?.contains(event.target as Node)) {
      setIsMenuOpen(false);
    }
  };
  document.addEventListener("mousedown", handlePointerDown);
  return () => document.removeEventListener("mousedown", handlePointerDown);
}, [isMenuOpen]);

React.useEffect(() => {
  if (!sendShortcutOpen) return;
  const handlePointerDown = (event: MouseEvent) => {
    if (!sendShortcutPopoverRef.current?.contains(event.target as Node)) {
      setSendShortcutOpen(false);
    }
  };
  document.addEventListener("mousedown", handlePointerDown);
  return () => document.removeEventListener("mousedown", handlePointerDown);
}, [sendShortcutOpen]);

React.useEffect(() => {
  if (!protectedAPIReady) return;
  let cancelled = false;
  fetchIdleSessionResourceReleasePreference()
    .then((preference) => {
      if (!cancelled) setIdleReleaseHours(String(preference.hours));
    })
    .catch(() => {});
  fetchNewProjectMetaLocationPreference()
    .then((location) => {
      if (!cancelled) setNewProjectMetaLocation(location);
    })
    .catch(() => {});
  return () => {
    cancelled = true;
  };
}, [protectedAPIReady]);
}
