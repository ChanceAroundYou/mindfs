import React from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../../i18n/index";
import {
  FileOperationItems,
  MoreMenuButton,
  moreMenuPopoverStyle,
  MoveFilePopover,
} from "./FileOperations";
import { useAnchoredMenuPosition } from "../../hooks/useAnchoredMenuPosition";

/**
 * 文件浏览器列表里「每一条」的操作菜单（删除 / 重命名 / 移动）。
 *
 * 复用既有的控件与面板：三点按钮是 MoreMenuButton，菜单条目是 FileOperationItems
 * （删除走应用内 confirmDialog、重命名走 promptDialog），移动面板是 MoveFilePopover。
 *
 * 弹层一律 portal 到 body + position: fixed：列表容器是 overflow:auto，行上还有
 * translateZ(0)（会给 fixed 造包含块），内联定位的菜单必被裁掉。
 */
export type FileEntryMenuProps = {
  /** 项目 id（后端的 root）。 */
  root: string;
  /** 项目绝对路径：移动面板浏览目标目录时要用。 */
  rootPath?: string;
  /** 目标条目相对项目根的路径（= FileEntry.path）。 */
  path: string;
  /** 操作成功后由调用方刷新列表。 */
  onComplete: () => void | Promise<void>;
};

export function FileEntryMenu({ root, rootPath, path, onComplete }: FileEntryMenuProps) {
  const { t } = useI18n();
  const [panel, setPanel] = React.useState<"menu" | "move" | null>(null);
  const anchorRef = React.useRef<HTMLSpanElement | null>(null);
  const menuRef = React.useRef<HTMLDivElement | null>(null);
  const moveRef = React.useRef<HTMLDivElement | null>(null);
  const open = panel !== null;
  const name = path.replace(/\\/g, "/").split("/").pop() || path;
  const { position } = useAnchoredMenuPosition({
    open,
    anchorRef,
    menuRef: panel === "move" ? moveRef : menuRef,
    deps: [panel],
  });

  // 点外面关掉：锚点、菜单、移动面板里的点击都算「在里面」。
  React.useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (
        anchorRef.current?.contains(target) ||
        menuRef.current?.contains(target) ||
        moveRef.current?.contains(target)
      ) {
        return;
      }
      setPanel(null);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setPanel(null);
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  const panelStyle: React.CSSProperties = {
    position: "fixed",
    top: position?.top ?? 0,
    left: position?.left ?? 0,
    zIndex: 10000,
    visibility: position ? "visible" : "hidden",
  };

  return (
    <span
      ref={anchorRef}
      style={{ position: "relative", flexShrink: 0, display: "inline-flex" }}
      // 三点在行内：点它不能连带触发行点击（那会打开文件 / 进入目录）。
      onClick={(event) => event.stopPropagation()}
    >
      <MoreMenuButton
        open={open}
        label={t("fileOperation.menuFor", { name })}
        onClick={() => setPanel((current) => (current === "menu" ? null : "menu"))}
      />
      {panel === "menu" && typeof document !== "undefined"
        ? createPortal(
            <div
              ref={menuRef}
              role="menu"
              style={{ ...moreMenuPopoverStyle, ...panelStyle, right: "auto" }}
            >
              <FileOperationItems
                root={root}
                path={path}
                onMove={() => setPanel("move")}
                onComplete={async () => {
                  await onComplete();
                  setPanel(null);
                }}
              />
            </div>,
            document.body,
          )
        : null}
      {panel === "move" && typeof document !== "undefined"
        ? createPortal(
            <div ref={moveRef} style={panelStyle}>
              <MoveFilePopover
                root={root}
                rootPath={rootPath}
                path={path}
                onComplete={async () => {
                  setPanel(null);
                  await onComplete();
                }}
              />
            </div>,
            document.body,
          )
        : null}
    </span>
  );
}
