import { SessionViewer } from "../components/session/SessionViewer";
import type { RelatedFileClickTarget, SessionItem, SlashCommandResult, GitFileStat } from "./appSession";
import type { ManagedRootPayload } from "./appMisc";
import type { AgentStatus } from "../services/agents";
import type { ActionHandlers } from "./useActionHandlers";
import type { useSessionPending } from "./useSessionPending";
import { sessionService } from "../services/session/sessionService";
import { promptDialog } from "../services/dialog";
import { useI18n } from "../i18n/index";

/**
 * 主对话区那一个 <SessionViewer>，连同它的 key 与全部回传回调。
 */
export type UseAppSessionViewParams = {
  selectedSessionSnapshot: ReturnType<ReturnType<typeof useSessionPending>["getSessionSnapshot"]>;
  selectedSessionPresence?: "running" | "awaiting" | "delegating" | "idle";
  availableAgents: AgentStatus[];
  slashCommandResultForSession: (rootID: string | null | undefined, session: { key?: string; session_key?: string } | null | undefined) => SlashCommandResult | null;
  selectedSession: SessionItem | null;
  currentRootId: string | null;
  sessionViewerComposerOverlayInset: number;
  selectedSessionLoading: boolean;
  getRootDisplayName: (rootId: string | null | undefined) => string;
  getDisplayNodeColor: (rootId: string) => string | null;
  managedRootByIdRef: React.RefObject<Record<string, ManagedRootPayload>>;
  gitFileStatsByPath: Record<string, GitFileStat>;
  handleSelectedSessionFileClick: (target: string | RelatedFileClickTarget) => void;
  actionHandlers: ActionHandlers;
  handleRemoveSessionRelatedFile: (rootID: string | null | undefined, sessionKey: string | undefined, path: string, head?: string, repoPath?: string, repoKind?: string) => Promise<void>;
  handleAskUserAnswer: (input: { rootId: string; sessionKey: string; agent?: string; toolUseId: string; answers: Record<string, string>; }) => Promise<void>;
  handleEditUserMessage: (content: string) => void;
  handleForkAgentMessage: (rootID: string | null | undefined, sessionKey: string | null | undefined, seq: number) => Promise<void>;
  handleRenameSession: (session: SessionItem, nextName: string) => Promise<boolean | void>;
  handleArchiveSession: (session: SessionItem, archived: boolean) => Promise<boolean | void>;
  handleDeleteSession: (session: SessionItem) => Promise<void>;
  getNodeIdForRoot: (rootId: string) => string | undefined;
};

/**
 * **key=会话键这条别动**：SessionViewer 的 visibleExchanges 是窗口化视图的唯一持久化源，
 * 不重挂载时它跨会话复用 —— 切换那一帧会先渲染**上一个会话**的窗口，紧接着 init effect
 * 拿还没就绪的 session.exchanges（此时为空）当种子 setVisibleExchanges([])，
 * 画面塌成空白，再等 getSessionWindow 回来才补上（实测 800ms）。
 * 用户看到的是「完整对话 → 塌掉 → 慢慢补回来」。重挂载让可见集与会话同生共死。
 *
 * root 一律取 `selectedSession?.root_id || currentRootId`：看板/工作台跳进来的会话
 * 可能不属于当前项目，只看 currentRootId 会给错的项目徽标与文件统计。
 *
 * 三个 onXxx 回调都在这里补 rootId 与 sessionKey 再上抛 —— 调用方只关心「第几段」，
 * 不该自己拼键。
 */
export function useAppSessionView(params: UseAppSessionViewParams) {
  const { selectedSessionSnapshot, selectedSessionPresence = "idle", availableAgents, slashCommandResultForSession, selectedSession, currentRootId, sessionViewerComposerOverlayInset, selectedSessionLoading, getRootDisplayName, getDisplayNodeColor, managedRootByIdRef, gitFileStatsByPath, handleSelectedSessionFileClick, actionHandlers, handleRemoveSessionRelatedFile, handleAskUserAnswer, handleEditUserMessage, handleForkAgentMessage, handleRenameSession, handleArchiveSession, handleDeleteSession, getNodeIdForRoot } = params;
  const { t } = useI18n();

  // 标题栏 ⋯ 菜单：会话对象取快照（它有 work_dir / agent），root 取会话自己的 root
  // 再退回当前项目 —— 与下面 rootId 的算法同口径，别用两套。
  const headerMenuRootId = selectedSession?.root_id || currentRootId;
  const headerMenuNodeId =
    String((selectedSessionSnapshot as any)?._nodeId || "") ||
    (headerMenuRootId ? getNodeIdForRoot(headerMenuRootId) : "");
  const headerMenu = selectedSessionSnapshot
    ? {
        agent: selectedSessionSnapshot.agent,
        rootId: headerMenuRootId,
        nodeId: headerMenuNodeId,
        rootPath:
          managedRootByIdRef.current[headerMenuRootId || ""]?.root_path || null,
        workDir: selectedSessionSnapshot.work_dir,
        archived: !!selectedSession?.archived_at,
        // 重命名走应用内输入弹窗（视图层不许直接调 dialog 服务，所以这里做）。
        onRename: () => {
          void (async () => {
            const next = await promptDialog({
              message: t("sessionList.rename"),
              defaultValue:
                selectedSessionSnapshot.name ||
                selectedSessionSnapshot.key ||
                selectedSessionSnapshot.session_key ||
                "",
              confirmLabel: t("sessionList.confirmRename"),
              cancelLabel: t("sessionList.cancelRename"),
            });
            if (next === null) return;
            void handleRenameSession(selectedSessionSnapshot, next);
          })();
        },
        onArchive: (archived: boolean) => {
          void handleArchiveSession(selectedSessionSnapshot, archived);
        },
        onDelete: () => {
          void handleDeleteSession(selectedSessionSnapshot);
        },
        // 换目录：搬转录 + 清 related + 钉新 cwd 由后端一次做完，成功后服务端广播
        // session.repointed，前端靠那条帧失效重拉（realtimeWsHandlers 已消费）。
        // 这里不自己刷新视图，避免与广播打架出两份状态。
        onRepoint: async (targetPath: string) => {
          if (!headerMenuRootId) return;
          const key =
            selectedSessionSnapshot.key || selectedSessionSnapshot.session_key || "";
          if (!key) return;
          await sessionService.repointSession(
            headerMenuRootId,
            key,
            targetPath,
            headerMenuNodeId,
          );
        },
      }
    : null;

  const sessionView = (
    <SessionViewer
      // key=会话键：切会话必须重挂载。SessionViewer 的 visibleExchanges 是窗口化视图的
      // 唯一持久化源，不重挂载时它跨会话复用 —— 切换那一帧先渲染**上一个会话**的窗口
      // （实测 2026-10-05：comp=20 seq=26..45 挂在已切走的会话上），紧接着 init effect
      // 用还没就绪的 session.exchanges 当种子（此时为空）setVisibleExchanges([])，
      // 画面塌成空白，再等 getSessionWindow 回来才补上（实测 800ms）。
      // 就是「完整对话 → 塌掉 → 慢慢补回来」。重挂载让可见集与会话同生共死。
      key={selectedSessionSnapshot?.key || "no-session"}
      session={selectedSessionSnapshot}
      presence={selectedSessionPresence}
      agents={availableAgents}
      slashCommandResult={slashCommandResultForSession(
        selectedSession?.root_id || currentRootId,
        selectedSessionSnapshot,
      )}
      targetSeq={selectedSession?.search_seq}
      targetSeqRequestKey={selectedSession?.search_target_id}
      composerOverlayInset={sessionViewerComposerOverlayInset}
      loading={selectedSessionLoading}
      rootId={selectedSession?.root_id || currentRootId}
      rootDisplayName={getRootDisplayName(selectedSession?.root_id || currentRootId)}
      rootColor={getDisplayNodeColor(String(selectedSession?.root_id || currentRootId || ""))}
      rootPath={
        managedRootByIdRef.current[
          selectedSession?.root_id || currentRootId || ""
        ]?.root_path || null
      }
      gitFileStatsByPath={gitFileStatsByPath}
      onFileClick={handleSelectedSessionFileClick}
      onRootClick={(root) => {
        void actionHandlers.open_dir({
          path: root,
          root,
          isRoot: true,
          forceDirectory: true,
          suppressTreeExpand: true,
        });
      }}
      onRemoveRelatedFile={(path, head, repoPath, repoKind) =>
        void handleRemoveSessionRelatedFile(
          selectedSession?.root_id || currentRootId,
          selectedSessionSnapshot?.key || selectedSessionSnapshot?.session_key,
          path,
          head,
          repoPath,
          repoKind,
        )
      }
      onAskUserAnswer={handleAskUserAnswer}
      onEditUserMessage={handleEditUserMessage}
      headerMenu={headerMenu}
      onForkAgentMessage={(seq) =>
        void handleForkAgentMessage(
          selectedSession?.root_id || currentRootId,
          selectedSessionSnapshot?.key || selectedSessionSnapshot?.session_key,
          seq,
        )
      }
    />
  );

  return {
    sessionView,
  };
}
