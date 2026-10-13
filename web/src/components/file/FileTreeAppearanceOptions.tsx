import React from "react";
export function FileTreeAppearanceOptions({ open, options, value, onChange, closeMenus, t }: any) {
 if (!open) return null; return <>{options.map((option: any) => { const active=option.value===value; return <button key={option.value} type="button" onClick={()=>{onChange(option.value);closeMenus();}} style={{width:"100%",border:"none",background:active?"var(--selection-bg)":"transparent",color:active?"var(--accent-color)":"var(--text-primary)",borderRadius:"8px",padding:"8px 10px",display:"flex",alignItems:"center",justifyContent:"space-between",textAlign:"left",cursor:"pointer",fontSize:"12px"}}><span>{t(option.labelKey)}</span><span style={{fontSize:"11px",opacity:active?1:0}}>✓</span></button>;})}</>;
}
