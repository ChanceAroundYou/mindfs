import React from "react";
import { useI18n } from "../i18n";
import { appPath } from "../services/base";
import { protectedJSON } from "../services/api";
import { useRefreshSpin } from "../hooks/useRefreshSpin";
import {
  addNode,
  getNodes,
  LOCAL_NODE_ID,
  PALETTE,
  removeNode,
  updateNode,
  type NodeConnection,
} from "../services/nodeRegistry";

type ProbeStatus = "checking" | "online" | "offline";

// 挂载即"打开"（与它替换掉的 add/remove/edit 三个 popover 一致），外部点击由面板自己收。
type NodeManagerPanelProps = {
  onClose: () => void;
  onRefreshAll?: () => Promise<void>;
};

const PANEL_STYLE: React.CSSProperties = {
  position: "absolute",
  top: "calc(100% + 6px)",
  left: "8px",
  right: "8px",
  zIndex: 35,
  padding: "12px",
  borderRadius: "12px",
  border: "1px solid var(--border-color)",
  background: "var(--menu-bg)",
  boxShadow: "0 12px 30px rgba(15,23,42,0.14)",
  display: "flex",
  flexDirection: "column",
  gap: 8,
  maxHeight: 380,
  overflow: "auto",
};

const INPUT_STYLE: React.CSSProperties = {
  width: "100%",
  borderRadius: 8,
  border: "1px solid var(--border-color)",
  padding: "8px 10px",
  fontSize: 12,
  boxSizing: "border-box",
};

const SMALL_BUTTON_STYLE: React.CSSProperties = {
  border: "1px solid var(--border-color)",
  background: "transparent",
  borderRadius: 8,
  padding: "6px 10px",
  fontSize: 12,
  cursor: "pointer",
  whiteSpace: "nowrap",
};

const PRIMARY_BUTTON_STYLE: React.CSSProperties = {
  border: "none",
  background: "var(--accent-color)",
  color: "#fff",
  borderRadius: 8,
  padding: "6px 10px",
  fontSize: 12,
  cursor: "pointer",
};

// 既存节点的探测：走 app 真实请求链路（含 E2EE），用 appPath 按 nodeId 解析地址
// —— 这样原生壳下 local 的 url 为空时也能正确回落到当前连接的服务器。
// 刻意不套 withNodeRetry：探测要的是快速反馈，不是重试。
async function probeNode(nodeId: string, timeoutMs = 4000): Promise<ProbeStatus> {
  let timer = 0;
  try {
    await Promise.race([
      protectedJSON<unknown>(appPath("/api/agents", nodeId)),
      new Promise((_resolve, reject) => {
        timer = window.setTimeout(() => reject(new Error("node_probe_timeout")), timeoutMs);
      }),
    ]);
    return "online";
  } catch {
    return "offline";
  } finally {
    if (timer) window.clearTimeout(timer);
  }
}

// 待加入节点的地址校验+归一：候选地址还不是节点，appPath 解析不了，只能自己拼。
// 与 addNode 内部 normalizeExplicitNodeBase 的"去尾斜杠"保持一致。
function normalizeInputUrl(raw: string): string {
  const trimmed = String(raw || "").trim();
  const withScheme = /^[a-z]+:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
  const url = new URL(withScheme);
  url.hash = "";
  return url.toString().replace(/\/+$/, "");
}

function originOf(url: string): string {
  try {
    return new URL(url).origin.toLowerCase();
  } catch {
    return "";
  }
}

export function NodeManagerPanel({ onClose, onRefreshAll }: NodeManagerPanelProps): React.ReactElement {
  const { t } = useI18n();
  const rootRef = React.useRef<HTMLDivElement | null>(null);
  // 不用 state 镜像节点列表（镜像会变陈旧：改完名面板不刷新，要重载才显示）。
  // 沿用被替换掉的三个 popover 的既有约定——渲染时直接读 getNodes()，
  // 节点变化只负责强制一次重渲染。
  const [, bumpNodesVersion] = React.useReducer((n: number) => n + 1, 0);
  const [probeStatus, setProbeStatus] = React.useState<Record<string, ProbeStatus>>({});
  const [editingId, setEditingId] = React.useState<string | null>(null);
  const [draftName, setDraftName] = React.useState("");
  const [draftUrl, setDraftUrl] = React.useState("");
  const [draftColor, setDraftColor] = React.useState("");
  const [editBusy, setEditBusy] = React.useState(false);
  const [addOpen, setAddOpen] = React.useState(false);
  const [addName, setAddName] = React.useState("");
  const [addUrl, setAddUrl] = React.useState("");
  const [addBusy, setAddBusy] = React.useState(false);
  const [addError, setAddError] = React.useState("");
  const [error, setError] = React.useState("");

  const runProbe = React.useCallback(async () => {
    const list = getNodes();
    setProbeStatus(Object.fromEntries(list.map((n) => [n.id, "checking" as ProbeStatus])));
    // 并发探测、逐个回填，不等全部完成
    await Promise.all(
      list.map(async (n) => {
        const status = await probeNode(n.id);
        setProbeStatus((prev) => ({ ...prev, [n.id]: status }));
      }),
    );
  }, []);

  const refreshSpin = useRefreshSpin(async () => {
    await onRefreshAll?.();
    await runProbe();
  });

  // 订阅节点变化（自己增删改、或别的设备经 WS 改了节点列表都会触发）
  React.useEffect(() => {
    const handler = () => bumpNodesVersion();
    window.addEventListener("mindfs:nodes-changed", handler);
    return () => window.removeEventListener("mindfs:nodes-changed", handler);
  }, []);

  React.useEffect(() => {
    void runProbe();
  }, [runProbe]);

  // 外部点击关闭（沿用原 popover 的做法）
  React.useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) onClose();
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [onClose]);

  const startEdit = (node: NodeConnection) => {
    setEditingId(node.id);
    setDraftName(node.name);
    setDraftUrl(node.url);
    setDraftColor(node.color);
    setError("");
  };

  // 候选地址（新节点、或节点改地址）还没进注册表，appPath 解析不了，只能自己拼。
  // 5s 内拿不到 200 就算不可用——添加和改地址共用这一套校验。
  const probeCandidateUrl = React.useCallback(
    async (normalizedUrl: string): Promise<{ ok: true } | { ok: false; message: string }> => {
      const controller = new AbortController();
      const timer = window.setTimeout(() => controller.abort(), 5000);
      try {
        const res = await fetch(`${normalizedUrl}/api/agents`, { signal: controller.signal });
        if (!res.ok) {
          return { ok: false, message: t("nodeManager.probeFailed", { status: String(res.status) }) };
        }
        return { ok: true };
      } catch (e: unknown) {
        const err = e as Error;
        // abort 是超时；其它基本都是 fetch 的网络层错误（TypeError "Failed to fetch"），
        // 原文不是给人看的，换成可读提示。
        return {
          ok: false,
          message: err?.name === "AbortError" ? t("nodeManager.probeTimeout") : t("nodeManager.probeUnreachable"),
        };
      } finally {
        window.clearTimeout(timer);
      }
    },
    [t],
  );

  const commitEdit = async (node: NodeConnection) => {
    const isLocal = node.id === LOCAL_NODE_ID;
    const name = draftName.trim();
    if (!name) return;
    let nextUrl = "";
    if (!isLocal) {
      try {
        nextUrl = normalizeInputUrl(draftUrl);
      } catch {
        setError(t("nodeManager.urlInvalid"));
        return;
      }
      // 改了地址就必须先连得上：连不上不保存，只能取消回退原值
      if (nextUrl !== node.url) {
        // 与别的节点同 origin 会被读取时的按 origin 去重悄悄吃掉一个，先拦住
        const nextOrigin = originOf(nextUrl);
        const clash = getNodes().some((n) => n.id !== node.id && nextOrigin !== "" && originOf(n.url) === nextOrigin);
        if (clash) {
          setError(t("nodeManager.urlExists"));
          return;
        }
        setEditBusy(true);
        const probe = await probeCandidateUrl(nextUrl);
        setEditBusy(false);
        if (!probe.ok) {
          setError(probe.message);
          return;
        }
      }
    }
    try {
      setError("");
      // local 的 url 每次读取都会被 deviceLocalNodeURL() 覆盖，提交了也是白提交
      await updateNode(node.id, isLocal ? { name, color: draftColor } : { name, color: draftColor, url: nextUrl });
      setEditingId(null);
    } catch (e: unknown) {
      setError(String((e as Error)?.message || e));
    }
  };

  const handleDelete = async (node: NodeConnection) => {
    if (!window.confirm(t("nodeManager.confirmDelete", { name: node.name }))) return;
    try {
      setError("");
      await removeNode(node.id);
    } catch (e: unknown) {
      setError(String((e as Error)?.message || e));
    }
  };

  const handleAdd = async () => {
    const name = String(addName || "").trim();
    let normalized = "";
    try {
      normalized = normalizeInputUrl(addUrl);
    } catch {
      setAddError(t("nodeManager.urlInvalid"));
      return;
    }
    if (!name) return;
    setAddBusy(true);
    setAddError("");
    try {
      const probe = await probeCandidateUrl(normalized);
      if (!probe.ok) {
        setAddError(probe.message);
        return;
      }
      const node = await addNode({ name, url: normalized });
      setProbeStatus((prev) => ({ ...prev, [node.id]: "online" }));
      setAddOpen(false);
      setAddName("");
      setAddUrl("");
    } catch (e: unknown) {
      const message = String((e as Error)?.message || e);
      setAddError(message === "node_url_exists" ? t("nodeManager.urlExists") : message);
    } finally {
      setAddBusy(false);
    }
  };

  const statusLabel = (id: string): { text: string; color: string } => {
    const status = probeStatus[id] || "checking";
    if (status === "online") return { text: t("nodeManager.online"), color: "#16a34a" };
    if (status === "offline") return { text: t("nodeManager.offline"), color: "#dc2626" };
    return { text: t("nodeManager.checking"), color: "var(--text-secondary)" };
  };

  const nodes = getNodes();

  return (
    <div ref={rootRef} style={PANEL_STYLE}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div style={{ fontWeight: 700, fontSize: 12, flex: 1 }}>{t("nodeManager.title")}</div>
        <button
          type="button"
          aria-label={t("common.refresh")}
          title={t("common.refresh")}
          disabled={refreshSpin.refreshing}
          onClick={() => void refreshSpin.handleClick()}
          onMouseDown={() => refreshSpin.setPressed(true)}
          onMouseUp={() => refreshSpin.setPressed(false)}
          onMouseLeave={() => refreshSpin.setPressed(false)}
          style={{
            ...SMALL_BUTTON_STYLE,
            display: "inline-flex",
            alignItems: "center",
            gap: 6,
            opacity: refreshSpin.refreshing ? 0.6 : 1,
            cursor: refreshSpin.refreshing ? "not-allowed" : "pointer",
          }}
        >
          <svg
            width="13"
            height="13"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.2"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={refreshSpin.refreshing ? { animation: "mindfs-update-spin 0.8s linear infinite" } : undefined}
          >
            <path d="M21 12a9 9 0 1 1-2.64-6.36" />
            <path d="M21 3v6h-6" />
          </svg>
          <span>{t("common.refresh")}</span>
        </button>
      </div>

      {nodes.map((node) => {
        const isLocal = node.id === LOCAL_NODE_ID;
        const status = statusLabel(node.id);
        const editing = editingId === node.id;
        return (
          <div
            key={node.id}
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 8,
              border: "1px solid var(--border-color)",
              borderRadius: 8,
              padding: "8px 10px",
            }}
          >
            {/* 编辑态：名称与路径的纯文本在这一行原地换成输入框，不再另起一组重复输入框 */}
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span
                  style={{ width: 8, height: 8, borderRadius: "50%", background: node.color, display: "inline-block", flex: "0 0 auto" }}
                />
                {editing ? (
                  <input
                    value={draftName}
                    onChange={(e) => setDraftName(e.target.value)}
                    placeholder={t("login.nodeNamePlaceholder")}
                    autoFocus
                    style={{ ...INPUT_STYLE, flex: 1, minWidth: 0, padding: "4px 8px" }}
                  />
                ) : (
                  <span style={{ flex: 1, fontSize: 12, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {node.name}
                  </span>
                )}
                <span style={{ fontSize: 11, color: status.color, whiteSpace: "nowrap" }}>{status.text}</span>
              </div>

              {editing ? (
                <input
                  value={draftUrl}
                  onChange={(e) => setDraftUrl(e.target.value)}
                  placeholder={isLocal ? t("nodeManager.thisDevice") : t("login.nodeUrlPlaceholder")}
                  disabled={isLocal}
                  style={{
                    ...INPUT_STYLE,
                    fontSize: 11,
                    padding: "4px 8px",
                    opacity: isLocal ? 0.6 : 1,
                    cursor: isLocal ? "not-allowed" : "text",
                  }}
                />
              ) : (
                <div style={{ fontSize: 11, color: "var(--text-secondary)", overflowWrap: "anywhere" }}>
                  {node.url || t("nodeManager.thisDevice")}
                </div>
              )}
            </div>

            {editing ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <span style={{ fontSize: 11, color: "var(--text-secondary)" }}>{t("nodeManager.color")}</span>
                  {PALETTE.map((color) => (
                    <button
                      key={color}
                      type="button"
                      aria-label={color}
                      onClick={() => setDraftColor(color)}
                      style={{
                        width: 18,
                        height: 18,
                        borderRadius: "50%",
                        background: color,
                        border: draftColor.toLowerCase() === color.toLowerCase() ? "2px solid var(--text-primary)" : "2px solid transparent",
                        cursor: "pointer",
                        padding: 0,
                      }}
                    />
                  ))}
                </div>
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
                  <button type="button" onClick={() => setEditingId(null)} style={SMALL_BUTTON_STYLE}>
                    {t("common.cancel")}
                  </button>
                  <button
                    type="button"
                    disabled={!draftName.trim() || editBusy}
                    onClick={() => void commitEdit(node)}
                    style={{
                      ...PRIMARY_BUTTON_STYLE,
                      opacity: draftName.trim() && !editBusy ? 1 : 0.6,
                      cursor: editBusy ? "not-allowed" : "pointer",
                    }}
                  >
                    {editBusy ? t("nodeManager.checking") : t("common.save")}
                  </button>
                </div>
              </div>
            ) : (
              <div style={{ display: "flex", gap: 8 }}>
                <button type="button" onClick={() => startEdit(node)} style={SMALL_BUTTON_STYLE}>
                  {t("common.edit")}
                </button>
                {!isLocal ? (
                  <button
                    type="button"
                    onClick={() => void handleDelete(node)}
                    style={{ ...SMALL_BUTTON_STYLE, borderColor: "#dc2626", color: "#dc2626" }}
                  >
                    {t("common.delete")}
                  </button>
                ) : null}
              </div>
            )}
          </div>
        );
      })}

      {addOpen ? (
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <input
            value={addName}
            onChange={(e) => setAddName(e.target.value)}
            placeholder={t("login.nodeNamePlaceholder")}
            style={INPUT_STYLE}
          />
          <input
            value={addUrl}
            onChange={(e) => setAddUrl(e.target.value)}
            placeholder={t("login.nodeUrlPlaceholder")}
            style={INPUT_STYLE}
          />
          {addError ? <div style={{ color: "#dc2626", fontSize: 12 }}>{addError}</div> : null}
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <button type="button" onClick={() => { setAddOpen(false); setAddError(""); }} style={SMALL_BUTTON_STYLE}>
              {t("common.cancel")}
            </button>
            <button
              type="button"
              disabled={addBusy || !addName.trim() || !addUrl.trim()}
              onClick={() => void handleAdd()}
              style={{
                ...PRIMARY_BUTTON_STYLE,
                opacity: addBusy || !addName.trim() || !addUrl.trim() ? 0.6 : 1,
                cursor: addBusy ? "not-allowed" : "pointer",
              }}
            >
              {addBusy ? t("nodeManager.checking") : t("common.save")}
            </button>
          </div>
        </div>
      ) : (
        <button type="button" onClick={() => { setAddOpen(true); setAddError(""); }} style={SMALL_BUTTON_STYLE}>
          {t("nodeManager.addNode")}
        </button>
      )}

      {error ? <div style={{ color: "#dc2626", fontSize: 12 }}>{error}</div> : null}
    </div>
  );
}
