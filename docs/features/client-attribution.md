# 客户端来源识别（Client Attribution）— 设计文档

> 状态：**设计草案，待确认**
> 所属：「AI 成本雷达」路线图的补充能力。依赖已落地的「成本归因」（`cost-attribution.md`）。

---

## 1. 背景与目标

识别每条 API 请求来自哪个客户端工具（Claude Code、Codex CLI、Cursor、Cline、LobeChat 等），落库并在日志/成本归因中展示，支撑：

- **成本归因**：团队共用一个 API Key 时，看清每笔消费来自哪个工具。
- **用量统计**：了解团队中各 AI 编程工具的实际使用占比。
- **可观测性**：快速定位某客户端产生的异常流量/报错。

> 安全审计场景：UA/Header 可伪造，**仅作参考、不作强判定**。

---

## 2. 范围

### 2.1 P1（In Scope，本期）
- 基于 **User-Agent + 少量辅助 Header** 的启发式识别。
- `logs` 表新增 `client` 列（归一化 slug，如 `claude-code`）。
- 使用日志表新增「客户端」列。
- 成本归因新增 **「客户端」维度**（复用现有 `attributionColumns` 机制）。
- 历史日志无 client → 显示「未知」。

### 2.2 P2（Out of Scope，后续迭代）
- 解析请求体 `client_metadata` 字段（如 Codex）做更精准识别。
  - 需在请求解析阶段缓冲 body；各 provider 格式不一；部分端点会拒收 `client_metadata`，逻辑易碎，单独迭代。

---

## 3. 识别规则（按优先级匹配，命中即停）

| 客户端 | 标识方式 | 匹配示例（UA / Header） |
|--------|----------|--------------------------|
| Claude Code | UA 含 `claude-code` | `claude-code/2.1.89 (cli)` |
| Codex CLI | UA 含 `codex`（P2 再补 body `client_metadata`） | `codex-cli`、`codex_vscode` |
| Cline | UA 含 `cline` | `Cline/x.x.x` |
| Cursor | UA 多为通用 `axios` + 辅助 Header `x-cursor-*` | UA=axios 且含 `x-cursor-*` |
| LobeChat | UA 含 `lobe-chat` / `lobehat` | `lobe-chat/x` |
| OpenAI SDK | UA 含 `openai-python` / `openai-node` | 归一为 `openai-sdk` |
| 其他 | 兜底 | `other`（保留原始 UA 截断存入 `Other` JSON 备查） |

- 归一化输出为小写 slug；前端用固定 map → 展示名 + 图标。
- 正则在包级常量预编译（避免每请求新建）。

> **准确率说明（重要）**：new-api 看到的是直连客户端的 UA，这正是目标；但走通用 SDK 的工具（UA 即 SDK 名）无法区分到具体工具，归 `openai-sdk`/`other`。Cursor 等需辅助 Header。覆盖率因工具而异，需管理预期。

---

## 4. 数据模型

`model/log.go` 的 `Log` 新增字段：

```go
Client string `json:"client" gorm:"type:varchar(32);index;default:''"`
```

- 迁移：依赖 GORM `AutoMigrate` 自动加列（SQLite/MySQL/PG 三库兼容，SQLite 走 ADD COLUMN）。
- 不污染现有 `Other` JSON（dashboard 需 `GROUP BY`，独立列才能跨库聚合）。

---

## 5. 后端改动

1. `service/client_detect.go`（新增）：`DetectClient(c *gin.Context) string` —— 读 UA + 选定 Header，表驱动返回 slug；纯函数、可单测。
2. `model/log.go`：
   - `Log` 加 `Client` 字段。
   - `RecordConsumeLog` / `RecordErrorLog` 内 `log.Client = service.DetectClient(c)`（已持有 `c`，与 `Ip` 同理；P1 无需新中间件）。
   - `attributionColumns` 加 `case "client": return "client", "client"`。
   - `GetAllLogs` 增加可选 `client` 过滤（与 `model_name` 等同构）。
3. `controller/log.go` / DTO：日志列表与归因接口透传 `client`（归因 `parseAttributionFilter` 已通用，仅需维度枚举允许 `client`）。

> P2 时引入 relay 路由级中间件，在请求解析期 peek body 写入 `context`，`DetectClient` 改为优先读 context。

---

## 6. 前端改动（`web/src`）

- 使用日志表：新增「客户端」列（来源于日志列表返回的 `client`，固定 map 渲染展示名/图标，空值显示「未知」）。
- 成本归因：`AttributionDimension` 加 `'client'`；维度 Tab 加「客户端」；`useDimensionLabels` 加文案。
- i18n：6 语言新增「客户端」「未知」及各工具展示名（展示名可不翻译，保留品牌名）。

---

## 7. 开关与隐私

- 识别仅依赖请求头，非强 PII；默认开启。
- 可选：复用系统设置做开关 `ClientDetectEnabled`（默认 true），关掉则不写 `client`。

---

## 8. 局限

1. UA 启发式，SDK 会掩盖真实工具；认不出归 `other`。
2. UA/Header 可伪造，不用于安全强判定。
3. 历史日志无回填，显示「未知」。

---

## 9. 涉及文件

- 后端：`model/log.go`、`service/client_detect.go`(新)、`controller/log.go`、`router`（仅 P2 加中间件）。
- 前端：`features/usage-logs`（表列）、`features/dashboard`（归因维度 + i18n）。
- 文档：本文件。

---

## 10. 验收标准（P1）

- [ ] 已知工具（Claude Code / Cline / LobeChat / Codex CLI）发请求后，日志 `client` 列正确标注；未知工具归 `other`/「未知」。
- [ ] 使用日志表出现「客户端」列。
- [ ] 成本归因「客户端」维度排行/趋势/下钻可用，与既有维度行为一致。
- [ ] 三库（SQLite/MySQL/PG）AutoMigrate 加列成功，聚合正确。
- [ ] `DetectClient` 有单测覆盖典型 UA。
- [ ] 文案走 i18n。
