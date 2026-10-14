import { useState } from "react";
import { deletePrompt, savePrompt } from "../services/prompts";

export function useCandidatePromptActions({
  onPromptSaved,
  onPromptDeleted,
  onPromptFormClosed,
  addErrorMessage,
  deleteErrorMessage,
}: {
  onPromptSaved: () => void;
  onPromptDeleted: (text: string) => void;
  onPromptFormClosed?: () => void;
  addErrorMessage: string;
  deleteErrorMessage: string;
}) {
  const [addingPrompt, setAddingPrompt] = useState(false);
  const [newPromptText, setNewPromptText] = useState("");
  const [savingPrompt, setSavingPrompt] = useState(false);
  const [deletingPrompt, setDeletingPrompt] = useState("");
  const [promptSaveError, setPromptSaveError] = useState("");

  const cancelAddPrompt = () => {
    if (savingPrompt) return;
    setAddingPrompt(false);
    setNewPromptText("");
    setPromptSaveError("");
    onPromptFormClosed?.();
  };

  const submitNewPrompt = async () => {
    const text = newPromptText.trim();
    if (!text || savingPrompt) return;
    setSavingPrompt(true);
    setPromptSaveError("");
    try {
      await savePrompt(text);
      setAddingPrompt(false);
      setNewPromptText("");
      onPromptSaved();
      onPromptFormClosed?.();
    } catch (error) {
      setPromptSaveError(String((error as Error)?.message || addErrorMessage));
    } finally {
      setSavingPrompt(false);
    }
  };

  const removePrompt = async (text: string) => {
    if (deletingPrompt) return;
    setDeletingPrompt(text);
    setPromptSaveError("");
    try {
      await deletePrompt(text);
      onPromptDeleted(text);
    } catch (error) {
      setPromptSaveError(String((error as Error)?.message || deleteErrorMessage));
    } finally {
      setDeletingPrompt("");
    }
  };

  return {
    addingPrompt,
    setAddingPrompt,
    newPromptText,
    setNewPromptText,
    savingPrompt,
    deletingPrompt,
    promptSaveError,
    setPromptSaveError,
    cancelAddPrompt,
    submitNewPrompt,
    removePrompt,
  };
}
