import type React from "react";
import { useI18n } from "../i18n";
import type { AgentStatus } from "../services/agents";
import { AgentIcon } from "../components/AgentIcon";
import { ChevronDownSmallIcon, ImportIcon } from "./taskIcons";
import { AgentMenuList } from "../components/AgentMenuList";
import { ExternalSessionList } from "../components/ExternalSessionList";
import {
  MultiProjectSessionList,
  SessionList,
  type ProjectSessionGroup,
  type SessionItem,
  type SessionListProps,
} from "../components/SessionList";
import type { DirectorySortMode } from "../services/directorySort";

/**
 * 会话右栏：导入入口（agent 菜单 + 只看未导入开关）+ 三种列表形态的切换。
 *
 * 三态互斥：外部导入模式 > 多项目聚合 > 单项目列表（搜索结果态由调用方换 sessions 传进来）。
 * 字段名与调用方（App）保持一致；同名字段（sessions/onLoadChildren/…）在两个列表组件里
 * 签名兼容，所以直接复用 SessionListProps 的字段类型，不重复抄一遍。
 */
export type SessionSidebarViewParams = {
  // 导入入口
  importMenuRef: React.RefObject<HTMLDivElement | null>;
  importMenuOpen: boolean;
  setImportMenuOpen: React.Dispatch<React.SetStateAction<boolean>>;
  availableAgents: AgentStatus[];
  setExternalImportAgent: (agent: string) => void;
  setExternalSelectedKey: (key: string) => void;
  enterImportMode: (agentName: string) => Promise<void>;
  setExternalFilterBound: React.Dispatch<React.SetStateAction<boolean>>;

  // 三态互斥判定
  sessionListMode: "local" | "import";
  multiProjectSessionsEnabled: boolean;
  sessionSearchOpen: boolean;
  sessionSearchResultsMode: boolean;

  // 外部导入列表
  externalSessions: SessionItem[];
  externalSelectedKey: string;
  externalImportAgent: string;
  externalFilterBound: boolean;
  importingExternalSessionKeys: Set<string>;
  selectedExternalImportKeys: Set<string>;
  loadingExternalSessions: boolean;
  externalSessionsError: string;
  loadingOlderExternalSessions: boolean;
  confirmingExternalImport: boolean;
  hasMoreExternalSessions: boolean;
  exitImportMode: () => void;
  toggleExternalImportSelection: (session: SessionItem) => void;
  toggleAllExternalImportSelection: (checked: boolean) => void;
  handleConfirmExternalImport: () => Promise<void>;
  handleLoadOlderExternalSessions: () => Promise<void>;

  // 多项目聚合列表
  multiProjectSessionGroups: ProjectSessionGroup[];
  multiProjectSessionsLoading: boolean;
  currentRootNodeId?: string | null;
  treeSortMode: DirectorySortMode;
  loadMoreMultiProjectSessions: (group: ProjectSessionGroup) => Promise<void> | void;

  // 单项目列表 / 搜索
  sessions: SessionItem[];
  sessionSearchResults: SessionItem[];
  sessionSearchQuery: string;
  sessionSearchLoading: boolean;
  handleSearchQueryChange: (query: string) => void;
  executeSessionSearch: () => void;
  closeSessionSearch: () => void;
  openSessionSearch: () => void;

  // 两个列表组件共用的会话操作
  activeBoundSessionKey?: string | null;
  currentRootId: string | null;
  syncingSessionKeys?: Set<string>;
  handleSelectSessionAndClose: (session: SessionItem) => void;
  handleSyncSession: SessionListProps["onSync"];
  handlePinSession: SessionListProps["onPin"];
  handleRenameSession: SessionListProps["onRename"];
  handleDeleteSession: SessionListProps["onDelete"];
  loadChildSessionsForParent: SessionListProps["onLoadChildren"];
  handleLoadOlderSessions: () => void;
  loadingOlderSessions: boolean;
  hasMoreSessions: boolean;
  toggleSessionSearch: () => void;
};

export function useSessionSidebarView({
  sessionListMode,
  importMenuRef,
  importMenuOpen,
  setImportMenuOpen,
  availableAgents,
  externalImportAgent,
  setExternalImportAgent,
  setExternalSelectedKey,
  enterImportMode,
  externalFilterBound,
  setExternalFilterBound,
  externalSessions,
  externalSelectedKey,
  importingExternalSessionKeys,
  selectedExternalImportKeys,
  loadingExternalSessions,
  externalSessionsError,
  loadingOlderExternalSessions,
  confirmingExternalImport,
  hasMoreExternalSessions,
  exitImportMode,
  toggleExternalImportSelection,
  toggleAllExternalImportSelection,
  handleConfirmExternalImport,
  handleLoadOlderExternalSessions,
  multiProjectSessionsEnabled,
  sessionSearchOpen,
  sessionSearchResultsMode,
  multiProjectSessionGroups,
  multiProjectSessionsLoading,
  currentRootId,
  currentRootNodeId,
  treeSortMode,
  loadMoreMultiProjectSessions,
  sessions,
  sessionSearchResults,
  sessionSearchQuery,
  sessionSearchLoading,
  handleSearchQueryChange,
  executeSessionSearch,
  closeSessionSearch,
  openSessionSearch,
  activeBoundSessionKey,
  syncingSessionKeys,
  handleSelectSessionAndClose,
  handleSyncSession,
  handlePinSession,
  handleRenameSession,
  handleDeleteSession,
  loadChildSessionsForParent,
  handleLoadOlderSessions,
  loadingOlderSessions,
  hasMoreSessions,
  toggleSessionSearch,
}: SessionSidebarViewParams) {
  const { t } = useI18n();

  const sessionImportMenu = (
    <div ref={importMenuRef} style={{ position: "relative" }}>
      <button
        type="button"
        onClick={() => setImportMenuOpen((open) => !open)}
        aria-label={t("externalImport.open")}
        style={{
          border: "none",
          background: "transparent",
          color:
            sessionListMode === "import" && externalImportAgent
              ? "var(--text-secondary)"
              : "#0f766e",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          height: "34px",
          minWidth: "34px",
          borderRadius: "8px",
          cursor: "pointer",
          padding:
            sessionListMode === "import" && externalImportAgent ? 0 : "0 2px",
        }}
      >
        {sessionListMode === "import" && externalImportAgent ? (
          <>
            <AgentIcon
              agentName={externalImportAgent}
              style={{ width: "14px", height: "14px", display: "block" }}
            />
            <ChevronDownSmallIcon />
          </>
        ) : (
          <ImportIcon />
        )}
      </button>
      {importMenuOpen ? (
        <div
          style={{
            position: "absolute",
            top: "calc(100% + 6px)",
            right: 0,
            width: "260px",
            padding: "10px",
            borderRadius: "12px",
            border: "1px solid var(--border-color)",
            background: "var(--menu-bg)",
            boxShadow: "0 12px 30px rgba(15, 23, 42, 0.14)",
            zIndex: 40,
            display: "flex",
            flexDirection: "column",
            gap: "10px",
          }}
        >
          <div
            style={{
              fontSize: "12px",
              fontWeight: 700,
              color: "var(--text-primary)",
            }}
          >
            {t("externalImport.chooseAgent")}
          </div>
          <AgentMenuList
            agents={availableAgents}
            selectedAgent={externalImportAgent}
            maxHeight="180px"
            onSelect={(agentName) => {
              setImportMenuOpen(false);
              setExternalImportAgent(agentName);
              setExternalSelectedKey("");
              void enterImportMode(agentName);
            }}
          />
          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: "8px",
              fontSize: "12px",
              color: "var(--text-primary)",
              cursor: "pointer",
            }}
          >
            <input
              type="checkbox"
              checked={externalFilterBound}
              onChange={(e) => setExternalFilterBound(e.target.checked)}
            />
            <span>{t("externalImport.hideImported")}</span>
          </label>
        </div>
      ) : null}
    </div>
  );

  const sessionSidebar =
    sessionListMode === "import" ? (
      <ExternalSessionList
        sessions={externalSessions}
        selectedKey={externalSelectedKey}
        selectedAgent={externalImportAgent}
        importingKeys={importingExternalSessionKeys}
        selectedImportKeys={selectedExternalImportKeys}
        filterBound={externalFilterBound}
        headerAction={sessionImportMenu}
        loading={loadingExternalSessions}
        error={externalSessionsError}
        loadingOlder={loadingOlderExternalSessions}
        confirmingImport={confirmingExternalImport}
        hasMore={hasMoreExternalSessions}
        onBack={exitImportMode}
        onSelect={(session) =>
          setExternalSelectedKey(
            String(session.key || session.session_key || ""),
          )
        }
        onToggleImport={toggleExternalImportSelection}
        onToggleSelectAllImport={toggleAllExternalImportSelection}
        onConfirmImport={() => {
          void handleConfirmExternalImport();
        }}
        onLoadOlder={() => {
          void handleLoadOlderExternalSessions();
        }}
      />
    ) : multiProjectSessionsEnabled && !sessionSearchOpen && !sessionSearchResultsMode ? (
      <MultiProjectSessionList
        groups={multiProjectSessionGroups}
        selectedKey={activeBoundSessionKey || ""}
        selectedRootId={currentRootId || ""}
        selectedNodeId={currentRootNodeId || ""}
        projectSortMode={treeSortMode}
        headerAction={sessionImportMenu}
        loading={multiProjectSessionsLoading}
        emptyText={t("externalImport.empty")}
        syncingSessionKeys={syncingSessionKeys}
        onSearchToggle={openSessionSearch}
        onSelect={handleSelectSessionAndClose}
        onSync={handleSyncSession}
        onPin={handlePinSession}
        onRename={handleRenameSession}
        onDelete={handleDeleteSession}
        onLoadMoreProject={loadMoreMultiProjectSessions}
        onLoadChildren={loadChildSessionsForParent}
      />
    ) : (
      <SessionList
        sessions={
          sessionSearchOpen && sessionSearchResultsMode
            ? sessionSearchResults
            : sessions
        }
        selectedKey={activeBoundSessionKey || ""}
        headerAction={sessionImportMenu}
        searchOpen={sessionSearchOpen}
        searchResultsMode={sessionSearchResultsMode}
        searchQuery={sessionSearchQuery}
        searchLoading={sessionSearchLoading}
        syncingSessionKeys={syncingSessionKeys}
        emptyText={
          sessionSearchResultsMode
            ? t("externalImport.noSearchMatch")
            : sessionSearchOpen
              ? ""
            : (
              <span>
                {t("externalImport.emptyHintPrefix")}
                <strong style={{ color: "var(--text-primary)", fontWeight: 800 }}>
                  {t("externalImport.emptyHintAction")}
                </strong>
              </span>
            )
        }
        onSearchToggle={toggleSessionSearch}
        onSearchQueryChange={handleSearchQueryChange}
        onSearchSubmit={executeSessionSearch}
        onSearchBlur={closeSessionSearch}
        onSearchBack={closeSessionSearch}
        onSelect={handleSelectSessionAndClose}
        onSync={handleSyncSession}
        onPin={handlePinSession}
        onRename={handleRenameSession}
        onDelete={handleDeleteSession}
        onLoadChildren={
          sessionSearchOpen && sessionSearchResultsMode
            ? undefined
            : loadChildSessionsForParent
        }
        onLoadOlder={
          sessionSearchOpen && sessionSearchResultsMode
            ? undefined
            : handleLoadOlderSessions
        }
        loadingOlder={
          sessionSearchOpen && sessionSearchResultsMode
            ? false
            : loadingOlderSessions
        }
        hasMore={
          sessionSearchOpen && sessionSearchResultsMode
            ? false
            : hasMoreSessions
        }
      />
    );

  return { sessionImportMenu, sessionSidebar };
}
