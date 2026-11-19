import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { I18nProvider, translateNow } from "../src/i18n";
import { WorkspaceProjectRow } from "../src/components/workspace/WorkspaceProjectRow";

function renderRow(collapsed: boolean) {
  return renderToStaticMarkup(React.createElement(I18nProvider, null,
    React.createElement(WorkspaceProjectRow, {
      group: { key: "node::root", rootId: "root", nodeId: "node", rootName: "Example project", color: null, tasks: [] },
      collapsed,
      onToggle: () => {}, onOpenProject: () => {}, onOpenTask: () => {},
      sessionKeysById: {}, sessionByKey: {}, getNodeColor: () => null,
      onMoveTask: () => {}, onOpenSession: () => {}, onShowSessionError: () => {},
    }),
  ));
}

describe("workspace project toggle", () => {
  for (const collapsed of [false, true]) {
    it(`places the ${collapsed ? "collapsed" : "expanded"} toggle before the project name`, () => {
      const html = renderRow(collapsed);
      const label = translateNow(collapsed ? "task.workspaceExpand" : "task.workspaceCollapse");
      const toggle = html.match(/<button[^>]*aria-label="[^"]*"[^>]*>[\s\S]*?<\/button>/)?.[0];
      expect(toggle).toBeDefined();
      // 无障碍标签与热区尺寸不随位置调整而改变
      expect(toggle).toContain(`aria-label="${label}"`);
      expect(toggle).toContain(`title="${label}"`);
      expect(toggle).toContain("min-width:40px;height:24px");
      // 图标方向仍在，箭头状态由 collapsed 决定
      expect(toggle).toContain(`rotate(${collapsed ? 0 : 90}deg)`);
      // 不再靠 margin-left:auto 钉在行尾
      expect(toggle).not.toContain("margin-left:auto");
      expect(html).not.toContain("margin-left:auto");

      const toggleAt = html.indexOf(toggle!);
      const nameAt = html.indexOf('title="Example project"');
      const badgeAt = html.indexOf("height:18px;border-radius:9px");
      expect(nameAt).toBeGreaterThan(-1);
      expect(badgeAt).toBeGreaterThan(-1);
      // 顺序：折叠箭头 → 项目名 → 计数徽章
      expect(toggleAt).toBeLessThan(nameAt);
      expect(nameAt).toBeLessThan(badgeAt);

      // 左侧三个元素紧凑贴左：名字不吃剩余宽度，右半边留空；超长名字 55% 封顶
      expect(html).not.toContain("flex:1 1 auto");
      expect(html).toContain("max-width:55%");
      expect(html).toContain("text-overflow:ellipsis");

      // 展开时才有任务网格，折叠时整块不渲染
      expect(html.includes("display:grid")).toBe(!collapsed);
    });
  }
});
