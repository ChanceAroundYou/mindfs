import type React from "react";
import { useI18n } from "../../i18n/index";
import { RunNowIcon } from "./taskIcons";

/**
 * 「立即执行」控件：任务新建面板与任务详情面板共用同一颗胶囊。
 *
 * 两处的落点一致 —— 都挂在输入框右下角的控件行（`PromptEditor` 的
 * `extraControls`），既不单独占表单里的一行，也不挂在排队留言的气泡页脚上；
 * 桌面与移动端是同一套布局，只是整行按钮本来就贴着输入框右下角。
 *
 * 语义按 `mode` 分：
 * - `toggle`：新建任务时「建完马上派发首条留言」的开关（默认开）。用真实
 *   checkbox 承载状态，视觉上是一颗按钮大小的胶囊，点整颗都能切换。
 * - `action`：详情面板里派发已经排队的首条留言。派发在飞时置灰并写「执行中…」。
 */
export type RunImmediatelyControlProps = {
  mode: "toggle" | "action";
  /** toggle：当前是否开启。 */
  active?: boolean;
  onToggle?: (next: boolean) => void;
  /** action：点一下派发。 */
  onClick?: () => void;
  /** action：派发请求在飞。 */
  running?: boolean;
  disabled?: boolean;
};

export function RunImmediatelyControl({
  mode,
  active = false,
  onToggle,
  onClick,
  running = false,
  disabled = false,
}: RunImmediatelyControlProps) {
  const { t } = useI18n();
  const isToggle = mode === "toggle";
  const on = isToggle ? active : !running;
  const inactive = disabled || (isToggle ? false : running);
  const label = t("task.startImmediately");
  const text = isToggle ? label : running ? t("task.startingNow") : label;
  const hint = isToggle ? t("task.startImmediatelyHint") : label;
  const style = pillStyle(on, inactive);
  const content = (
    <>
      <RunNowIcon />
      <span>{text}</span>
    </>
  );

  if (isToggle) {
    // 真实 checkbox：读屏与键盘拿到的仍是「开关」，视觉上由整颗胶囊表现。
    return (
      <label data-testid="run-immediately-toggle" title={hint} style={style}>
        <input
          type="checkbox"
          checked={active}
          disabled={disabled}
          aria-label={label}
          onChange={(event) => onToggle?.(event.target.checked)}
          style={hiddenCheckboxStyle}
        />
        {content}
      </label>
    );
  }

  return (
    <button
      type="button"
      data-testid="run-immediately-action"
      title={hint}
      aria-label={label}
      disabled={inactive}
      onMouseDown={(event) => event.preventDefault()}
      onClick={onClick}
      style={style}
    >
      {content}
    </button>
  );
}

/** 与输入框右下角那排 28px 图标键同高的一颗胶囊；开启/可点时用强调色。 */
function pillStyle(on: boolean, disabled: boolean): React.CSSProperties {
  return {
    display: "inline-flex",
    alignItems: "center",
    gap: "4px",
    height: "22px",
    padding: "0 8px",
    borderRadius: "999px",
    border: `1px solid ${on ? "var(--accent-color)" : "var(--border-color)"}`,
    background: on ? "rgba(37, 99, 235, 0.10)" : "transparent",
    color: on ? "var(--accent-color)" : "var(--text-secondary)",
    fontSize: "11px",
    fontWeight: 600,
    lineHeight: 1,
    whiteSpace: "nowrap",
    cursor: disabled ? "default" : "pointer",
    opacity: disabled ? 0.5 : 1,
    flexShrink: 0,
  };
}

/** 视觉隐藏、但仍可聚焦与朗读：整颗胶囊就是它的点击区域。 */
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
