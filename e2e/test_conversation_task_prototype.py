#!/usr/bin/env python3
"""
对话式任务原型端到端测试

验证：
1. 后端 API 返回任务活动列表
2. 后端 API 接受新的留言
3. 前端组件能够渲染对话流
"""

import os
import sys
import json
from playwright.sync_api import sync_playwright, expect

# 测试实例配置
TEST_PORT = os.environ.get("MINDFS_TEST_PORT", "7431")
BASE_URL = f"http://127.0.0.1:{TEST_PORT}"

def test_conversation_task_api():
    """测试对话式任务 API 端点"""

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context()
        page = context.new_page()

        # 1. 先通过登录页面建立会话
        page.goto(f"{BASE_URL}/mindfs/")

        # 等待页面加载，检查是否有密码输入框（登录页）或者项目列表（已登录）
        try:
            page.wait_for_selector('input[type="password"]', timeout=2000)
            # 需要登录
            page.fill('input[type="password"]', 'test123')
            page.locator('button:has-text("登录"), button:has-text("Login"), button[type="submit"]').first.click()
            page.wait_for_load_state('networkidle', timeout=10000)
        except:
            # 可能已经登录或者是其他界面，继续
            pass

        # 2. 获取第一个项目的 root_id
        response = page.request.get(f"{BASE_URL}/mindfs/api/dirs")
        assert response.status == 200, f"GET /api/dirs 失败: {response.status}"
        dirs = response.json()
        assert len(dirs) > 0, "需要至少一个项目"
        root_id = dirs[0]['id']
        print(f"✓ 使用项目: {root_id}")

        # 3. 创建一个测试任务（如果没有任务的话）
        response = page.request.get(
            f"{BASE_URL}/mindfs/api/tasks",
            params={"root": root_id}
        )
        assert response.status == 200
        data = response.json()
        tasks = data.get('items', [])

        if len(tasks) == 0:
            # 创建测试任务
            response = page.request.post(
                f"{BASE_URL}/mindfs/api/tasks",
                data=json.dumps({
                    "root_id": root_id,
                    "title": "E2E 测试任务",
                    "description": "用于测试对话式任务 API",
                    "stages": [
                        {
                            "role": "user",
                            "prompt": "测试提示词"
                        }
                    ]
                }),
                headers={"Content-Type": "application/json"}
            )
            if response.status != 200:
                print(f"创建任务失败: {response.status}")
                print(f"响应: {response.text()}")
                raise AssertionError(f"创建任务失败: {response.status}")
            task_data = response.json()
            task_id = task_data['task']['id']
            print(f"✓ 创建测试任务: {task_id}")
        else:
            # API 返回的结构是 {task: {...}, stage_runs: [...], events: [...]}
            task_id = tasks[0]['task']['id']
            print(f"✓ 使用现有任务: {task_id}")

        # 4. 测试 GET /api/tasks/{id}/activities
        response = page.request.get(
            f"{BASE_URL}/mindfs/api/tasks/{task_id}/activities",
            params={"root": root_id}
        )
        assert response.status == 200
        data = response.json()
        assert 'activities' in data
        activities = data['activities']
        print(f"✓ GET activities 成功，返回 {len(activities)} 条")

        # 5. 测试 POST /api/tasks/{id}/activities
        test_message = "测试留言 - E2E validation"
        response = page.request.post(
            f"{BASE_URL}/mindfs/api/tasks/{task_id}/activities",
            data=json.dumps({
                "root_id": root_id,
                "body": test_message,
                "workflow_choice": "empty"
            }),
            headers={"Content-Type": "application/json"}
        )
        assert response.status == 200
        print(f"✓ POST activity 成功")

        # 6. 验证新留言出现在列表中
        response = page.request.get(
            f"{BASE_URL}/mindfs/api/tasks/{task_id}/activities",
            params={"root": root_id}
        )
        assert response.status == 200
        data = response.json()
        new_activities = data['activities']
        assert len(new_activities) > len(activities), "新留言应该被添加"

        # 找到刚才添加的留言
        found = False
        for activity in new_activities:
            if activity.get('body') == test_message:
                assert activity['role'] == 'user'
                found = True
                break
        assert found, "应该能找到刚才添加的留言"
        print(f"✓ 新留言已添加并可查询")

        browser.close()
        print("\n✅ 对话式任务 API 测试通过")


def test_conversation_task_ui():
    """测试前端对话式任务 UI 组件（需要组件被集成到页面中）"""

    # 注：当前前端组件已创建但尚未集成到任务详情页
    # 此测试作为未来集成后的验证预留
    print("⚠️  前端 UI 测试跳过 - 组件尚未集成到任务详情页")


if __name__ == "__main__":
    try:
        print(f"测试目标: {BASE_URL}\n")
        test_conversation_task_api()
        # test_conversation_task_ui()  # 集成后启用
        print("\n✅ 所有测试通过")
        sys.exit(0)
    except AssertionError as e:
        print(f"\n❌ 测试失败: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(1)
    except Exception as e:
        print(f"\n❌ 测试异常: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(1)
