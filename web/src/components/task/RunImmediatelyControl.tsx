import type React from "react";
import { useI18n } from "../../i18n/index";
import { composerSendButtonStyle } from "../action/composerStyles";
import { RunNowIcon } from "./taskIcons";

/**
 * 「立即执行」开关：新建任务面板的输入框右下角，与「+ 附件」「发送」同一排。
 *
 * 只有图标，没有文字：背景与发送按钮同源（`composerSendButtonStyle` 的 panel 形态，
 * 28×28 圆角正方形）—— 打开时用主题色实底，关闭时用发送按钮的禁用灰底，
 * 用户不用读文案也能看出这是同一组控件里的开关。
 * 文案留在 `title` / `aria-label` 上，屏幕阅读器与悬浮提示仍能读到。
 */
export type RunImmediatelyControlProps = {
  active: boolean;
  onToggle: (next: boolean) => void;
  disabled?: boolean;
};

export function RunImmediatelyControl({ active, onToggle, disabled = false }: RunImmediatelyControlProps) {
  const { t } = useI18n();
  const label = t("task.startImmediately");
  const hint = t("task.startImmediatelyHint");
  return (
    <label
      data-testid="run-immediately-toggle"
      title={hint}
      aria-label={label}
      style={{
        ...composerSendButtonStyle({ enabled: active && !disabled, variant: "panel" }),
        padding: 0,
        cursor: disabled ? "not-allowed" : "pointer",
      }}
    >
      {/* 视觉隐藏但可聚焦：保留原生 checkbox 的键盘与读屏语义。 */}
      <input
        type="checkbox"
        checked={active}
        disabled={disabled}
        aria-label={label}
        onChange={(event) => onToggle(event.target.checked)}
        style={hiddenCheckboxStyle}
      />
      <RunNowIcon />
    </label>
  );
}

const hiddenCheckboxStyle: React.CSSProperties = {
  position: "absolute",
  width: "1px",
  height: "1px",
  margin: "-1px",
  padding: 0,
  border: 0,
  overflow: "hidden",
  clip: "rect(0 0 0 0)",
  whiteSpace: "nowrap",
};
