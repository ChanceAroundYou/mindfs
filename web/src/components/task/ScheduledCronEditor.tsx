import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { useI18n, type MessageKey } from "../../i18n/index";

// cron 字段的解析/校验、CronEditor 以及行内图标按钮，
// 从 ScheduledAgentTaskDialog 抽出（fieldStyle 为宿主与编辑器共享）。

const CRON_LABEL_KEYS: MessageKey[] = [
  "scheduled.cronMinute",
  "scheduled.cronHour",
  "scheduled.cronDay",
  "scheduled.cronMonth",
  "scheduled.cronWeek",
];


function splitCron(value: string): string[] {
  const raw = String(value || "").trim();
  if (!raw) {
    return ["", "", "", "", ""];
  }
  const parts = raw.split(/\s+/).filter(Boolean);
  const padded = [...parts.slice(0, 5)];
  while (padded.length < 5) {
    padded.push("*");
  }
  return padded;
}

function joinCron(parts: string[]): string {
  return parts
    .map((part) => part.trim())
    .slice(0, 5)
    .join(" ");
}

function isCronSegmentValid(value: string, index: number): boolean {
  const raw = value.trim();
  if (!raw) return false;
  if (raw === "*" || raw === "?") return true;
  const names =
    index === 3
      ? [
          "jan",
          "feb",
          "mar",
          "apr",
          "may",
          "jun",
          "jul",
          "aug",
          "sep",
          "oct",
          "nov",
          "dec",
        ]
      : index === 4
        ? ["sun", "mon", "tue", "wed", "thu", "fri", "sat"]
        : [];
  const min = [0, 0, 1, 1, 0][index];
  const max = [59, 23, 31, 12, 7][index];
  const atomValid = (atom: string): boolean => {
    const normalized = atom.toLowerCase();
    if (names.includes(normalized)) return true;
    if (!/^\d+$/.test(atom)) return false;
    const number = Number(atom);
    return number >= min && number <= max;
  };
  return raw.split(",").every((item) => {
    if (!item) return false;
    const [rangePart, stepPart] = item.split("/");
    if (
      stepPart !== undefined &&
      (!/^\d+$/.test(stepPart) || Number(stepPart) <= 0)
    ) {
      return false;
    }
    if (rangePart === "*" || rangePart === "?") return true;
    const range = rangePart.split("-");
    if (range.length === 1) return atomValid(range[0]);
    if (range.length !== 2 || !atomValid(range[0]) || !atomValid(range[1]))
      return false;
    if (/^\d+$/.test(range[0]) && /^\d+$/.test(range[1])) {
      return Number(range[0]) <= Number(range[1]);
    }
    return true;
  });
}

export const fieldStyle: CSSProperties = {
  width: "100%",
  border: "1px solid var(--border-color)",
  borderRadius: 8,
  background: "var(--content-bg, #fff)",
  color: "var(--text-primary)",
  fontSize: 13,
  padding: "8px 10px",
  outline: "none",
  boxSizing: "border-box",
};

export const taskIconButtonStyle: CSSProperties = {
  border: "none",
  background: "transparent",
  color: "var(--text-primary)",
  borderRadius: 6,
  width: 26,
  height: 26,
  padding: 0,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  cursor: "pointer",
  flexShrink: 0,
};

export function StopIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="13"
      height="13"
      viewBox="0 0 1024 1024"
      aria-hidden="true"
    >
      <path d="M0 0h1024v1024H0z" fill="none" />
      <path
        fill="currentColor"
        d="M512 64C264.6 64 64 264.6 64 512s200.6 448 448 448s448-200.6 448-448S759.4 64 512 64m0 820c-205.4 0-372-166.6-372-372c0-89 31.3-170.8 83.5-234.8l523.3 523.3C682.8 852.7 601 884 512 884m288.5-137.2L277.2 223.5C341.2 171.3 423 140 512 140c205.4 0 372 166.6 372 372c0 89-31.3 170.8-83.5 234.8"
      />
    </svg>
  );
}

export function RunNowIcon() {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width="13"
      height="13"
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      <path d="M0 0h24v24H0z" fill="none" />
      <path
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        d="M20.409 9.353a2.998 2.998 0 0 1 0 5.294L7.597 21.614C5.534 22.737 3 21.277 3 18.968V5.033c0-2.31 2.534-3.769 4.597-2.648z"
      />
    </svg>
  );
}

export function DeleteIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6l-1 14H6L5 6" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
      <path d="M9 6V4h6v2" />
    </svg>
  );
}

export function CronEditor({
  label,
  value,
  onChange,
  placeholder,
  headerRight,
  allowEmpty = false,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  headerRight?: ReactNode;
  allowEmpty?: boolean;
}) {
  const { t } = useI18n();
  const [parts, setParts] = useState(() => splitCron(value));
  const [touched, setTouched] = useState<boolean[]>(() => [
    false,
    false,
    false,
    false,
    false,
  ]);
  const lastEmittedRef = useRef<string | null>(null);
  useEffect(() => {
    if (lastEmittedRef.current === value) {
      return;
    }
    setParts(splitCron(value));
    setTouched([false, false, false, false, false]);
  }, [value]);
  const placeholderParts = splitCron(placeholder || "* * * * *");
  const allEmpty = parts.every((part) => !part.trim());
  const shouldValidate = touched.some(Boolean) && !(allowEmpty && allEmpty);
  const invalid =
    shouldValidate &&
    parts.some((part, index) => !isCronSegmentValid(part || "", index));
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 8,
        }}
      >
        <div
          style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}
        >
          <div
            style={{
              fontSize: 12,
              color: "var(--text-secondary)",
              flexShrink: 0,
            }}
          >
            {label}
          </div>
          {invalid ? (
            <div
              style={{
                fontSize: 12,
                color: "#dc2626",
                whiteSpace: "nowrap",
                overflow: "hidden",
                textOverflow: "ellipsis",
              }}
            >
              {t("scheduled.cronInvalid")}
            </div>
          ) : null}
        </div>
        {headerRight ? (
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            {headerRight}
          </div>
        ) : null}
      </div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "repeat(5, minmax(0, 1fr))",
          gap: 8,
        }}
      >
        {CRON_LABEL_KEYS.map((item, index) => (
          <div
            key={item}
            style={{
              position: "relative",
              minWidth: 0,
            }}
          >
            <span
              style={{
                position: "absolute",
                top: "50%",
                left: 9,
                transform: "translateY(-50%)",
                color: "var(--text-secondary)",
                opacity: 0.65,
                fontSize: 11,
                lineHeight: 1,
                pointerEvents: "none",
              }}
            >
              {t(item)}
            </span>
            <input
              className="scheduled-agent-task-input"
              value={parts[index] || ""}
              placeholder={placeholderParts[index] || "*"}
              onChange={(event) => {
                const next = [...parts];
                next[index] = event.target.value;
                setParts(next);
                const nextValue = joinCron(next);
                lastEmittedRef.current = nextValue;
                onChange(nextValue);
              }}
              onBlur={() => {
                setTouched([true, true, true, true, true]);
              }}
              style={{
                ...fieldStyle,
                borderColor:
                  shouldValidate &&
                  !isCronSegmentValid(parts[index] || "", index)
                    ? "#ef4444"
                    : "var(--border-color)",
                padding: "7px 8px 7px 26px",
                textAlign: "center",
              }}
            />
          </div>
        ))}
      </div>
    </div>
  );
}
