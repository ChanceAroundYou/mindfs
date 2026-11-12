# 对话式任务系统实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将任务从"阶段式"重构为"对话式"，用户发留言 → Agent 自动选择 Workflow 执行 → 回复结果

**Architecture:** 新增 `task_activities` 表存储对话流，增强 `workflows` 表支持元数据和自动选择，前端组件从阶段列表重构为对话式界面，保留历史任务兼容性

**Tech Stack:** Go (后端), TypeScript/React (前端), SQLite (数据库), WebSocket (实时通信)

**Spec:** `docs/superpowers/specs/2026-11-12-conversation-driven-task-design.md`

## Global Constraints

- Go 版本 ≥ 1.26.5
- TypeScript 版本 ≥ 5.3
- 所有 API 响应必须在 500ms 内返回（Workflow 执行除外）
- WebSocket 消息必须在 100ms 内广播
- 前端组件文件不超过 500 行（硬线）
- 后端 Go 文件不超过 500 行（硬线）
- 所有新增表必须有索引优化查询
- 历史任务（task_stages_json 不为空）必须正常显示

## Review Focus

1. **空留言提交** - 前端必须禁用发送按钮，后端必须返回 400；测试在 Task 6
2. **并发 Activity 创建** - 同一任务同时提交两条留言，必须串行处理不丢失；测试在 Task 4
3. **WorkflowRun 失败后的 Activity 状态** - Run 失败必须更新 Activity 为 failed + 错误信息；测试在 Task 5
4. **WebSocket 断线重连** - 客户端断线期间的状态更新，重连后必须补发；测试在 Task 8
5. **历史任务无 activities 时的显示** - 必须从 stage_runs 提取总结，不能空白；测试在 Task 11

---

## Phase 1: Workflow Metadata 系统

**目标**: 让 Workflow 可以被描述和自动选择

### Task 1: Workflow 表迁移和元数据

**Files:**
- Create: `server/internal/kanban/migrations/005_workflow_metadata.sql`
- Modify: `server/internal/kanban/workflow_store.go`
- Test: `server/internal/kanban/workflow_store_test.go`

**Interfaces:**
- Consumes: 现有 `workflows` 表
- Produces: `WorkflowMeta` 结构体包含 `Description`, `UseCases`, `Tags`, `AutoSelectEnabled`

- [ ] **Step 1: 写迁移 SQL 的测试**

```go
func TestMigration005WorkflowMetadata(t *testing.T) {
    db := setupTestDB(t)
    runMigrations(db, "004")
    
    // 运行迁移
    err := runMigration(db, "005_workflow_metadata.sql")
    assert.NoError(t, err)
    
    // 验证新列存在
    rows := db.QueryRow("PRAGMA table_info(workflows)")
    columns := extractColumnNames(rows)
    assert.Contains(t, columns, "description")
    assert.Contains(t, columns, "use_cases")
    assert.Contains(t, columns, "tags")
    assert.Contains(t, columns, "auto_select_enabled")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./server/internal/kanban -run TestMigration005`
Expected: FAIL - migration file not found

- [ ] **Step 3: 编写迁移文件**

`server/internal/kanban/migrations/005_workflow_metadata.sql`:
```sql
-- 005_workflow_metadata.sql
ALTER TABLE workflows ADD COLUMN description TEXT;
ALTER TABLE workflows ADD COLUMN use_cases TEXT; -- JSON array
ALTER TABLE workflows ADD COLUMN tags TEXT; -- JSON array
ALTER TABLE workflows ADD COLUMN auto_select_enabled INTEGER DEFAULT 1;

-- 为现有工作流补充默认值
UPDATE workflows SET 
    description = name || ' workflow',
    use_cases = '[]',
    tags = '[]',
    auto_select_enabled = 1
WHERE description IS NULL;
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./server/internal/kanban -run TestMigration005 -v`
Expected: PASS

- [ ] **Step 5: 更新 WorkflowStore 结构体**

在 `workflow_store.go` 添加:
```go
type WorkflowMeta struct {
    ID                 string   `json:"id"`
    Name               string   `json:"name"`
    Description        string   `json:"description"`
    UseCases           []string `json:"use_cases"`
    Tags               []string `json:"tags"`
    AutoSelectEnabled  bool     `json:"auto_select_enabled"`
}

func (s *WorkflowStore) GetMeta(ctx context.Context, id string) (*WorkflowMeta, error)
func (s *WorkflowStore) ListAvailable(ctx context.Context) ([]*WorkflowMeta, error)
func (s *WorkflowStore) UpdateMeta(ctx context.Context, id string, meta *WorkflowMeta) error
```

- [ ] **Step 6: 实现 GetMeta/ListAvailable/UpdateMeta 方法**

实现上述三个方法，使用 `json.Unmarshal` 解析 JSON 字段

- [ ] **Step 7: 添加单元测试**

```go
func TestWorkflowStore_GetMeta(t *testing.T) {
    store := setupWorkflowStore(t)
    meta := &WorkflowMeta{
        ID: "wf-1",
        Name: "Debug",
        Description: "Fix bugs",
        UseCases: []string{"error", "crash"},
        Tags: []string{"debug"},
        AutoSelectEnabled: true,
    }
    store.UpdateMeta(ctx, "wf-1", meta)
    
    got, err := store.GetMeta(ctx, "wf-1")
    assert.NoError(t, err)
    assert.Equal(t, meta.Description, got.Description)
    assert.Equal(t, meta.UseCases, got.UseCases)
}
```

- [ ] **Step 8: 运行测试确认通过**

Run: `go test ./server/internal/kanban -run TestWorkflowStore -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add server/internal/kanban/migrations/005_workflow_metadata.sql \
        server/internal/kanban/workflow_store.go \
        server/internal/kanban/workflow_store_test.go
git commit -m "feat(kanban): add workflow metadata schema and store methods

- 迁移: workflows 表增加 description, use_cases, tags, auto_select_enabled
- WorkflowMeta 结构体
- GetMeta/ListAvailable/UpdateMeta 方法
- 单元测试覆盖

Scope: Phase 1 - Task 1"
```

---

### Task 2: Workflow 推荐 API

**Files:**
- Create: `server/internal/kanban/workflow_recommender.go`
- Create: `server/internal/kanban/workflow_recommender_test.go`
- Modify: `server/internal/api/http_workflows.go`

**Interfaces:**
- Consumes: `WorkflowStore.ListAvailable() ([]*WorkflowMeta, error)`
- Produces: `RecommendWorkflow(ctx, userInput string) (*WorkflowRecommendation, error)`

- [ ] **Step 1: 写推荐器的失败测试**

```go
func TestWorkflowRecommender_Recommend(t *testing.T) {
    recommender := NewWorkflowRecommender(mockStore, mockLLMClient)
    
    rec, err := recommender.Recommend(ctx, "登录报错了")
    assert.NoError(t, err)
    assert.NotEmpty(t, rec.WorkflowID)
    assert.NotEmpty(t, rec.Reason)
    assert.Greater(t, rec.MatchScore, 0.0)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./server/internal/kanban -run TestWorkflowRecommender`
Expected: FAIL - type not defined

- [ ] **Step 3: 实现 WorkflowRecommender**

`workflow_recommender.go`:
```go
type WorkflowRecommendation struct {
    WorkflowID  string  `json:"workflow_id"`
    Reason      string  `json:"reason"`
    MatchScore  float64 `json:"match_score"`
}

type WorkflowRecommender struct {
    store     *WorkflowStore
    llmClient LLMClient // 接口，可注入 mock
}

func (r *WorkflowRecommender) Recommend(ctx context.Context, userInput string) (*WorkflowRecommendation, error) {
    workflows, err := r.store.ListAvailable(ctx)
    if err != nil {
        return nil, err
    }
    
    // 构造 prompt
    prompt := r.buildPrompt(userInput, workflows)
    
    // 调用 LLM
    response, err := r.llmClient.Call(ctx, prompt)
    if err != nil {
        // 降级: 返回第一个可用的 workflow
        if len(workflows) > 0 {
            return &WorkflowRecommendation{
                WorkflowID: workflows[0].ID,
                Reason:     "LLM unavailable, using default",
                MatchScore: 0.5,
            }, nil
        }
        return nil, err
    }
    
    // 解析响应
    return r.parseResponse(response)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./server/internal/kanban -run TestWorkflowRecommender -v`
Expected: PASS

- [ ] **Step 5: 添加 HTTP 端点**

在 `http_workflows.go` 添加:
```go
// GET /api/workflows/available
func (h *WorkflowHandler) ListAvailable(w http.ResponseWriter, r *http.Request) {
    userInput := r.URL.Query().Get("user_input")
    
    workflows, err := h.store.ListAvailable(r.Context())
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    // 如果有 user_input，进行推荐
    if userInput != "" {
        rec, _ := h.recommender.Recommend(r.Context(), userInput)
        // 标记推荐的 workflow
        for _, wf := range workflows {
            if rec != nil && wf.ID == rec.WorkflowID {
                wf.Recommended = true
                wf.MatchScore = rec.MatchScore
            }
        }
    }
    
    json.NewEncoder(w).Encode(map[string]interface{}{
        "workflows": workflows,
    })
}

// POST /api/workflows/recommend
func (h *WorkflowHandler) Recommend(w http.ResponseWriter, r *http.Request) {
    var req struct {
        UserInput   string `json:"user_input"`
        TaskContext *struct {
            TaskID string `json:"task_id"`
        } `json:"task_context"`
    }
    json.NewDecoder(r.Body).Decode(&req)
    
    rec, err := h.recommender.Recommend(r.Context(), req.UserInput)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    json.NewEncoder(w).Encode(map[string]interface{}{
        "recommended": rec,
    })
}
```

- [ ] **Step 6: 注册路由**

在 `http_routes.go` 添加:
```go
r.Get("/api/workflows/available", workflowHandler.ListAvailable)
r.Post("/api/workflows/recommend", workflowHandler.Recommend)
```

- [ ] **Step 7: 集成测试**

```go
func TestWorkflowAPI_ListAvailable(t *testing.T) {
    server := setupTestServer(t)
    
    resp := httptest.NewRecorder()
    req := httptest.NewRequest("GET", "/api/workflows/available", nil)
    server.ServeHTTP(resp, req)
    
    assert.Equal(t, http.StatusOK, resp.Code)
    var body map[string]interface{}
    json.Unmarshal(resp.Body.Bytes(), &body)
    assert.NotEmpty(t, body["workflows"])
}
```

- [ ] **Step 8: 运行测试确认通过**

Run: `go test ./server/internal/api -run TestWorkflowAPI -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add server/internal/kanban/workflow_recommender.go \
        server/internal/kanban/workflow_recommender_test.go \
        server/internal/api/http_workflows.go \
        server/internal/api/http_routes.go
git commit -m "feat(api): workflow recommendation endpoints

- WorkflowRecommender 使用 LLM 推荐 workflow
- GET /api/workflows/available 支持 user_input 参数
- POST /api/workflows/recommend 返回推荐结果
- 降级策略: LLM 不可用时返回默认

Scope: Phase 1 - Task 2"
```

---

### Task 3: 前端 Workflow 选择器组件

**Files:**
- Create: `web/src/components/task/WorkflowSelector.tsx`
- Create: `web/src/components/task/WorkflowPickerDialog.tsx`
- Create: `web/src/services/workflows.ts`
- Test: `web/tests/workflow-selector.test.mjs`

**Interfaces:**
- Consumes: `GET /api/workflows/available`, `POST /api/workflows/recommend`
- Produces: `<WorkflowSelector choice={...} onChange={...} />`

- [ ] **Step 1: 写前端服务层的类型定义**

`web/src/services/workflows.ts`:
```typescript
export interface WorkflowMeta {
  id: string;
  name: string;
  description: string;
  use_cases: string[];
  tags: string[];
  recommended?: boolean;
  match_score?: number;
}

export interface WorkflowRecommendation {
  workflow_id: string;
  reason: string;
  match_score: number;
}

export async function listAvailableWorkflows(
  userInput?: string
): Promise<WorkflowMeta[]> {
  const url = appURL('/api/workflows/available');
  if (userInput) url.searchParams.set('user_input', userInput);
  const res = await fetch(url);
  const json = await res.json();
  return json.workflows;
}

export async function recommendWorkflow(
  userInput: string
): Promise<WorkflowRecommendation> {
  const res = await fetch(appURL('/api/workflows/recommend'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ user_input: userInput }),
  });
  const json = await res.json();
  return json.recommended;
}
```

- [ ] **Step 2: 实现 WorkflowSelector 组件**

`web/src/components/task/WorkflowSelector.tsx`:
```typescript
export type WorkflowChoice = 'empty' | 'specified' | 'disabled';

interface WorkflowSelectorProps {
  choice: WorkflowChoice;
  versionId: string;
  userInput?: string;
  recommendation?: WorkflowRecommendation | null;
  onChange: (choice: WorkflowChoice, versionId: string) => void;
}

export function WorkflowSelector({
  choice,
  versionId,
  userInput,
  recommendation,
  onChange,
}: WorkflowSelectorProps) {
  const { t } = useI18n();
  const [showPicker, setShowPicker] = useState(false);

  return (
    <div className="workflow-selector" data-testid="workflow-selector">
      <div role="radiogroup" aria-label={t('task.workflowChoice')}>
        <button
          role="radio"
          aria-checked={choice === 'empty'}
          onClick={() => onChange('empty', '')}
          data-choice="empty"
        >
          {t('task.workflowChoice.empty')}
          {recommendation && (
            <span className="recommendation-chip">
              {t('task.workflowRecommended', { name: recommendation.workflow_id })}
            </span>
          )}
        </button>

        <button
          role="radio"
          aria-checked={choice === 'specified'}
          onClick={() => setShowPicker(true)}
          data-choice="specified"
        >
          {t('task.workflowChoice.specified')}
        </button>

        <button
          role="radio"
          aria-checked={choice === 'disabled'}
          onClick={() => onChange('disabled', '')}
          data-choice="disabled"
        >
          {t('task.workflowChoice.disabled')}
        </button>
      </div>

      {showPicker && (
        <WorkflowPickerDialog
          selected={versionId}
          onSelect={(id) => {
            onChange('specified', id);
            setShowPicker(false);
          }}
          onClose={() => setShowPicker(false)}
        />
      )}
    </div>
  );
}
```

- [ ] **Step 3: 实现 WorkflowPickerDialog**

`web/src/components/task/WorkflowPickerDialog.tsx`:
```typescript
interface WorkflowPickerDialogProps {
  selected: string;
  onSelect: (id: string) => void;
  onClose: () => void;
}

export function WorkflowPickerDialog({ selected, onSelect, onClose }: WorkflowPickerDialogProps) {
  const { t } = useI18n();
  const [workflows, setWorkflows] = useState<WorkflowMeta[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    listAvailableWorkflows().then((wfs) => {
      setWorkflows(wfs);
      setLoading(false);
    });
  }, []);

  return (
    <div className="workflow-picker-dialog" data-testid="workflow-picker">
      <div className="dialog-overlay" onClick={onClose} />
      <div className="dialog-content">
        <h3>{t('task.selectWorkflow')}</h3>
        {loading ? (
          <div>{t('common.loading')}</div>
        ) : (
          <ul>
            {workflows.map((wf) => (
              <li key={wf.id}>
                <button
                  onClick={() => onSelect(wf.id)}
                  data-selected={wf.id === selected || undefined}
                >
                  <strong>{wf.name}</strong>
                  <p>{wf.description}</p>
                  {wf.tags.length > 0 && (
                    <div className="tags">
                      {wf.tags.map((tag) => (
                        <span key={tag} className="tag">{tag}</span>
                      ))}
                    </div>
                  )}
                </button>
              </li>
            ))}
          </ul>
        )}
        <button onClick={onClose}>{t('common.cancel')}</button>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: 添加 i18n 文案**

在 `web/src/i18n/locales/zh-CN-tasks.ts` 添加:
```typescript
workflowChoice: "工作流选择",
workflowChoiceEmpty: "自动选择",
workflowChoiceSpecified: "指定工作流",
workflowChoiceDisabled: "禁用工作流",
workflowRecommended: "推荐: {{name}}",
selectWorkflow: "选择工作流",
```

- [ ] **Step 5: 写测试**

`web/tests/workflow-selector.test.mjs`:
```javascript
import { test, expect } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import { WorkflowSelector } from '../src/components/task/WorkflowSelector';

test('WorkflowSelector renders three choices', () => {
  const { getByRole } = render(
    <WorkflowSelector choice="empty" versionId="" onChange={() => {}} />
  );
  
  const radios = getByRole('radiogroup').querySelectorAll('[role="radio"]');
  expect(radios).toHaveLength(3);
  expect(radios[0]).toHaveAttribute('data-choice', 'empty');
  expect(radios[1]).toHaveAttribute('data-choice', 'specified');
  expect(radios[2]).toHaveAttribute('data-choice', 'disabled');
});

test('WorkflowSelector calls onChange when choice changes', () => {
  const onChange = vi.fn();
  const { getByRole } = render(
    <WorkflowSelector choice="empty" versionId="" onChange={onChange} />
  );
  
  fireEvent.click(getByRole('radiogroup').querySelector('[data-choice="disabled"]'));
  expect(onChange).toHaveBeenCalledWith('disabled', '');
});

test('WorkflowSelector shows recommendation chip when provided', () => {
  const rec = { workflow_id: 'wf-debug', reason: 'matches', match_score: 0.9 };
  const { getByText } = render(
    <WorkflowSelector
      choice="empty"
      versionId=""
      recommendation={rec}
      onChange={() => {}}
    />
  );
  
  expect(getByText(/推荐: wf-debug/)).toBeInTheDocument();
});
```

- [ ] **Step 6: 运行测试**

Run: `npm test workflow-selector.test.mjs`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add web/src/components/task/WorkflowSelector.tsx \
        web/src/components/task/WorkflowPickerDialog.tsx \
        web/src/services/workflows.ts \
        web/tests/workflow-selector.test.mjs \
        web/src/i18n/locales/zh-CN-tasks.ts
git commit -m "feat(ui): workflow selector component

- WorkflowSelector 三态按钮组（empty/specified/disabled）
- WorkflowPickerDialog 弹窗选择 workflow
- 推荐芯片显示
- 完整测试覆盖

Scope: Phase 1 - Task 3"
```

---

## Phase 2: Activity 系统与执行模型

**目标**: 建立对话式的执行模型

### Task 4: Activity 表和 Store

**Files:**
- Create: `server/internal/kanban/migrations/006_task_activities.sql`
- Create: `server/internal/kanban/activity_store.go`
- Create: `server/internal/kanban/activity_store_test.go`

**Interfaces:**
- Consumes: `tasks` 表
- Produces: `ActivityStore` 提供 `Insert`, `Get`, `List`, `Update` 方法

- [ ] **Step 1: 写迁移 SQL 测试**

```go
func TestMigration006TaskActivities(t *testing.T) {
    db := setupTestDB(t)
    runMigrations(db, "005")
    
    err := runMigration(db, "006_task_activities.sql")
    assert.NoError(t, err)
    
    // 验证表结构
    rows := db.Query("PRAGMA table_info(task_activities)")
    columns := extractColumnNames(rows)
    requiredColumns := []string{
        "id", "task_id", "root_id", "type", "role", "body",
        "author", "status", "summary", "workflow_run_id",
        "workflow_version_id", "workflow_choice", "session_key",
        "created_at", "updated_at",
    }
    for _, col := range requiredColumns {
        assert.Contains(t, columns, col)
    }
    
    // 验证索引
    indexes := db.Query("PRAGMA index_list(task_activities)")
    indexNames := extractIndexNames(indexes)
    assert.Contains(t, indexNames, "idx_task_activities_task")
    assert.Contains(t, indexNames, "idx_task_activities_workflow_run")
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./server/internal/kanban -run TestMigration006`
Expected: FAIL

- [ ] **Step 3: 编写迁移 SQL**

`server/internal/kanban/migrations/006_task_activities.sql`:
```sql
-- 006_task_activities.sql
CREATE TABLE task_activities (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    root_id TEXT NOT NULL,
    
    type TEXT NOT NULL CHECK(type IN ('user_message', 'agent_reply', 'system_event')),
    role TEXT NOT NULL CHECK(role IN ('user', 'agent', 'system')),
    
    body TEXT NOT NULL,
    author TEXT,
    
    status TEXT CHECK(status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    summary TEXT,
    
    workflow_run_id TEXT,
    workflow_version_id TEXT,
    workflow_choice TEXT CHECK(workflow_choice IN ('empty', 'specified', 'disabled')),
    session_key TEXT,
    
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE INDEX idx_task_activities_task ON task_activities(task_id, created_at);
CREATE INDEX idx_task_activities_workflow_run ON task_activities(workflow_run_id);
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./server/internal/kanban -run TestMigration006 -v`
Expected: PASS

- [ ] **Step 5: 定义 TaskActivity 结构体**

`activity_store.go`:
```go
type TaskActivity struct {
    ID        string `json:"id"`
    TaskID    string `json:"task_id"`
    RootID    string `json:"root_id"`
    
    Type string `json:"type"` // user_message, agent_reply, system_event
    Role string `json:"role"` // user, agent, system
    
    Body   string  `json:"body"`
    Author *string `json:"author,omitempty"`
    
    Status  *string `json:"status,omitempty"`  // pending, running, completed, failed, cancelled
    Summary *string `json:"summary,omitempty"`
    
    WorkflowRunID     *string `json:"workflow_run_id,omitempty"`
    WorkflowVersionID *string `json:"workflow_version_id,omitempty"`
    WorkflowChoice    *string `json:"workflow_choice,omitempty"`
    SessionKey        *string `json:"session_key,omitempty"`
    
    CreatedAt int64 `json:"created_at"`
    UpdatedAt int64 `json:"updated_at"`
}

type ActivityStore struct {
    db *sql.DB
}

func NewActivityStore(db *sql.DB) *ActivityStore {
    return &ActivityStore{db: db}
}
```

- [ ] **Step 6: 实现 CRUD 方法**

```go
func (s *ActivityStore) Insert(ctx context.Context, activity *TaskActivity) error
func (s *ActivityStore) Get(ctx context.Context, id string) (*TaskActivity, error)
func (s *ActivityStore) List(ctx context.Context, taskID string) ([]*TaskActivity, error)
func (s *ActivityStore) UpdateStatus(ctx context.Context, id string, status string, summary *string) error
func (s *ActivityStore) UpdateBody(ctx context.Context, id string, body string) error
```

实现时注意：
- `Insert` 自动设置 `created_at` 和 `updated_at`
- `List` 按 `created_at` 升序返回
- `UpdateStatus` 同时更新 `updated_at`

- [ ] **Step 7: 写单元测试**

```go
func TestActivityStore_Insert(t *testing.T) {
    store := setupActivityStore(t)
    activity := &TaskActivity{
        ID:        "act-1",
        TaskID:    "task-1",
        RootID:    "root-1",
        Type:      "user_message",
        Role:      "user",
        Body:      "请修复 bug",
        CreatedAt: time.Now().UnixMilli(),
        UpdatedAt: time.Now().UnixMilli(),
    }
    
    err := store.Insert(ctx, activity)
    assert.NoError(t, err)
    
    got, err := store.Get(ctx, "act-1")
    assert.NoError(t, err)
    assert.Equal(t, activity.Body, got.Body)
}

func TestActivityStore_List(t *testing.T) {
    store := setupActivityStore(t)
    store.Insert(ctx, &TaskActivity{ID: "act-1", TaskID: "task-1", ...})
    store.Insert(ctx, &TaskActivity{ID: "act-2", TaskID: "task-1", ...})
    
    list, err := store.List(ctx, "task-1")
    assert.NoError(t, err)
    assert.Len(t, list, 2)
    assert.Equal(t, "act-1", list[0].ID) // 按创建时间升序
}

func TestActivityStore_UpdateStatus(t *testing.T) {
    store := setupActivityStore(t)
    store.Insert(ctx, &TaskActivity{ID: "act-1", TaskID: "task-1", Status: ptr("pending"), ...})
    
    summary := "已完成"
    err := store.UpdateStatus(ctx, "act-1", "completed", &summary)
    assert.NoError(t, err)
    
    got, _ := store.Get(ctx, "act-1")
    assert.Equal(t, "completed", *got.Status)
    assert.Equal(t, "已完成", *got.Summary)
}

// 并发测试 - Review Focus #2
func TestActivityStore_ConcurrentInsert(t *testing.T) {
    store := setupActivityStore(t)
    
    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(n int) {
            defer wg.Done()
            activity := &TaskActivity{
                ID:     fmt.Sprintf("act-%d", n),
                TaskID: "task-1",
                Body:   fmt.Sprintf("message %d", n),
                ...
            }
            store.Insert(ctx, activity)
        }(i)
    }
    wg.Wait()
    
    list, _ := store.List(ctx, "task-1")
    assert.Len(t, list, 10) // 全部插入成功
}
```

- [ ] **Step 8: 运行测试**

Run: `go test ./server/internal/kanban -run TestActivityStore -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add server/internal/kanban/migrations/006_task_activities.sql \
        server/internal/kanban/activity_store.go \
        server/internal/kanban/activity_store_test.go
git commit -m "feat(kanban): task activities storage layer

- 迁移: task_activities 表
- ActivityStore 提供 Insert/Get/List/UpdateStatus/UpdateBody
- 并发安全测试

Scope: Phase 2 - Task 4"
```

---

### Task 5: Activity Service 和 Workflow 集成

**Files:**
- Create: `server/internal/kanban/activity_service.go`
- Create: `server/internal/kanban/activity_service_test.go`
- Modify: `server/internal/kanban/workflow_store.go` (增加 triggered_by, activity_id)

**Interfaces:**
- Consumes: `ActivityStore`, `WorkflowStore`, `WorkflowRecommender`
- Produces: `CreateUserActivity(ctx, taskID, body, choice, versionID) (*TaskActivity, *TaskActivity, error)`

- [ ] **Step 1: 更新 workflow_runs 表**

在 `migrations/` 添加 `007_workflow_runs_activity.sql`:
```sql
ALTER TABLE workflow_runs ADD COLUMN triggered_by TEXT CHECK(triggered_by IN ('user', 'agent_auto', 'scheduled'));
ALTER TABLE workflow_runs ADD COLUMN activity_id TEXT;
```

- [ ] **Step 2: 写 ActivityService.CreateUserActivity 测试**

```go
func TestActivityService_CreateUserActivity_WithWorkflow(t *testing.T) {
    svc := setupActivityService(t)
    
    userAct, agentAct, err := svc.CreateUserActivity(ctx, "task-1", "修复 bug", "empty", nil)
    
    assert.NoError(t, err)
    assert.Equal(t, "user_message", userAct.Type)
    assert.Equal(t, "修复 bug", userAct.Body)
    
    assert.Equal(t, "agent_reply", agentAct.Type)
    assert.Equal(t, "pending", *agentAct.Status)
    assert.NotNil(t, agentAct.WorkflowRunID)
}

func TestActivityService_CreateUserActivity_Disabled(t *testing.T) {
    svc := setupActivityService(t)
    
    userAct, agentAct, err := svc.CreateUserActivity(ctx, "task-1", "手动执行", "disabled", nil)
    
    assert.NoError(t, err)
    assert.Nil(t, agentAct.WorkflowRunID) // 禁用 workflow 时无 run
}

// Review Focus #3: WorkflowRun 失败后的 Activity 状态
func TestActivityService_WorkflowRunFailure_UpdatesActivity(t *testing.T) {
    svc := setupActivityService(t)
    
    _, agentAct, _ := svc.CreateUserActivity(ctx, "task-1", "test", "empty", nil)
    
    // 模拟 WorkflowRun 失败
    svc.OnWorkflowRunFailed(ctx, *agentAct.WorkflowRunID, "execution error")
    
    updated, _ := svc.activityStore.Get(ctx, agentAct.ID)
    assert.Equal(t, "failed", *updated.Status)
    assert.Contains(t, *updated.Summary, "execution error")
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./server/internal/kanban -run TestActivityService`
Expected: FAIL

- [ ] **Step 4: 实现 ActivityService**

`activity_service.go`:
```go
type ActivityService struct {
    activityStore   *ActivityStore
    workflowStore   *WorkflowStore
    recommender     *WorkflowRecommender
    sessionManager  *session.Manager
    hub             *StreamHub // WebSocket 广播
}

func (s *ActivityService) CreateUserActivity(
    ctx context.Context,
    taskID string,
    body string,
    workflowChoice string,
    workflowVersionID *string,
) (*TaskActivity, *TaskActivity, error) {
    // 1. 验证输入
    if strings.TrimSpace(body) == "" {
        return nil, nil, errors.New("body cannot be empty")
    }
    
    // 2. 获取任务信息
    task, err := s.taskStore.Get(ctx, taskID)
    if err != nil {
        return nil, nil, err
    }
    
    // 3. 创建用户留言
    userAct := &TaskActivity{
        ID:        uuid.New().String(),
        TaskID:    taskID,
        RootID:    task.RootID,
        Type:      "user_message",
        Role:      "user",
        Body:      body,
        CreatedAt: time.Now().UnixMilli(),
        UpdatedAt: time.Now().UnixMilli(),
    }
    if err := s.activityStore.Insert(ctx, userAct); err != nil {
        return nil, nil, err
    }
    
    // 4. 创建 Agent 回复
    var agentAct *TaskActivity
    
    if workflowChoice == "disabled" {
        // 直接启动 Agent（不使用 Workflow）
        agentAct = s.startDirectAgent(ctx, task, body)
    } else {
        // 使用 Workflow
        var wfID string
        if workflowChoice == "specified" && workflowVersionID != nil {
            wfID = *workflowVersionID
        } else {
            // AI 自动选择
            rec, err := s.recommender.Recommend(ctx, body)
            if err != nil || rec == nil {
                // 降级：使用默认 workflow
                wfID = s.getDefaultWorkflowID(ctx)
            } else {
                wfID = rec.WorkflowID
            }
        }
        
        agentAct = s.startWorkflowRun(ctx, task, body, wfID, userAct.ID)
    }
    
    if agentAct != nil {
        s.activityStore.Insert(ctx, agentAct)
    }
    
    // 5. 广播
    s.broadcastActivityCreated(task.RootID, userAct)
    if agentAct != nil {
        s.broadcastActivityCreated(task.RootID, agentAct)
    }
    
    return userAct, agentAct, nil
}

func (s *ActivityService) startWorkflowRun(
    ctx context.Context,
    task *Task,
    userInput string,
    workflowVersionID string,
    triggeredByActivityID string,
) *TaskActivity {
    // 创建 WorkflowRun
    run, err := s.workflowStore.CreateRun(ctx, &CreateRunRequest{
        WorkflowVersionID: workflowVersionID,
        TaskID:            task.ID,
        Input:             userInput,
        TriggeredBy:       "user",
        ActivityID:        triggeredByActivityID,
    })
    if err != nil {
        return s.createErrorActivity(task, err)
    }
    
    // 创建 AgentActivity
    agentAct := &TaskActivity{
        ID:                uuid.New().String(),
        TaskID:            task.ID,
        RootID:            task.RootID,
        Type:              "agent_reply",
        Role:              "agent",
        Body:              "正在准备执行工作流...",
        Status:            ptr("pending"),
        WorkflowRunID:     &run.ID,
        WorkflowVersionID: &workflowVersionID,
        WorkflowChoice:    ptr("empty"), // 或 "specified"
        CreatedAt:         time.Now().UnixMilli(),
        UpdatedAt:         time.Now().UnixMilli(),
    }
    
    // 异步启动执行
    go s.executeWorkflowRun(ctx, run.ID, agentAct.ID)
    
    return agentAct
}

func (s *ActivityService) executeWorkflowRun(ctx context.Context, runID, activityID string) {
    // 更新状态为 running
    s.activityStore.UpdateStatus(ctx, activityID, "running", nil)
    s.activityStore.UpdateBody(ctx, activityID, "正在执行工作流...")
    s.broadcastActivityUpdated(activityID, map[string]interface{}{
        "status": "running",
        "body":   "正在执行工作流...",
    })
    
    // 执行 Workflow
    result, err := s.workflowStore.Execute(ctx, runID)
    
    if err != nil {
        summary := fmt.Sprintf("执行失败: %s", err.Error())
        s.activityStore.UpdateStatus(ctx, activityID, "failed", &summary)
        s.broadcastActivityUpdated(activityID, map[string]interface{}{
            "status":  "failed",
            "summary": summary,
        })
        return
    }
    
    // 生成总结
    summary := s.generateSummary(result)
    
    // 更新为 completed
    s.activityStore.UpdateStatus(ctx, activityID, "completed", &summary)
    s.broadcastActivityUpdated(activityID, map[string]interface{}{
        "status":  "completed",
        "summary": summary,
    })
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./server/internal/kanban -run TestActivityService -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add server/internal/kanban/migrations/007_workflow_runs_activity.sql \
        server/internal/kanban/activity_service.go \
        server/internal/kanban/activity_service_test.go \
        server/internal/kanban/workflow_store.go
git commit -m "feat(kanban): activity service with workflow integration

- ActivityService.CreateUserActivity 处理用户留言
- 自动推荐或手动指定 workflow
- 异步执行 WorkflowRun 并更新 Activity 状态
- WorkflowRun 失败时正确更新 Activity

Scope: Phase 2 - Task 5"
```

---

### Task 6: Activity HTTP API

**Files:**
- Create: `server/internal/api/http_activities.go`
- Create: `server/internal/api/http_activities_test.go`
- Modify: `server/internal/api/http_routes.go`

**Interfaces:**
- Consumes: `ActivityService.CreateUserActivity`, `ActivityStore.List`
- Produces: `POST /api/tasks/{id}/activities`, `GET /api/tasks/{id}/activities`

- [ ] **Step 1: 写 API 测试**

```go
func TestActivityAPI_Create(t *testing.T) {
    server := setupTestServer(t)
    
    reqBody := `{
        "body": "请修复 bug",
        "workflow_choice": "empty"
    }`
    req := httptest.NewRequest("POST", "/api/tasks/task-1/activities", strings.NewReader(reqBody))
    req.Header.Set("Content-Type", "application/json")
    resp := httptest.NewRecorder()
    
    server.ServeHTTP(resp, req)
    
    assert.Equal(t, http.StatusOK, resp.Code)
    var body map[string]interface{}
    json.Unmarshal(resp.Body.Bytes(), &body)
    assert.NotNil(t, body["user_activity"])
    assert.NotNil(t, body["agent_activity"])
}

// Review Focus #1: 空留言提交
func TestActivityAPI_Create_EmptyBody(t *testing.T) {
    server := setupTestServer(t)
    
    reqBody := `{"body": "", "workflow_choice": "empty"}`
    req := httptest.NewRequest("POST", "/api/tasks/task-1/activities", strings.NewReader(reqBody))
    req.Header.Set("Content-Type", "application/json")
    resp := httptest.NewRecorder()
    
    server.ServeHTTP(resp, req)
    
    assert.Equal(t, http.StatusBadRequest, resp.Code)
    assert.Contains(t, resp.Body.String(), "body cannot be empty")
}

func TestActivityAPI_List(t *testing.T) {
    server := setupTestServer(t)
    
    req := httptest.NewRequest("GET", "/api/tasks/task-1/activities", nil)
    resp := httptest.NewRecorder()
    
    server.ServeHTTP(resp, req)
    
    assert.Equal(t, http.StatusOK, resp.Code)
    var body map[string]interface{}
    json.Unmarshal(resp.Body.Bytes(), &body)
    assert.NotNil(t, body["activities"])
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./server/internal/api -run TestActivityAPI`
Expected: FAIL

- [ ] **Step 3: 实现 HTTP Handler**

`http_activities.go`:
```go
type ActivityHandler struct {
    activityService *kanban.ActivityService
    activityStore   *kanban.ActivityStore
}

// POST /api/tasks/{id}/activities
func (h *ActivityHandler) Create(w http.ResponseWriter, r *http.Request) {
    taskID := chi.URLParam(r, "id")
    
    var req struct {
        Body              string  `json:"body"`
        WorkflowChoice    string  `json:"workflow_choice"`    // empty, specified, disabled
        WorkflowVersionID *string `json:"workflow_version_id"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    
    // 验证 workflow_choice
    if req.WorkflowChoice == "" {
        req.WorkflowChoice = "empty"
    }
    if !slices.Contains([]string{"empty", "specified", "disabled"}, req.WorkflowChoice) {
        http.Error(w, "invalid workflow_choice", http.StatusBadRequest)
        return
    }
    
    // 创建活动
    userAct, agentAct, err := h.activityService.CreateUserActivity(
        r.Context(),
        taskID,
        req.Body,
        req.WorkflowChoice,
        req.WorkflowVersionID,
    )
    if err != nil {
        if err.Error() == "body cannot be empty" {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "user_activity":  userAct,
        "agent_activity": agentAct,
    })
}

// GET /api/tasks/{id}/activities
func (h *ActivityHandler) List(w http.ResponseWriter, r *http.Request) {
    taskID := chi.URLParam(r, "id")
    
    activities, err := h.activityStore.List(r.Context(), taskID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "activities": activities,
    })
}

// GET /api/tasks/{taskId}/activities/{activityId}
func (h *ActivityHandler) Get(w http.ResponseWriter, r *http.Request) {
    activityID := chi.URLParam(r, "activityId")
    
    activity, err := h.activityStore.Get(r.Context(), activityID)
    if err != nil {
        http.Error(w, err.Error(), http.StatusNotFound)
        return
    }
    
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(activity)
}
```

- [ ] **Step 4: 注册路由**

在 `http_routes.go`:
```go
activityHandler := &ActivityHandler{
    activityService: activityService,
    activityStore:   activityStore,
}

r.Post("/api/tasks/{id}/activities", activityHandler.Create)
r.Get("/api/tasks/{id}/activities", activityHandler.List)
r.Get("/api/tasks/{taskId}/activities/{activityId}", activityHandler.Get)
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./server/internal/api -run TestActivityAPI -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add server/internal/api/http_activities.go \
        server/internal/api/http_activities_test.go \
        server/internal/api/http_routes.go
git commit -m "feat(api): activity HTTP endpoints

- POST /api/tasks/{id}/activities 创建用户留言
- GET /api/tasks/{id}/activities 获取活动流
- GET /api/tasks/{id}/activities/{activityId} 获取单个活动
- 空留言验证

Scope: Phase 2 - Task 6"
```

---

### Task 7: WebSocket 事件广播

**Files:**
- Modify: `server/internal/api/ws.go`
- Modify: `server/internal/kanban/activity_service.go`
- Test: `server/internal/api/ws_test.go`

**Interfaces:**
- Consumes: `ActivityService` 的广播方法
- Produces: WS 事件 `task.activity.created`, `task.activity.updated`

- [ ] **Step 1: 定义 WS 事件类型**

在 `ws.go` 添加:
```go
const (
    EventTaskActivityCreated = "task.activity.created"
    EventTaskActivityUpdated = "task.activity.updated"
)

type TaskActivityCreatedPayload struct {
    TaskID   string        `json:"task_id"`
    Activity *TaskActivity `json:"activity"`
}

type TaskActivityUpdatedPayload struct {
    TaskID     string                 `json:"task_id"`
    ActivityID string                 `json:"activity_id"`
    Changes    map[string]interface{} `json:"changes"`
}
```

- [ ] **Step 2: 实现广播方法**

在 `activity_service.go`:
```go
func (s *ActivityService) broadcastActivityCreated(rootID string, activity *TaskActivity) {
    s.hub.Broadcast(&WSMessage{
        Type: EventTaskActivityCreated,
        Payload: TaskActivityCreatedPayload{
            TaskID:   activity.TaskID,
            Activity: activity,
        },
    })
}

func (s *ActivityService) broadcastActivityUpdated(activityID string, changes map[string]interface{}) {
    activity, _ := s.activityStore.Get(context.Background(), activityID)
    if activity == nil {
        return
    }
    
    s.hub.Broadcast(&WSMessage{
        Type: EventTaskActivityUpdated,
        Payload: TaskActivityUpdatedPayload{
            TaskID:     activity.TaskID,
            ActivityID: activityID,
            Changes:    changes,
        },
    })
}
```

- [ ] **Step 3: 集成到 WorkflowRun 执行过程**

在 `executeWorkflowRun` 方法中，在节点开始/完成时广播:
```go
func (s *ActivityService) onWorkflowNodeStarted(runID, nodeID string) {
    run := s.workflowStore.GetRun(context.Background(), runID)
    activity := s.activityStore.GetByWorkflowRunID(context.Background(), runID)
    
    if run != nil && activity != nil {
        currentNode := run.GetNode(nodeID)
        progressText := fmt.Sprintf("步骤 %d/%d: %s", 
            currentNode.Index, 
            len(run.Nodes), 
            currentNode.Name,
        )
        
        s.activityStore.UpdateBody(context.Background(), activity.ID, progressText)
        s.broadcastActivityUpdated(activity.ID, map[string]interface{}{
            "body": progressText,
        })
    }
}
```

- [ ] **Step 4: 写集成测试**

```go
func TestWebSocket_ActivityEvents(t *testing.T) {
    server, wsClient := setupTestServerWithWS(t)
    
    // 创建活动
    reqBody := `{"body": "test", "workflow_choice": "empty"}`
    req := httptest.NewRequest("POST", "/api/tasks/task-1/activities", strings.NewReader(reqBody))
    resp := httptest.NewRecorder()
    server.ServeHTTP(resp, req)
    
    // 等待 WS 消息
    select {
    case msg := <-wsClient.Messages:
        assert.Equal(t, "task.activity.created", msg.Type)
        payload := msg.Payload.(TaskActivityCreatedPayload)
        assert.Equal(t, "task-1", payload.TaskID)
        assert.NotNil(t, payload.Activity)
    case <-time.After(1 * time.Second):
        t.Fatal("timeout waiting for WS message")
    }
}

// Review Focus #4: WebSocket 断线重连
func TestWebSocket_ReconnectReceivesMissedUpdates(t *testing.T) {
    server, wsClient := setupTestServerWithWS(t)
    
    // 创建活动
    _, agentAct := createTestActivity(server, "task-1")
    
    // 断开 WS
    wsClient.Close()
    
    // 更新活动状态
    activityService.UpdateActivityStatus(ctx, agentAct.ID, "completed", ptr("done"))
    
    // 重连
    wsClient2 := reconnectWS(server)
    
    // 客户端应该能查询到最新状态（通过 HTTP GET 补偿）
    req := httptest.NewRequest("GET", "/api/tasks/task-1/activities/"+agentAct.ID, nil)
    resp := httptest.NewRecorder()
    server.ServeHTTP(resp, req)
    
    var activity TaskActivity
    json.Unmarshal(resp.Body.Bytes(), &activity)
    assert.Equal(t, "completed", *activity.Status)
}
```

- [ ] **Step 5: 运行测试**

Run: `go test ./server/internal/api -run TestWebSocket -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add server/internal/api/ws.go \
        server/internal/api/ws_test.go \
        server/internal/kanban/activity_service.go
git commit -m "feat(ws): activity event broadcasting

- task.activity.created 事件
- task.activity.updated 事件
- WorkflowRun 执行过程中广播进度
- WebSocket 断线重连测试

Scope: Phase 2 - Task 7"
```

---

## Phase 3: 前端重构

**目标**: 对话式任务详情面板

### Task 8: 前端数据层和 Hooks

**Files:**
- Create: `web/src/services/activities.ts`
- Create: `web/src/hooks/useTaskActivities.ts`
- Create: `web/src/hooks/useWorkflowRun.ts`
- Test: `web/tests/activities-service.test.mjs`

**Interfaces:**
- Consumes: Activity API 端点
- Produces: `useTaskActivities(taskId)`, `useWorkflowRun(runId)`

- [ ] **Step 1: 实现 activities 服务层**

`web/src/services/activities.ts`:
```typescript
export interface TaskActivity {
  id: string;
  task_id: string;
  root_id: string;
  type: 'user_message' | 'agent_reply' | 'system_event';
  role: 'user' | 'agent' | 'system';
  body: string;
  author?: string;
  status?: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled';
  summary?: string;
  workflow_run_id?: string;
  workflow_version_id?: string;
  workflow_choice?: 'empty' | 'specified' | 'disabled';
  session_key?: string;
  created_at: number;
  updated_at: number;
}

export interface CreateActivityRequest {
  body: string;
  workflow_choice: 'empty' | 'specified' | 'disabled';
  workflow_version_id?: string;
}

export interface CreateActivityResponse {
  user_activity: TaskActivity;
  agent_activity?: TaskActivity;
}

export async function listActivities(
  taskId: string,
  nodeId?: string
): Promise<TaskActivity[]> {
  const url = appURL(`/api/tasks/${taskId}/activities`, nodeId);
  const res = await fetch(url);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const json = await res.json();
  return json.activities;
}

export async function createActivity(
  taskId: string,
  req: CreateActivityRequest,
  nodeId?: string
): Promise<CreateActivityResponse> {
  const url = appURL(`/api/tasks/${taskId}/activities`, nodeId);
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `HTTP ${res.status}`);
  }
  return res.json();
}

export async function getActivity(
  taskId: string,
  activityId: string,
  nodeId?: string
): Promise<TaskActivity> {
  const url = appURL(`/api/tasks/${taskId}/activities/${activityId}`, nodeId);
  const res = await fetch(url);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}
```

- [ ] **Step 2: 实现 useTaskActivities hook**

`web/src/hooks/useTaskActivities.ts`:
```typescript
export function useTaskActivities(taskId: string, nodeId?: string) {
  const [activities, setActivities] = useState<TaskActivity[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      const data = await listActivities(taskId, nodeId);
      setActivities(data);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [taskId, nodeId]);

  useEffect(() => {
    load();
  }, [load]);

  // WebSocket 监听
  useEffect(() => {
    const handler = (event: WSMessage) => {
      if (event.type === 'task.activity.created') {
        const payload = event.payload as TaskActivityCreatedPayload;
        if (payload.task_id === taskId) {
          setActivities((prev) => [...prev, payload.activity]);
        }
      } else if (event.type === 'task.activity.updated') {
        const payload = event.payload as TaskActivityUpdatedPayload;
        if (payload.task_id === taskId) {
          setActivities((prev) =>
            prev.map((act) =>
              act.id === payload.activity_id
                ? { ...act, ...payload.changes }
                : act
            )
          );
        }
      }
    };

    const unsubscribe = subscribeToWS(handler);
    return unsubscribe;
  }, [taskId]);

  return { activities, loading, error, reload: load };
}
```

- [ ] **Step 3: 实现 useWorkflowRun hook**

`web/src/hooks/useWorkflowRun.ts`:
```typescript
export interface WorkflowRunStatus {
  id: string;
  status: string;
  current_node: number;
  total_nodes: number;
  nodes: Array<{
    id: string;
    name: string;
    status: string;
    description?: string;
  }>;
}

export function useWorkflowRun(runId?: string) {
  const [run, setRun] = useState<WorkflowRunStatus | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!runId) {
      setRun(null);
      return;
    }

    setLoading(true);
    fetch(appURL(`/api/workflow-runs/${runId}`))
      .then((res) => res.json())
      .then((data) => {
        setRun(data);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, [runId]);

  return { run, loading };
}
```

- [ ] **Step 4: 写测试**

`web/tests/activities-service.test.mjs`:
```javascript
import { test, expect, vi } from 'vitest';
import { listActivities, createActivity } from '../src/services/activities';

test('listActivities fetches from correct endpoint', async () => {
  global.fetch = vi.fn(() =>
    Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ activities: [] }),
    })
  );

  await listActivities('task-1');

  expect(fetch).toHaveBeenCalledWith(
    expect.stringContaining('/api/tasks/task-1/activities')
  );
});

test('createActivity sends correct payload', async () => {
  global.fetch = vi.fn(() =>
    Promise.resolve({
      ok: true,
      json: () => Promise.resolve({ user_activity: {}, agent_activity: {} }),
    })
  );

  await createActivity('task-1', {
    body: 'test',
    workflow_choice: 'empty',
  });

  expect(fetch).toHaveBeenCalledWith(
    expect.stringContaining('/api/tasks/task-1/activities'),
    expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ body: 'test', workflow_choice: 'empty' }),
    })
  );
});

test('createActivity throws on empty body', async () => {
  global.fetch = vi.fn(() =>
    Promise.resolve({
      ok: false,
      status: 400,
      text: () => Promise.resolve('body cannot be empty'),
    })
  );

  await expect(
    createActivity('task-1', { body: '', workflow_choice: 'empty' })
  ).rejects.toThrow('body cannot be empty');
});
```

- [ ] **Step 5: 运行测试**

Run: `npm test activities-service.test.mjs`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/services/activities.ts \
        web/src/hooks/useTaskActivities.ts \
        web/src/hooks/useWorkflowRun.ts \
        web/tests/activities-service.test.mjs
git commit -m "feat(ui): activity data layer and hooks

- activities 服务层
- useTaskActivities hook with WS updates
- useWorkflowRun hook
- 完整测试覆盖

Scope: Phase 3 - Task 8"
```

---

### Task 9: ActivityStream 和卡片组件

**Files:**
- Create: `web/src/components/task/ActivityStream.tsx`
- Create: `web/src/components/task/UserMessageCard.tsx`
- Create: `web/src/components/task/AgentReplyCard.tsx`
- Create: `web/src/components/task/ProgressView.tsx`
- Test: `web/tests/activity-stream.test.mjs`

**Interfaces:**
- Consumes: `useTaskActivities()` 返回的 activities
- Produces: `<ActivityStream taskId={...} />`

- [ ] **Step 1: 实现 UserMessageCard**

`web/src/components/task/UserMessageCard.tsx` (≤150 行):
```typescript
interface UserMessageCardProps {
  activity: TaskActivity;
}

export function UserMessageCard({ activity }: UserMessageCardProps) {
  const { t } = useI18n();

  return (
    <div
      className="activity-card user-message"
      data-testid="user-message"
      style={{
        alignSelf: 'flex-end',
        maxWidth: '85%',
        border: '1px solid var(--border-color)',
        borderRadius: '8px',
        padding: '10px',
        background: 'var(--content-bg)',
      }}
    >
      <div style={{ display: 'flex', gap: '6px', alignItems: 'center', marginBottom: '4px' }}>
        <span style={{ fontSize: '11px', fontWeight: 700, color: 'var(--text-color)' }}>
          {activity.author || t('task.activity.user')}
        </span>
        <span style={{ fontSize: '10px', color: 'var(--text-secondary)' }}>
          {formatTimestamp(activity.created_at)}
        </span>
      </div>
      <div style={{ fontSize: '12px', whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>
        {activity.body}
      </div>
    </div>
  );
}
```

- [ ] **Step 2: 实现 ProgressView**

`web/src/components/task/ProgressView.tsx` (≤100 行):
```typescript
interface ProgressViewProps {
  activity: TaskActivity;
}

export function ProgressView({ activity }: ProgressViewProps) {
  const { run, loading } = useWorkflowRun(activity.workflow_run_id);

  if (loading || !run) {
    return <Spinner />;
  }

  const { current_node, total_nodes, nodes } = run;
  const currentNodeInfo = nodes.find((n) => n.status === 'running');

  return (
    <div className="progress-view" data-testid="progress-view">
      <div className="progress-bar" style={{ marginBottom: '8px' }}>
        <div
          style={{
            width: `${(current_node / total_nodes) * 100}%`,
            height: '4px',
            background: 'var(--accent-color)',
            borderRadius: '2px',
            transition: 'width 0.3s ease',
          }}
        />
      </div>
      <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>
        步骤 {current_node}/{total_nodes}
        {currentNodeInfo && `: ${currentNodeInfo.name}`}
      </div>
    </div>
  );
}
```

- [ ] **Step 3: 实现 AgentReplyCard**

`web/src/components/task/AgentReplyCard.tsx` (≤200 行):
```typescript
interface AgentReplyCardProps {
  activity: TaskActivity;
  onViewDetails: () => void;
  onOpenSession: (sessionKey: string) => void;
}

export function AgentReplyCard({
  activity,
  onViewDetails,
  onOpenSession,
}: AgentReplyCardProps) {
  const { t } = useI18n();
  const { status, summary, workflow_run_id, session_key } = activity;

  const statusColor = {
    pending: 'var(--text-secondary)',
    running: 'var(--accent-color)',
    completed: 'var(--status-ok)',
    failed: '#dc2626',
    cancelled: 'var(--text-secondary)',
  }[status || 'pending'];

  return (
    <div
      className="activity-card agent-reply"
      data-testid="agent-reply"
      data-status={status}
      style={{
        alignSelf: 'flex-start',
        maxWidth: '85%',
        border: '1px solid var(--border-color)',
        borderRadius: '8px',
        padding: '10px',
        background: 'var(--content-bg)',
      }}
    >
      {/* Header */}
      <div style={{ display: 'flex', gap: '6px', alignItems: 'center', marginBottom: '8px' }}>
        <AgentIcon size={14} />
        <span style={{ fontSize: '11px', fontWeight: 700, color: 'var(--accent-color)' }}>
          {t('task.activity.agent')}
        </span>
        <span
          style={{
            fontSize: '10px',
            fontWeight: 700,
            color: statusColor,
            border: `1px solid ${statusColor}`,
            borderRadius: '4px',
            padding: '0 4px',
          }}
        >
          {t(`task.activity.status.${status}`)}
        </span>
        <span style={{ fontSize: '10px', color: 'var(--text-secondary)' }}>
          {formatTimestamp(activity.updated_at)}
        </span>
      </div>

      {/* Body */}
      <div style={{ marginBottom: '8px' }}>
        {status === 'pending' && <div style={{ fontSize: '12px' }}>{activity.body}</div>}
        {status === 'running' && <ProgressView activity={activity} />}
        {status === 'completed' && summary && (
          <div style={{ fontSize: '12px', whiteSpace: 'pre-wrap' }}>{summary}</div>
        )}
        {status === 'failed' && summary && (
          <div style={{ fontSize: '12px', color: '#dc2626' }}>{summary}</div>
        )}
      </div>

      {/* Actions */}
      <div style={{ display: 'flex', gap: '6px' }}>
        {workflow_run_id && (
          <button
            type="button"
            onClick={onViewDetails}
            style={{
              fontSize: '11px',
              padding: '4px 8px',
              borderRadius: '4px',
              border: '1px solid var(--border-color)',
              background: 'transparent',
              cursor: 'pointer',
            }}
          >
            {t('task.activity.viewDetails')} →
          </button>
        )}
        {session_key && (
          <button
            type="button"
            onClick={() => onOpenSession(session_key)}
            style={{
              fontSize: '11px',
              padding: '4px 8px',
              borderRadius: '4px',
              border: '1px solid var(--border-color)',
              background: 'transparent',
              cursor: 'pointer',
            }}
          >
            {t('task.activity.openSession')}
          </button>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 4: 实现 ActivityStream**

`web/src/components/task/ActivityStream.tsx` (≤150 行):
```typescript
interface ActivityStreamProps {
  taskId: string;
  rootId: string;
  nodeId?: string;
  onOpenSession: (sessionKey: string) => void;
  onViewWorkflowRun: (runId: string) => void;
}

export function ActivityStream({
  taskId,
  rootId,
  nodeId,
  onOpenSession,
  onViewWorkflowRun,
}: ActivityStreamProps) {
  const { t } = useI18n();
  const { activities, loading, error } = useTaskActivities(taskId, nodeId);

  if (loading) {
    return <div style={{ fontSize: '12px', color: 'var(--text-secondary)' }}>{t('common.loading')}</div>;
  }

  if (error) {
    return <div style={{ fontSize: '12px', color: '#dc2626' }}>{error}</div>;
  }

  if (activities.length === 0) {
    return <div style={{ fontSize: '12px', color: 'var(--text-secondary)' }}>{t('task.activity.empty')}</div>;
  }

  return (
    <div
      className="activity-stream"
      data-testid="activity-stream"
      style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}
    >
      {activities.map((activity) => {
        if (activity.type === 'user_message') {
          return <UserMessageCard key={activity.id} activity={activity} />;
        }
        if (activity.type === 'agent_reply') {
          return (
            <AgentReplyCard
              key={activity.id}
              activity={activity}
              onViewDetails={() =>
                activity.workflow_run_id && onViewWorkflowRun(activity.workflow_run_id)
              }
              onOpenSession={onOpenSession}
            />
          );
        }
        return null; // system_event 可以后续添加
      })}
    </div>
  );
}
```

- [ ] **Step 5: 添加 i18n**

在 `zh-CN-tasks.ts` 添加:
```typescript
activity: {
  user: "用户",
  agent: "Agent",
  empty: "暂无对话",
  viewDetails: "查看执行详情",
  openSession: "打开会话",
  status: {
    pending: "等待中",
    running: "执行中",
    completed: "已完成",
    failed: "失败",
    cancelled: "已取消",
  },
},
```

- [ ] **Step 6: 写测试**

`web/tests/activity-stream.test.mjs`:
```javascript
test('ActivityStream renders user and agent messages', async () => {
  const activities = [
    { id: '1', type: 'user_message', role: 'user', body: 'test', created_at: Date.now() },
    { id: '2', type: 'agent_reply', role: 'agent', status: 'completed', summary: 'done', created_at: Date.now() },
  ];

  const { getByTestId, getAllByTestId } = render(
    <MockProvider activities={activities}>
      <ActivityStream taskId="task-1" rootId="root-1" onOpenSession={() => {}} onViewWorkflowRun={() => {}} />
    </MockProvider>
  );

  expect(getByTestId('activity-stream')).toBeInTheDocument();
  expect(getByTestId('user-message')).toHaveTextContent('test');
  expect(getByTestId('agent-reply')).toHaveTextContent('done');
});

test('AgentReplyCard shows progress when running', () => {
  const activity = {
    id: '1',
    type: 'agent_reply',
    status: 'running',
    workflow_run_id: 'run-1',
    ...
  };

  const { getByTestId } = render(<AgentReplyCard activity={activity} onViewDetails={() => {}} onOpenSession={() => {}} />);

  expect(getByTestId('progress-view')).toBeInTheDocument();
});
```

- [ ] **Step 7: 运行测试**

Run: `npm test activity-stream.test.mjs`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add web/src/components/task/ActivityStream.tsx \
        web/src/components/task/UserMessageCard.tsx \
        web/src/components/task/AgentReplyCard.tsx \
        web/src/components/task/ProgressView.tsx \
        web/tests/activity-stream.test.mjs \
        web/src/i18n/locales/zh-CN-tasks.ts
git commit -m "feat(ui): activity stream components

- UserMessageCard 用户留言卡片
- AgentReplyCard Agent 回复卡片（pending/running/completed/failed）
- ProgressView 实时进度显示
- ActivityStream 对话流容器
- 完整测试覆盖

Scope: Phase 3 - Task 9"
```

---

### Task 10: ActivityInput 和集成到 TaskDetailPanel

**Files:**
- Create: `web/src/components/task/ActivityInput.tsx`
- Modify: `web/src/components/task/TaskDetailPanel.tsx`
- Test: `web/tests/activity-input.test.mjs`

**Interfaces:**
- Consumes: `createActivity()`, `<WorkflowSelector />`
- Produces: `<ActivityInput taskId={...} onSend={...} />`

- [ ] **Step 1: 实现 ActivityInput**

`web/src/components/task/ActivityInput.tsx` (≤200 行):
```typescript
interface ActivityInputProps {
  taskId: string;
  nodeId?: string;
  onSuccess?: () => void;
}

export function ActivityInput({ taskId, nodeId, onSuccess }: ActivityInputProps) {
  const { t } = useI18n();
  const [body, setBody] = useState('');
  const [choice, setChoice] = useState<WorkflowChoice>('empty');
  const [versionId, setVersionId] = useState('');
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [recommendation, setRecommendation] = useState<WorkflowRecommendation | null>(null);

  // AI 推荐（debounce）
  useEffect(() => {
    if (body.trim() && choice === 'empty') {
      const timer = setTimeout(() => {
        recommendWorkflow(body).then(setRecommendation).catch(() => {});
      }, 500);
      return () => clearTimeout(timer);
    } else {
      setRecommendation(null);
    }
  }, [body, choice]);

  const handleSend = async () => {
    if (!body.trim() || sending) return;
    if (choice === 'specified' && !versionId.trim()) {
      setError(t('task.activity.workflowRequired'));
      return;
    }

    try {
      setSending(true);
      setError(null);
      await createActivity(
        taskId,
        {
          body: body.trim(),
          workflow_choice: choice,
          workflow_version_id: choice === 'specified' ? versionId : undefined,
        },
        nodeId
      );
      setBody('');
      setChoice('empty');
      setVersionId('');
      onSuccess?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSending(false);
    }
  };

  return (
    <div
      className="activity-input"
      data-testid="activity-input"
      style={{
        border: '1px solid var(--border-color)',
        borderRadius: '8px',
        padding: '10px',
        background: 'var(--panel-bg)',
        display: 'flex',
        flexDirection: 'column',
        gap: '8px',
      }}
    >
      {/* Workflow 选择器 */}
      <WorkflowSelector
        choice={choice}
        versionId={versionId}
        userInput={body}
        recommendation={recommendation}
        onChange={(c, v) => {
          setChoice(c);
          setVersionId(v);
        }}
      />

      {/* 输入框 */}
      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            handleSend();
          }
        }}
        placeholder={t('task.activity.inputPlaceholder')}
        disabled={sending}
        style={{
          minHeight: '60px',
          fontSize: '12px',
          padding: '8px',
          border: '1px solid var(--border-color)',
          borderRadius: '4px',
          resize: 'vertical',
        }}
      />

      {/* 错误提示 */}
      {error && (
        <div style={{ fontSize: '11px', color: '#dc2626' }} data-testid="input-error">
          {error}
        </div>
      )}

      {/* 发送按钮 */}
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button
          type="button"
          onClick={handleSend}
          disabled={sending || !body.trim() || (choice === 'specified' && !versionId.trim())}
          style={{
            padding: '6px 12px',
            fontSize: '12px',
            borderRadius: '4px',
            border: 'none',
            background: 'var(--accent-color)',
            color: '#fff',
            cursor: sending || !body.trim() ? 'not-allowed' : 'pointer',
            opacity: sending || !body.trim() ? 0.5 : 1,
          }}
        >
          {sending ? t('common.sending') : t('common.send')}
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: 修改 TaskDetailPanel**

在 `TaskDetailPanel.tsx`:
```typescript
export function TaskDetailPanel({ detail, agents, onClose, onOpenSession, ... }: TaskDetailPanelProps) {
  const { t } = useI18n();
  const task = detail.task;
  const hasLegacyStages = task.task_stages_json && task.task_stages_json.length > 0;
  
  // 如果是历史任务，显示旧视图
  if (hasLegacyStages) {
    return <LegacyTaskView detail={detail} ... />;
  }
  
  // 新任务：对话式视图
  return (
    <PanelShell
      width={720}
      onClose={onClose}
      title={...}
      headerLeft={...}
      headerRight={...}
    >
      <div style={{ padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: '12px' }}>
        {/* 初始输入 */}
        {task.initial_input && (
          <div style={{ border: '1px dashed var(--border-color)', borderRadius: '8px', padding: '10px', background: 'var(--panel-bg)' }}>
            <span style={{ fontSize: '11px', fontWeight: 800, color: 'var(--text-secondary)' }}>
              {t('task.initialInput')}
            </span>
            <div style={{ fontSize: '12px', marginTop: '6px' }}>{task.initial_input}</div>
          </div>
        )}
        
        {/* 活动流 */}
        <ActivityStream
          taskId={task.id}
          rootId={task.root_id}
          nodeId={nodeId}
          onOpenSession={onOpenSession}
          onViewWorkflowRun={(runId) => {
            // 打开 WorkflowRunDrawer（Task 11 实现）
            setSelectedRunId(runId);
          }}
        />
        
        {/* 新留言输入 */}
        {!isTerminalKanbanTask(task) && (
          <ActivityInput
            taskId={task.id}
            nodeId={nodeId}
            onSuccess={() => {
              // 刷新活动流（由 WS 自动更新，无需手动）
            }}
          />
        )}
      </div>
    </PanelShell>
  );
}
```

- [ ] **Step 3: 写测试**

`web/tests/activity-input.test.mjs`:
```javascript
test('ActivityInput disables send when body is empty', () => {
  const { getByRole } = render(<ActivityInput taskId="task-1" />);
  
  const sendButton = getByRole('button', { name: /发送/ });
  expect(sendButton).toBeDisabled();
});

test('ActivityInput enables send when body has text', async () => {
  const { getByRole, getByPlaceholderText } = render(<ActivityInput taskId="task-1" />);
  
  const textarea = getByPlaceholderText(/输入/);
  fireEvent.change(textarea, { target: { value: 'test message' } });
  
  const sendButton = getByRole('button', { name: /发送/ });
  expect(sendButton).not.toBeDisabled();
});

test('ActivityInput shows error when API fails', async () => {
  global.fetch = vi.fn(() =>
    Promise.resolve({
      ok: false,
      status: 400,
      text: () => Promise.resolve('body cannot be empty'),
    })
  );
  
  const { getByRole, getByPlaceholderText, getByTestId } = render(<ActivityInput taskId="task-1" />);
  
  fireEvent.change(getByPlaceholderText(/输入/), { target: { value: 'test' } });
  fireEvent.click(getByRole('button', { name: /发送/ }));
  
  await waitFor(() => {
    expect(getByTestId('input-error')).toHaveTextContent('body cannot be empty');
  });
});

test('TaskDetailPanel renders ActivityStream for new tasks', () => {
  const detail = {
    task: { id: 'task-1', task_stages_json: null, ... },
    ...
  };
  
  const { getByTestId } = render(<TaskDetailPanel detail={detail} ... />);
  
  expect(getByTestId('activity-stream')).toBeInTheDocument();
  expect(getByTestId('activity-input')).toBeInTheDocument();
});
```

- [ ] **Step 4: 运行测试**

Run: `npm test activity-input.test.mjs`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/components/task/ActivityInput.tsx \
        web/src/components/task/TaskDetailPanel.tsx \
        web/tests/activity-input.test.mjs
git commit -m "feat(ui): activity input and panel integration

- ActivityInput 组件（debounced AI 推荐）
- TaskDetailPanel 切换到对话式视图
- 历史任务保留旧视图
- 完整测试覆盖

Scope: Phase 3 - Task 10"
```

---

## Phase 4: 历史任务适配

**目标**: 兼容现有的阶段式任务

### Task 11: 历史任务视图和总结生成

**Files:**
- Create: `web/src/components/task/LegacyTaskView.tsx`
- Create: `web/src/shared/legacySummary.ts`
- Test: `web/tests/legacy-task-view.test.mjs`

**Interfaces:**
- Consumes: `task.task_stages_json`, `detail.stage_runs`
- Produces: `<LegacyTaskView detail={...} />`

- [ ] **Step 1: 实现总结生成逻辑**

`web/src/shared/legacySummary.ts`:
```typescript
import type { StageRun } from './taskTypes';

export function extractSummaryFromStageRuns(stageRuns: StageRun[]): string {
  if (!stageRuns || stageRuns.length === 0) {
    return "任务已完成（无详细记录）";
  }
  
  // 找最后一个 Agent 阶段的输出
  const lastAgentRun = stageRuns
    .filter((run) => run.role === 'agent' && run.status === 'completed')
    .pop();
  
  if (lastAgentRun && lastAgentRun.output) {
    // 提取前 500 字符作为总结
    const output = lastAgentRun.output.trim();
    if (output.length <= 500) {
      return output;
    }
    return output.substring(0, 500) + '...';
  }
  
  // 降级：统计阶段数
  const completed = stageRuns.filter((r) => r.status === 'completed').length;
  const total = stageRuns.length;
  return `已完成 ${completed}/${total} 个阶段`;
}
```

- [ ] **Step 2: 实现 LegacyTaskView**

`web/src/components/task/LegacyTaskView.tsx` (≤300 行):
```typescript
interface LegacyTaskViewProps {
  detail: TaskDetail;
  agents: AgentStatus[];
  onClose: () => void;
  onOpenSession: (sessionKey: string) => void;
  nodeId?: string;
}

export function LegacyTaskView({
  detail,
  agents,
  onClose,
  onOpenSession,
  nodeId,
}: LegacyTaskViewProps) {
  const { t } = useI18n();
  const [showStages, setShowStages] = useState(false);
  const task = detail.task;
  
  // 生成总结
  const summary = extractSummaryFromStageRuns(detail.stage_runs);
  
  // 构造假的 Activity（用于渲染）
  const syntheticAgentReply: TaskActivity = {
    id: 'legacy-summary',
    task_id: task.id,
    root_id: task.root_id,
    type: 'agent_reply',
    role: 'agent',
    body: '(历史任务)',
    summary: summary,
    status: task.status as any, // 'completed' | 'failed' | ...
    created_at: task.created_at || Date.now(),
    updated_at: task.updated_at || Date.now(),
  };
  
  return (
    <PanelShell
      width={720}
      onClose={onClose}
      title={...}
      headerLeft={...}
      headerRight={...}
    >
      <div style={{ padding: '12px 14px', display: 'flex', flexDirection: 'column', gap: '12px' }}>
        {/* 初始输入 */}
        {task.initial_input && (
          <div style={{ border: '1px dashed var(--border-color)', borderRadius: '8px', padding: '10px' }}>
            <span style={{ fontSize: '11px', fontWeight: 800, color: 'var(--text-secondary)' }}>
              {t('task.initialInput')}
            </span>
            <div style={{ fontSize: '12px', marginTop: '6px' }}>{task.initial_input}</div>
          </div>
        )}
        
        {/* 总结留言 */}
        <AgentReplyCard
          activity={syntheticAgentReply}
          onViewDetails={() => setShowStages(true)}
          onOpenSession={() => {}}
        />
        
        {/* 折叠的阶段列表 */}
        <Collapsible
          title={t('task.legacy.viewStages')}
          subtitle={t('task.legacy.stagesSubtitle')}
          open={showStages}
          onToggle={() => setShowStages(!showStages)}
          data-testid="legacy-stages-collapsible"
        >
          {detail.stages.map((stage, i) => {
            const run = detail.stage_runs[i];
            return (
              <div
                key={i}
                style={{
                  border: '1px solid var(--border-color)',
                  borderRadius: '8px',
                  padding: '10px',
                  marginBottom: '8px',
                  background: 'var(--panel-bg)',
                }}
              >
                <div style={{ fontWeight: 700, marginBottom: '6px' }}>
                  {stage.name || t('task.stageLabel', { index: i + 1 })}
                </div>
                {run && (
                  <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>
                    {statusText(run.status, t)}
                  </div>
                )}
                {run && run.session_key && (
                  <button
                    type="button"
                    onClick={() => onOpenSession(run.session_key)}
                    style={{
                      marginTop: '6px',
                      fontSize: '11px',
                      padding: '4px 8px',
                      borderRadius: '4px',
                      border: '1px solid var(--border-color)',
                    }}
                  >
                    {t('task.openSession', { index: 1 })}
                  </button>
                )}
              </div>
            );
          })}
        </Collapsible>
      </div>
    </PanelShell>
  );
}
```

- [ ] **Step 3: 实现 Collapsible 组件**

`web/src/components/task/Collapsible.tsx` (≤80 行):
```typescript
interface CollapsibleProps {
  title: string;
  subtitle?: string;
  open: boolean;
  onToggle: () => void;
  children: React.ReactNode;
}

export function Collapsible({ title, subtitle, open, onToggle, children }: CollapsibleProps) {
  return (
    <div className="collapsible" data-testid="collapsible" data-open={open || undefined}>
      <button
        type="button"
        onClick={onToggle}
        style={{
          width: '100%',
          padding: '10px',
          border: '1px solid var(--border-color)',
          borderRadius: '8px',
          background: 'var(--panel-bg)',
          textAlign: 'left',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          gap: '8px',
        }}
      >
        <span style={{ fontSize: '16px' }}>{open ? '▼' : '▶'}</span>
        <div style={{ flex: 1 }}>
          <div style={{ fontWeight: 700, fontSize: '12px' }}>{title}</div>
          {subtitle && (
            <div style={{ fontSize: '11px', color: 'var(--text-secondary)' }}>{subtitle}</div>
          )}
        </div>
      </button>
      {open && <div style={{ marginTop: '8px' }}>{children}</div>}
    </div>
  );
}
```

- [ ] **Step 4: 添加 i18n**

```typescript
legacy: {
  viewStages: "查看执行详情（历史阶段视图）",
  stagesSubtitle: "此任务使用旧的阶段模式",
},
```

- [ ] **Step 5: 写测试**

`web/tests/legacy-task-view.test.mjs`:
```javascript
// Review Focus #5: 历史任务无 activities 时的显示
test('LegacyTaskView extracts summary from stage_runs', () => {
  const detail = {
    task: { id: 'task-1', task_stages_json: ['stage1'], status: 'completed' },
    stages: [{ name: 'Stage 1', role: 'agent', prompt_template: 'test' }],
    stage_runs: [
      {
        stage_index: 1,
        role: 'agent',
        status: 'completed',
        output: '已修复 bug 并通过测试',
        session_key: 'session-1',
      },
    ],
  };
  
  const { getByTestId, getByText } = render(<LegacyTaskView detail={detail} ... />);
  
  expect(getByTestId('agent-reply')).toHaveTextContent('已修复 bug 并通过测试');
});

test('LegacyTaskView handles empty stage_runs', () => {
  const detail = {
    task: { id: 'task-1', task_stages_json: ['stage1'], status: 'completed' },
    stages: [],
    stage_runs: [],
  };
  
  const { getByText } = render(<LegacyTaskView detail={detail} ... />);
  
  expect(getByText(/任务已完成（无详细记录）/)).toBeInTheDocument();
});

test('LegacyTaskView shows stages when collapsible is opened', () => {
  const detail = {
    task: { id: 'task-1', task_stages_json: ['stage1'] },
    stages: [{ name: 'Stage 1', role: 'agent' }],
    stage_runs: [],
  };
  
  const { getByTestId, getByText, queryByText } = render(<LegacyTaskView detail={detail} ... />);
  
  // 初始折叠
  expect(queryByText('Stage 1')).not.toBeInTheDocument();
  
  // 点击展开
  fireEvent.click(getByTestId('legacy-stages-collapsible').querySelector('button'));
  
  // 显示阶段
  expect(getByText('Stage 1')).toBeInTheDocument();
});
```

- [ ] **Step 6: 运行测试**

Run: `npm test legacy-task-view.test.mjs`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add web/src/components/task/LegacyTaskView.tsx \
        web/src/components/task/Collapsible.tsx \
        web/src/shared/legacySummary.ts \
        web/tests/legacy-task-view.test.mjs \
        web/src/i18n/locales/zh-CN-tasks.ts
git commit -m "feat(ui): legacy task view with stage collapsing

- extractSummaryFromStageRuns 从历史数据生成总结
- LegacyTaskView 显示总结 + 可折叠的阶段列表
- Collapsible 通用折叠组件
- 空历史数据降级处理

Scope: Phase 4 - Task 11"
```

---

## Phase 5: 优化与抛光

**目标**: 体验优化和测试补全

### Task 12: 端到端测试和文档

**Files:**
- Create: `e2e/test_conversation_task.py`
- Create: `docs/features/conversation-driven-tasks.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: 完整系统
- Produces: E2E 测试 + 用户文档

- [ ] **Step 1: 编写 E2E 测试**

`e2e/test_conversation_task.py`:
```python
def test_conversation_task_full_flow(page, server):
    """完整的对话式任务流程"""
    # 1. 创建任务
    page.goto(f"{server}/mindfs")
    page.click('[data-testid="create-task-button"]')
    page.fill('input[name="task-name"]', "测试对话任务")
    page.fill('textarea[name="initial-input"]', "请实现用户登录功能")
    page.click('button[type="submit"]')
    
    # 2. 等待任务详情面板打开
    page.wait_for_selector('[data-testid="activity-stream"]', timeout=5000)
    
    # 3. 发送留言
    page.fill('[data-testid="activity-input"] textarea', "先实现基本的表单")
    page.click('[data-testid="activity-input"] button[type="submit"]')
    
    # 4. 等待用户留言出现
    user_msg = page.wait_for_selector('[data-testid="user-message"]', timeout=5000)
    assert "先实现基本的表单" in user_msg.text_content()
    
    # 5. 等待 Agent 回复
    agent_reply = page.wait_for_selector('[data-testid="agent-reply"]', timeout=10000)
    assert agent_reply is not None
    
    # 6. 验证状态徽标
    status = agent_reply.get_attribute('data-status')
    assert status in ['pending', 'running', 'completed']
    
    # 7. 如果有 workflow_run，验证"查看详情"按钮存在
    if page.query_selector('[data-testid="agent-reply"] button:has-text("查看执行详情")'):
        page.click('[data-testid="agent-reply"] button:has-text("查看执行详情")')
        # 验证抽屉打开（Task 11 如果实现了）
        # page.wait_for_selector('[data-testid="workflow-run-drawer"]', timeout=2000)

def test_legacy_task_shows_summary(page, server):
    """历史任务显示总结"""
    # 假设有一个历史任务 ID
    legacy_task_id = "task-legacy-1"
    
    page.goto(f"{server}/mindfs")
    # 打开历史任务
    page.click(f'[data-task-id="{legacy_task_id}"]')
    
    # 验证显示 AgentReplyCard（总结）
    agent_reply = page.wait_for_selector('[data-testid="agent-reply"]', timeout=5000)
    assert agent_reply is not None
    
    # 验证折叠区存在
    collapsible = page.query_selector('[data-testid="legacy-stages-collapsible"]')
    assert collapsible is not None
    
    # 点击展开
    collapsible.click()
    
    # 验证阶段列表显示
    # （根据实际历史数据验证）

def test_empty_message_blocked(page, server):
    """空留言被阻止"""
    page.goto(f"{server}/mindfs")
    # 打开一个任务
    page.click('[data-testid="task-card"]:first-child')
    
    # 尝试发送空留言
    send_button = page.query_selector('[data-testid="activity-input"] button[type="submit"]')
    assert send_button.is_disabled()
    
    # 输入文本后启用
    page.fill('[data-testid="activity-input"] textarea', "test")
    assert not send_button.is_disabled()
```

- [ ] **Step 2: 运行 E2E 测试**

Run: `pytest e2e/test_conversation_task.py -v`
Expected: PASS

- [ ] **Step 3: 编写用户文档**

`docs/features/conversation-driven-tasks.md`:
```markdown
# 对话式任务系统

## 概述

MindFS 的任务系统采用对话式交互：你发送留言，Agent 自动选择合适的 Workflow 执行，并将结果作为回复返回。

## 创建任务

1. 点击"新建任务"按钮
2. 输入任务名称和初始描述
3. 任务创建后会打开详情面板

## 与任务对话

### 发送留言

在任务详情面板底部的输入框中输入你的需求，例如：
- "请修复登录功能的 bug"
- "添加单元测试"
- "优化性能"

### Workflow 选择

发送留言时，可以选择 Workflow 模式：

1. **自动选择**（默认）：Agent 根据你的描述自动选择最合适的 Workflow
2. **指定 Workflow**：手动选择要使用的 Workflow
3. **禁用 Workflow**：Agent 直接响应，不使用 Workflow

### 查看进度

Agent 执行过程中，回复卡片会显示实时进度：
- 当前步骤：例如"步骤 2/5: 复现问题"
- 进度条显示整体进度

### 查看详情

每条 Agent 回复都有"查看执行详情"按钮，点击可以：
- 查看 Workflow 的节点树
- 查看每个节点的执行状态
- 跳转到节点内部的会话详情

## 历史任务

旧的阶段式任务会以兼容模式显示：
- 主界面显示任务的总结
- 点击"查看执行详情"可以展开原有的阶段列表

## 快捷键

- `Cmd/Ctrl + Enter`：发送留言
```

- [ ] **Step 4: 更新 CHANGELOG**

在 `CHANGELOG.md` 添加:
```markdown
## [v0.6.0] - 2026-11-XX

### Added

- **对话式任务系统**: 从阶段式重构为对话式交互
  - 用户发送留言，Agent 自动选择 Workflow 执行
  - 实时进度显示
  - Workflow 执行详情抽屉
  - 历史任务兼容视图

- **Workflow 元数据**: 支持描述、使用场景、标签
  - AI 自动推荐 Workflow
  - 手动选择或禁用 Workflow

- **Activity API**: 
  - `POST /api/tasks/{id}/activities` - 创建留言
  - `GET /api/tasks/{id}/activities` - 获取活动流
  - `GET /api/workflows/available` - 获取可用 Workflows
  - `POST /api/workflows/recommend` - AI 推荐

### Changed

- 任务详情面板从阶段列表改为对话流
- Workflow 定位为 Agent 的内部工具

### Migration

历史任务自动以兼容模式显示，无需手动迁移。
新创建的任务使用对话式交互。
```

- [ ] **Step 5: 运行全量测试**

```bash
# Go 测试
go test ./server/... -v

# 前端测试
npm test

# E2E 测试
pytest e2e/ -v

# TypeScript 编译
tsc -b --noEmit

# 构建
make build
```

- [ ] **Step 6: Commit**

```bash
git add e2e/test_conversation_task.py \
        docs/features/conversation-driven-tasks.md \
        CHANGELOG.md
git commit -m "docs: conversation-driven tasks e2e tests and documentation

- E2E 测试覆盖完整流程
- 用户文档
- CHANGELOG 更新

Scope: Phase 5 - Task 12"
```

---

## 总结

本实施计划共 **12 个任务**，分为 **5 个 Phase**：

| Phase | 任务数 | 预计时间 |
|-------|--------|---------|
| Phase 1: Workflow Metadata | 3 | 2 天 |
| Phase 2: Activity 系统 | 4 | 3 天 |
| Phase 3: 前端重构 | 3 | 4 天 |
| Phase 4: 历史任务适配 | 1 | 2 天 |
| Phase 5: 优化抛光 | 1 | 2 天 |
| **总计** | **12** | **13 天** |

### 关键里程碑

- **Phase 1 完成**：Workflow 可以被推荐和选择
- **Phase 2 完成**：对话式执行模型可用
- **Phase 3 完成**：前端对话界面可用
- **Phase 4 完成**：历史任务兼容
- **Phase 5 完成**：生产就绪

### 测试覆盖

每个 Task 都包含：
- 单元测试（Go 和 TypeScript）
- 集成测试（HTTP API）
- E2E 测试（Phase 5）

### 验收标准

完整系统验收：
1. ✅ 可以创建新任务并发送留言
2. ✅ Agent 自动推荐 Workflow
3. ✅ 执行过程中显示实时进度
4. ✅ 可以查看 Workflow 执行详情
5. ✅ 历史任务正常显示
6. ✅ 所有测试通过
7. ✅ 文档完整

---

**计划已完成，等待执行方式选择。**
