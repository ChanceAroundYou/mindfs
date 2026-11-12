/**
 * 工作流编辑器自动化测试
 *
 * 使用独立测试实例和 Playwright 进行全面测试
 */

import { test, expect, type Page } from '@playwright/test';

const TEST_PORT = 7431; // 使用独立端口避免影响生产实例
const BASE_URL = `http://localhost:${TEST_PORT}`;

test.describe('Workflow Editor - Desktop', () => {
  test.use({ viewport: { width: 1920, height: 1080 } });

  test('should display workflow editor with proper layout', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);

    // 等待登录或直接进入（根据实际情况）
    await page.waitForLoadState('networkidle');

    // 打开工作流编辑器
    await page.click('[data-testid="workflow-editor-button"]', { timeout: 5000 });

    // 验证编辑器已打开
    await expect(page.locator('[data-testid="workflow-draft-editor"]')).toBeVisible();

    // 验证左侧列表
    await expect(page.locator('nav[aria-label*="草稿"]')).toBeVisible();

    // 验证画布区域
    await expect(page.locator('[data-testid="workflow-canvas"]')).toBeVisible();
  });

  test('should allow creating new workflow', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 点击新建按钮
    await page.click('button:has-text("新建")');

    // 等待新工作流创建
    await page.waitForTimeout(1000);

    // 验证画布中有初始节点
    await expect(page.locator('.react-flow__node')).toHaveCount(1);
  });

  test('should support adding different node types', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 添加 Agent 节点
    await page.click('button:has-text("+ Agent")');

    // 添加 Condition 节点
    await page.click('button:has-text("+ 条件")');

    // 添加 Loop 节点
    await page.click('button:has-text("+ 循环")');

    // 验证节点数量
    await expect(page.locator('.react-flow__node')).toHaveCount(4); // 包括初始节点
  });

  test('should open floating inspector on node click', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 点击节点
    await page.click('.react-flow__node');

    // 验证浮动窗口出现
    await expect(page.locator('[data-testid="workflow-canvas-inspector"]')).toBeVisible();

    // 验证浮动窗口包含编辑字段
    await expect(page.locator('[data-testid="workflow-canvas-inspector"] input')).toBeVisible();
  });

  test('should close inspector with Esc key', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 打开浮动窗口
    await page.click('.react-flow__node');
    await expect(page.locator('[data-testid="workflow-canvas-inspector"]')).toBeVisible();

    // 按 Esc 键
    await page.keyboard.press('Escape');

    // 验证窗口关闭
    await expect(page.locator('[data-testid="workflow-canvas-inspector"]')).not.toBeVisible();
  });

  test('should delete node with Delete key', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 添加一个新节点
    await page.click('button:has-text("+ Agent")');
    const initialCount = await page.locator('.react-flow__node').count();

    // 选中节点
    await page.click('.react-flow__node:last-child');

    // 按 Delete 键
    await page.keyboard.press('Delete');

    // 验证节点被删除
    const afterCount = await page.locator('.react-flow__node').count();
    expect(afterCount).toBe(initialCount - 1);
  });

  test('should support connecting nodes', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 添加第二个节点
    await page.click('button:has-text("+ Agent")');

    // 连接节点（通过拖拽handle）
    const sourceHandle = page.locator('.react-flow__handle-right').first();
    const targetHandle = page.locator('.react-flow__handle-left').last();

    await sourceHandle.dragTo(targetHandle);

    // 验证连接线出现
    await expect(page.locator('.react-flow__edge')).toHaveCount(1);
  });

  test('should display node type icons', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 添加不同类型的节点
    await page.click('button:has-text("+ Agent")');
    await page.click('button:has-text("+ 条件")');
    await page.click('button:has-text("+ 循环")');

    // 验证每个节点都有对应的图标
    await expect(page.locator('.react-flow__node svg')).toHaveCount(await page.locator('.react-flow__node').count());
  });

  test('should show different colors for different node types', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 添加不同类型节点
    await page.click('button:has-text("+ Agent")');
    await page.click('button:has-text("+ 条件")');

    const nodes = page.locator('.react-flow__node');

    // 验证节点有不同的边框颜色
    const firstNodeBorder = await nodes.first().evaluate(el => getComputedStyle(el).borderColor);
    const secondNodeBorder = await nodes.nth(1).evaluate(el => getComputedStyle(el).borderColor);

    expect(firstNodeBorder).not.toBe(secondNodeBorder);
  });

  test('should save workflow changes', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 添加节点使状态变脏
    await page.click('button:has-text("+ Agent")');

    // 验证显示未保存状态
    await expect(page.locator('[data-testid="workflow-canvas-dirty"]')).toBeVisible();

    // 点击保存按钮
    await page.click('button:has-text("保存")');

    // 等待保存完成
    await page.waitForTimeout(1000);

    // 验证未保存标记消失
    await expect(page.locator('[data-testid="workflow-canvas-dirty"]')).not.toBeVisible();
  });
});

test.describe('Workflow Editor - Mobile', () => {
  test.use({ viewport: { width: 375, height: 667 } }); // iPhone SE

  test('should display read-only view on mobile', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 验证显示只读视图
    await expect(page.locator('[data-testid="workflow-canvas-readonly"]')).toBeVisible();

    // 验证画布不可见
    await expect(page.locator('.react-flow')).not.toBeVisible();
  });

  test('should show workflow nodes list on mobile', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 验证节点列表可见
    await expect(page.locator('[data-testid="workflow-canvas-readonly"]')).toBeVisible();

    // 验证有提示信息
    await expect(page.locator('text=/桌面端编辑/i')).toBeVisible();
  });
});

test.describe('Workflow Editor - Loop Functionality', () => {
  test('should support editing loop inner graph', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 添加 Loop 节点
    await page.click('button:has-text("+ 循环")');

    // 点击 Loop 节点
    await page.click('.react-flow__node:has-text("循环")');

    // 点击"编辑内部图"按钮
    await page.click('[data-testid="workflow-loop-enter"]');

    // 验证进入内部图编辑
    await expect(page.locator('text=/内部图/i')).toBeVisible();

    // 验证可以返回
    await expect(page.locator('button:has-text("返回")')).toBeVisible();
  });
});

test.describe('Workflow Editor - Error Handling', () => {
  test('should display validation errors', async ({ page }) => {
    await page.goto(`${BASE_URL}/mindfs`);
    await page.click('[data-testid="workflow-editor-button"]');

    // 创建无效配置（例如：condition 节点没有 routes）
    await page.click('button:has-text("+ 条件")');
    await page.click('.react-flow__node:has-text("条件")');

    // 清空 routes
    await page.fill('input[value*="pass"]', '');

    // 关闭编辑器
    await page.keyboard.press('Escape');

    // 验证显示错误信息
    await expect(page.locator('[data-testid="workflow-canvas-errors"]')).toBeVisible();
  });
});
