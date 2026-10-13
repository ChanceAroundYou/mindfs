import React, { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ExitIcon } from "../common/ExitIcon";
import { useI18n } from "../../i18n/index";
import { fetchFile, type FilePayload } from "../../services/file";
import { type FileEditSession, type FileEditStore } from "../../services/fileEditing";
import { CodeTextEditor } from "../editor/CodeTextEditor";

const FILE_EDITOR_STYLES = String.raw`.file-editor { display: flex; flex-direction: column; flex: 1; min-height: 0; min-width: 0; color: var(--text-primary); }
.file-editor-button { border: 1px solid var(--border-color); border-radius: 6px; padding: 5px 10px; background: transparent; color: var(--text-primary); font: inherit; font-size: 12px; cursor: pointer; white-space: nowrap; }
.file-editor-button.primary { background: var(--accent-color); border-color: var(--accent-color); color: var(--accent-foreground, #fff); }
.file-editor-button:disabled { opacity: .5; cursor: default; }
.file-editor-button:focus-visible { outline: 2px solid var(--accent-color); outline-offset: 2px; }
.file-editor-error { padding: 10px 16px; border-bottom: 1px solid var(--border-color); color: var(--text-primary); background: rgba(220, 80, 50, .10); font-size: 13px; overflow-wrap: anywhere; }
.file-editor-dialog { width: min(440px, calc(100vw - 32px)); box-sizing: border-box; padding: 24px; color: var(--text-primary); background: var(--menu-bg, var(--panel-bg)); border: 1px solid var(--border-color); border-radius: 10px; box-shadow: 0 24px 60px rgba(0,0,0,.24); }
.file-editor-dialog::backdrop { background: rgba(0,0,0,.4); }
.file-editor-dialog h2 { font-size: 17px; margin: 0 0 12px; }
.file-editor-dialog p { font-size: 14px; line-height: 1.6; color: var(--text-secondary); margin: 0 0 24px; }
.file-editor-dialog-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 8px; }
@media (max-width: 768px) {
  .file-editor-dialog-actions .file-editor-button { min-height: 36px; }
}

.file-editor-actions { display: inline-flex; align-items: center; gap: 6px; flex-shrink: 0; }
.file-editor-actions:empty { display: none; }
.file-editor-icon-button { display: inline-flex; align-items: center; justify-content: center; width: 20px; height: 28px; padding: 0; border: 0; border-radius: 5px; background: transparent; color: var(--text-secondary); cursor: pointer; flex-shrink: 0; }
.file-editor-icon-button > svg { width: 18px; height: 18px; flex-shrink: 0; }
.file-editor-icon-button:hover:not(:disabled) { background: var(--border-color); color: var(--text-primary); }
.file-editor-icon-button.is-dirty { color: var(--accent-color); }
.file-editor-icon-button:disabled { opacity: .45; cursor: default; }
.file-editor-icon-button:focus-visible { outline: 2px solid var(--accent-color); outline-offset: -2px; }
.file-editor-save-success { position: fixed; z-index: 10000; transform: translateX(-50%); width: 24px; height: 24px; display: flex; align-items: center; justify-content: center; border-radius: 6px; color: #fff; background: #16803d; box-shadow: 0 2px 8px rgba(0,0,0,.16); pointer-events: none; }
.file-editor-loading { padding: 16px; font-size: 13px; color: var(--text-secondary); }

/* CodeMirror owns the editing surface; keep its metrics identical in every theme. */
.file-code-editor {
  flex: 1;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
  --file-code-comment: #64748b;
  --file-code-keyword: #7c3aed;
  --file-code-string: #16703a;
  --file-code-number: #b45309;
  --file-code-type: #0e7490;
  --file-code-function: #1d4ed8;
  --file-code-property: #9d174d;
  --file-code-invalid: #dc2626;
}
.file-code-editor .cm-editor { height: 100%; color: var(--text-primary); background: var(--panel-bg, transparent); }
.file-code-editor .cm-editor.cm-focused { outline: none; }
.file-code-editor .cm-scroller { overflow: auto; font-family: Menlo, Monaco, "Courier New", monospace; font-size: 13px; line-height: 20px; }
.mindfs-font-scale-region .file-code-editor .cm-scroller { font-size: calc(13px * var(--mindfs-font-scale, 1)); }
.file-code-editor .cm-content { padding: 16px 0; caret-color: var(--text-primary); }
.file-code-editor .cm-line { padding: 0 16px 0 12px; }
.file-code-editor .cm-gutters { background: var(--panel-bg); color: var(--text-secondary); border-right: 1px solid var(--border-color); }
.file-code-editor .cm-lineNumbers .cm-gutterElement { padding: 0 10px 0 12px; min-width: 32px; }
.file-code-editor .cm-activeLineGutter { background: var(--selection-bg); color: var(--accent-color); }
.file-code-editor .cm-cursor, .file-code-editor .cm-dropCursor { border-left-color: var(--text-primary); }
.file-code-editor .cm-selectionBackground, .file-code-editor .cm-focused .cm-selectionBackground { background: var(--selection-bg); }
.file-code-editor .cm-selectionMatch { background: var(--selection-bg); }
.file-code-editor .cm-matchingBracket { background: var(--selection-bg); outline: 1px solid var(--accent-color); }
.file-editor-language-notice { padding: 8px 16px; color: var(--text-secondary); font-size: 12px; }
:root[data-theme="dark"] .file-code-editor {
  --file-code-comment: #94a3b8;
  --file-code-keyword: #c4b5fd;
  --file-code-string: #86efac;
  --file-code-number: #fcd34d;
  --file-code-type: #67e8f9;
  --file-code-function: #93c5fd;
  --file-code-property: #f9a8d4;
  --file-code-invalid: #fca5a5;
}
@media (prefers-color-scheme: dark) {
  :root:not([data-theme]) .file-code-editor {
    --file-code-comment: #94a3b8;
    --file-code-keyword: #c4b5fd;
    --file-code-string: #86efac;
    --file-code-number: #fcd34d;
    --file-code-type: #67e8f9;
    --file-code-function: #93c5fd;
    --file-code-property: #f9a8d4;
    --file-code-invalid: #fca5a5;
  }
}
@media (max-width: 768px) {
  .file-code-editor .cm-lineNumbers .cm-gutterElement { padding-left: 8px; padding-right: 8px; }
}`;

type Props = {
  actionsTarget: HTMLElement | null;
  store: FileEditStore;
  editKey: string;
  session: FileEditSession;
  isVisible: boolean;
  onFileUpdated: (file: FilePayload) => void;
  onFileSaved: (file: FilePayload) => void;
  onExitError: (error: string) => void;
};

export function FileEditor({ actionsTarget, store, editKey, session, isVisible, onFileUpdated, onFileSaved, onExitError }: Props) {
  const { t } = useI18n();
  const dialog = useRef<HTMLDialogElement>(null);
  const saveButton = useRef<HTMLButtonElement>(null);
  const successTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mounted = useRef(false);
  const [saveSuccess, setSaveSuccess] = useState<{ left: number; top: number } | null>(null);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      if (successTimer.current !== null) clearTimeout(successTimer.current);
    };
  }, []);

  useEffect(() => {
    if (!isVisible) setSaveSuccess(null);
  }, [isVisible]);
  const [confirmExit, setConfirmExit] = useState(false);
  const leaving = session.leaving;
  const dirty = session.text !== session.savedText;
  const busy = session.loading || session.saving || leaving;
  const loaded = !!session.revision;

  useEffect(() => {
    const node = dialog.current;
    if (confirmExit && isVisible && node && !node.open) node.showModal();
    else if (node?.open) node.close();
  }, [confirmExit, isVisible]);

  async function exit() {
    store.patch(editKey, { leaving: true });
    setConfirmExit(false);
    try {
      const file = await fetchFile({ rootId: session.rootId, path: session.path, readMode: "full", fresh: true, timeoutMs: 15000 });
      if (!file) throw new Error(t("fileEditor.reloadFailed"));
      onFileUpdated(file);
    } catch (error) {
      onExitError(`${t("fileEditor.reloadFailed")}: ${error instanceof Error ? error.message : String(error)}`);
    } finally {
      if (store.get(editKey)?.view === session.view) store.close(editKey);
    }
  }

  async function save(andExit = false) {
    if (successTimer.current !== null) clearTimeout(successTimer.current);
    setSaveSuccess(null);
    const saved = await store.save(editKey);
    if (!saved) { setConfirmExit(false); return; }
    onFileSaved(saved);
    if (mounted.current && !andExit && saveButton.current) {
      const rect = saveButton.current.getBoundingClientRect();
      setSaveSuccess({ left: rect.left + rect.width / 2, top: Math.max(4, rect.top - 28) });
      successTimer.current = setTimeout(() => setSaveSuccess(null), 2000);
    }
    const latest = store.get(editKey);
    if (andExit && latest && latest.text === latest.savedText) await exit();
  }

  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    if (!isVisible) return;
    const handler = (event: KeyboardEvent) => {
      if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== "s") return;
      event.preventDefault();
      if (!event.isComposing && !busy && loaded && dirty && !confirmExit) void saveRef.current();
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [isVisible, busy, loaded, dirty, confirmExit]);

  const errorText = session.error === "file_edit_conflict" ? t("fileEditor.conflict")
    : session.error === "file_edit_too_large" ? t("fileEditor.tooLarge")
    : session.error === "file_not_editable" ? t("fileEditor.notEditable") : session.error;

  return (
    <section className="file-editor" aria-label={t("fileEditor.editor")}>
      <style>{FILE_EDITOR_STYLES}</style>
      {actionsTarget && createPortal(<>
        <button ref={saveButton} type="button" className={`file-editor-icon-button${dirty ? " is-dirty" : ""}`} title={session.saving ? t("fileEditor.saving") : t("fileEditor.save")} aria-label={t("fileEditor.save")} aria-busy={session.saving} disabled={busy || !loaded || !dirty} onClick={() => { void save(); }}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h12l4 4v12a2 2 0 0 1-2 2Z" /><path d="M7 3v6h10V3M7 21v-8h10v8" /></svg>
        </button>
        <button type="button" className="file-editor-icon-button" title={leaving ? t("fileEditor.reloading") : t("fileEditor.exit")} aria-label={t("fileEditor.exit")} disabled={busy} onClick={() => { if (dirty) setConfirmExit(true); else void exit(); }}>
          <ExitIcon />
        </button>
      </>, actionsTarget)}
      {saveSuccess && isVisible && createPortal(<span className="file-editor-save-success" role="status" aria-label={t("fileEditor.saved")} style={saveSuccess}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m5 12 4 4L19 6" /></svg>
      </span>, document.body)}
      {session.loading && <div className="file-editor-loading" role="status">{t("fileEditor.loading")}</div>}
      {errorText && <div className="file-editor-error" role="alert">{errorText}</div>}
      {loaded && <CodeTextEditor store={store} editKey={editKey} session={session} isVisible={isVisible} />}
      <dialog ref={dialog} className="file-editor-dialog" aria-labelledby="file-editor-exit-title" aria-describedby="file-editor-exit-description" onCancel={(event) => { event.preventDefault(); if (!busy) setConfirmExit(false); }}>
        <h2 id="file-editor-exit-title">{t("fileEditor.confirmTitle")}</h2>
        <p id="file-editor-exit-description">{t("fileEditor.confirmDescription")}</p>
        <div className="file-editor-dialog-actions">
          <button type="button" autoFocus className="file-editor-button" disabled={busy} onClick={() => setConfirmExit(false)}>{t("fileEditor.continue")}</button>
          <button type="button" className="file-editor-button" disabled={busy} onClick={() => { void exit(); }}>{t("fileEditor.discard")}</button>
          <button type="button" className="file-editor-button primary" disabled={busy} onClick={() => { void save(true); }}>{session.saving ? t("fileEditor.saving") : t("fileEditor.saveExit")}</button>
        </div>
      </dialog>
    </section>
  );
}
