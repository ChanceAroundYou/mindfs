import React from "react";
import { useNodeRegistry } from "../hooks/useNodeRegistry";
import { addNode, removeNode, updateNode, PALETTE, getActiveNode } from "../services/nodeRegistry";

function normalizeURL(v: string): string {
  const t = String(v || "").trim();
  if (!t) return "";
  const withScheme = /^[a-z]+:\/\//i.test(t) ? t : `https://${t}`;
  try { const u = new URL(withScheme); if (u.protocol !== "http:" && u.protocol !== "https:") return ""; u.hash=""; return u.toString().replace(/\/+$/,""); } catch { return ""; }
}

export function NodeSwitcher({ onChanged }: { onChanged?: () => void }) {
  const { nodes, activeId, aggregated, setActive, setAggregated } = useNodeRegistry();
  const [name, setName] = React.useState("");
  const [url, setUrl] = React.useState("");
  const [err, setErr] = React.useState("");
  const [editing, setEditing] = React.useState<string | null>(null);
  const [editName, setEditName] = React.useState("");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const nUrl = normalizeURL(url);
    const nName = name.trim() || (()=>{ try{ return new URL(nUrl).hostname } catch { return nUrl }})();
    if (!nUrl) { setErr("请输入合法的 http(s) 地址"); return; }
    if (nodes.some(n=>n.url===nUrl)) { setErr("该节点地址已存在"); return; }
    try { addNode({ name: nName, url: nUrl }); setName(""); setUrl(""); setErr(""); window.dispatchEvent(new CustomEvent("mindfs:nodes-changed")); onChanged?.(); } catch (ex:any){ setErr(String(ex?.message||ex)); }
  };

  return (
    <div style={{ display:"flex", flexDirection:"column", gap:"8px", padding:"8px 10px", borderBottom:"1px solid var(--border-color)" }}>
      <div style={{ display:"flex", gap:"6px", flexWrap:"wrap", alignItems:"center" }}>
        {nodes.length===0 ? <span style={{ fontSize:"12px", color:"var(--text-secondary)" }}>暂无节点，请添加</span> : nodes.map(n=>{
          const active = n.id===activeId;
          return (
            <span key={n.id} style={{ display:"inline-flex", alignItems:"center", gap:"6px", border: active?"1px solid var(--accent-color)":"1px solid var(--border-color)", background: active?"var(--selection-bg)":"transparent", borderRadius:"999px", padding:"4px 8px", fontSize:"12px" }}>
              <span style={{ width:10, height:10, borderRadius:"50%", background:n.color, flexShrink:0, display:"inline-block" }} />
              <button type="button" onClick={()=>{ setActive(n.id); onChanged?.(); }} style={{ border:"none", background:"transparent", color: active?"var(--accent-color)":"var(--text-primary)", cursor:"pointer", fontWeight: active?700:500, padding:0 }}>{n.name}</button>
              <span style={{ color:"var(--text-secondary)", maxWidth:160, overflow:"hidden", textOverflow:"ellipsis", whiteSpace:"nowrap" }}>{n.url}</span>
              {editing===n.id ? (
                <span style={{ display:"inline-flex", gap:4 }}>
                  <input value={editName} onChange={e=>setEditName(e.target.value)} style={{ width:90, fontSize:12 }} />
                  <button type="button" onClick={()=>{ const v=editName.trim(); if(v) updateNode(n.id,{name:v}); setEditing(null); window.dispatchEvent(new CustomEvent("mindfs:nodes-changed")); onChanged?.(); }}>✓</button>
                  <button type="button" onClick={()=>setEditing(null)}>✕</button>
                </span>
              ) : <button type="button" onClick={()=>{ setEditing(n.id); setEditName(n.name); }} title="重命名" style={{ border:"none", background:"transparent", cursor:"pointer" }}>✎</button>}
              <button type="button" title="删除节点" onClick={()=>{ if(!confirm(`删除节点 ${n.name} ?`)) return; removeNode(n.id); window.dispatchEvent(new CustomEvent("mindfs:nodes-changed")); onChanged?.(); }} style={{ border:"none", background:"transparent", color:"#dc2626", cursor:"pointer" }}>×</button>
            </span>
          );
        })}
      </div>
      <div style={{ display:"flex", gap:8, alignItems:"center", fontSize:12 }}>
        <label style={{ display:"inline-flex", gap:6, alignItems:"center", color:"var(--text-secondary)" }}><input type="checkbox" checked={aggregated} onChange={e=>{ setAggregated(e.target.checked); onChanged?.(); }} /> 聚合视图</label>
        {!aggregated && nodes.length>1 ? <span style={{ color:"var(--text-secondary)" }}>关闭聚合时仅显示当前节点项目</span> : null}
        <span style={{ marginLeft:"auto", color:"var(--text-secondary)", fontSize:11 }}>颜色循环：{PALETTE.join(" ")}</span>
      </div>
      <form onSubmit={submit} style={{ display:"flex", gap:6, alignItems:"center", flexWrap:"wrap" }}>
        <input value={name} onChange={e=>setName(e.target.value)} placeholder="节点名（可选）" style={{ flex:"0 1 140px", fontSize:12, padding:"6px 8px", borderRadius:8, border:"1px solid var(--border-color)" }} />
        <input value={url} onChange={e=>setUrl(e.target.value)} placeholder="https://home.xiaokubao.space/mindfs" style={{ flex:"1 1 220px", fontSize:12, padding:"6px 8px", borderRadius:8, border:"1px solid var(--border-color)" }} />
        <button type="submit" style={{ fontSize:12, padding:"6px 10px", borderRadius:8, border:"1px solid var(--accent-color)", background:"var(--accent-color)", color:"#fff", cursor:"pointer" }}>添加节点</button>
      </form>
      {err ? <div style={{ fontSize:12, color:"#dc2626" }}>{err}</div> : null}
      {getActiveNode() ? <div style={{ fontSize:11, color:"var(--text-secondary)" }}>当前节点：<span style={{ color: getActiveNode()!.color, fontWeight:700 }}>{getActiveNode()!.name}</span> {getActiveNode()!.url}</div> : null}
    </div>
  );
}
