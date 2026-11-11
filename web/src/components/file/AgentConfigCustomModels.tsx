import React from "react";
import { useI18n } from "../../i18n/index";
import {
  agentConfigFieldStyle,
  agentConfigHintStyle,
  agentConfigIconButtonStyle,
  agentConfigInputStyle,
  agentConfigLabelStyle,
  agentConfigSecondaryButtonStyle,
} from "./agentConfigStyles";

// 每个 agent 的自定义模型目录：只负责输入与标签，保存由调用方的 hook 处理。
export function AgentConfigCustomModels({ models, busy, onAdd, onRemove }: {
  models: string[];
  busy: boolean;
  onAdd: (model: string) => void;
  onRemove: (model: string) => void;
}) {
  const { t } = useI18n();
  const [input, setInput] = React.useState("");
  const disabled = busy || !input.trim();
  return (
    <div style={agentConfigFieldStyle}>
      <label style={agentConfigLabelStyle}>{t("agentConfig.customModels")}</label>
      <div style={{ display: "flex", gap: "6px", alignItems: "center" }}>
        <input
          value={input}
          onChange={(event) => setInput(event.target.value)}
          placeholder={t("agentConfig.customModelPlaceholder")}
          style={agentConfigInputStyle}
        />
        <button
          type="button"
          disabled={disabled}
          onClick={() => {
            onAdd(input.trim());
            setInput("");
          }}
          style={agentConfigSecondaryButtonStyle(disabled)}
        >
          {t("agentConfig.addModel")}
        </button>
      </div>
      {models.length > 0 ? (
        <div style={{ display: "flex", flexWrap: "wrap", gap: "6px", marginTop: "6px" }}>
          {models.map((model) => (
            <span
              key={model}
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "4px",
                border: "1px solid var(--border-color)",
                borderRadius: "999px",
                padding: "2px 8px",
                fontSize: "11px",
                color: "var(--text-primary)",
              }}
            >
              {model}
              <button
                type="button"
                aria-label={t("agentConfig.removeModel", { name: model })}
                title={t("agentConfig.removeModel", { name: model })}
                disabled={busy}
                onClick={() => onRemove(model)}
                style={{ ...agentConfigIconButtonStyle(busy), width: "18px", height: "18px" }}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      ) : (
        <div style={{ ...agentConfigHintStyle, marginTop: "4px" }}>{t("agentConfig.customModelsEmpty")}</div>
      )}
      <div style={{ ...agentConfigHintStyle, marginTop: "4px" }}>{t("agentConfig.customModelsHint")}</div>
    </div>
  );
}