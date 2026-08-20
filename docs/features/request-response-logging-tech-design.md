# 请求/响应完整日志记录 — 技术设计与执行计划（P0）

> 范围：**仅"日志记录"这一个需求**（AI 成本雷达 P0 数据采集层）。
> 不含：多维归因、浪费识别、优化建议（P1/P2/P3）。
> 关联需求文档：`docs/features/ai-cost-radar.md`
> 状态：**已实现并合入 `custom`**

---

## 1. 目标与边界

### 1.1 目标
在开关开启时，把每次 relay 调用的**完整请求体 + 完整响应体**（含 prompt 原文）落库，并提供**管理员查看入口**（日志详情弹窗中点击 → JSON 美化展示）。

### 1.2 范围内
- 两张新表：`request_details`（正文）+ `request_media`（媒体二进制）。
- 采集中间件：包装 `c.Writer` 抄录响应，请求结束后异步落库。
- 媒体分离：base64 媒体外置到 `request_media`，正文留可读占位符。
- 全局开关（默认关）、媒体大小上限（默认 ~10MB）、`sha256` 去重。
- 查询接口（仅管理员）+ 清理联动（跟随主日志清理）。
- 前端：日志详情新增「查看完整请求/响应」按钮 + 弹窗。

### 1.3 已确认决策（来自需求评审）
| 项 | 结论 |
|---|---|
| 捕获范围 | 方案 B：所有 relay 请求都抓 |
| 存储方式 | 正文进库 + 媒体二进制独立表（PG `bytea`） |
| 可见范围 | 仅管理员 |
| 默认开关 | 默认关闭 |
| 媒体上限 / 去重 | 单条默认 ~10MB（超限只存占位符）+ `sha256` 去重 |
| 接口鉴权 | 服务端 `AdminAuth()` 强制，非仅前端 |
| TTL | 跟随 `log_cleanup` 系统任务联动清理 |

---

## 2. 现有架构对接点（已核对代码）

| 能力 | 现有实现 | 用法 |
|------|----------|------|
| 请求体读取 | `common.GetBodyStorage(c)` → `BodyStorage`（`common/body_storage.go`、`common/gin.go`） | `Bytes()` 取全量、`Size()`、`IsDisk()`、可 `Seek`；重试间复用 |
| request_id | `main.go:174` 全局 `middleware.RequestId()` | `c.GetString(common.RequestIdKey)`，与 `logs.request_id` 一致 |
| 响应写出 | relay handler 流式写 `c.Writer`；重试循环（`controller/relay.go:190`）仅最终一次写客户端 | 在外层包装 `c.Writer` 抄录"客户端最终看到的字节" |
| relay 路由 | `router/relay-router.go`：`relayV1Router`(httpRouter)、`relayGeminiRouter`、`/pg` 等 | 采集中间件挂这些组 |
| 开关变量 | `common/constants.go`（如 `LogConsumeEnabled`）+ `model/option.go`（`OptionMap` + `updateOptionMap` switch） | 仿此新增 `RequestDetailLogEnabled` 等 |
| 鉴权 | `middleware.AdminAuth()`（`middleware/auth.go:176`），`logRoute` 已全挂（`router/api-router.go:307+`） | 新接口同样挂 |
| 日志清理 | `service/system_task.go` 的 `log_cleanup` 系统任务 | 日志清理后联动删新表 |
| 前端日志页 | `web/src/features/usage-logs`；`DetailsDialog`（`components/dialogs/details-dialog.tsx`） | 在 `DetailsDialog` 内加按钮 |
| JSON 高亮 | `components/ai-elements/code-block.tsx`（基于 `shiki`） | 弹窗内复用 |
| API 封装 | `features/usage-logs/api.ts` + 统一 `@/lib/api`（axios）+ React Query | 新增 `getRequestDetail` |

---

## 3. 数据模型设计

> 遵守跨库规则（SQLite / MySQL / PostgreSQL 同时支持）：用 GORM 抽象，JSON/文本用 `text`，二进制用 `[]byte`，不用 JSONB / 库专属类型。

### 3.1 `request_details`（正文，可读）

```go
type RequestDetail struct {
    Id                int    `json:"id"`
    RequestId         string `json:"request_id" gorm:"type:varchar(64);index:idx_rd_request_id;default:''"`
    UserId            int    `json:"user_id" gorm:"index"`
    CreatedAt         int64  `json:"created_at" gorm:"bigint;index"` // 用于联动清理
    Endpoint          string `json:"endpoint" gorm:"type:varchar(128);default:''"`
    ModelName         string `json:"model_name" gorm:"type:varchar(128);default:''"`
    ChannelId         int    `json:"channel_id" gorm:"default:0"`
    TokenId           int    `json:"token_id" gorm:"default:0"`
    StatusCode        int    `json:"status_code" gorm:"default:0"`
    IsStream          bool   `json:"is_stream" gorm:"default:false"`
    RequestBody       string `json:"request_body" gorm:"type:text"`   // 清洗后（媒体替换为占位符）
    ResponseBody      string `json:"response_body" gorm:"type:text"`  // 清洗后
    RequestTruncated  bool   `json:"request_truncated" gorm:"default:false"`
    ResponseTruncated bool   `json:"response_truncated" gorm:"default:false"`
}
```
- 主键交给 GORM（不手写 AUTO_INCREMENT/SERIAL）。
- 与 `logs` 通过 `request_id` 关联（非外键，软关联）。

### 3.2 `request_media`（媒体二进制，独立表）

```go
type RequestMedia struct {
    Id        int    `json:"id"`
    RequestId string `json:"request_id" gorm:"type:varchar(64);index:idx_rm_request_id;default:''"`
    MediaId   string `json:"media_id" gorm:"type:varchar(64);index;default:''"` // 占位符引用目标
    Sha256    string `json:"sha256" gorm:"type:varchar(64);index:idx_rm_sha256;default:''"` // 去重
    MimeType  string `json:"mime_type" gorm:"type:varchar(128);default:''"`
    Size      int64  `json:"size" gorm:"default:0"`
    Role      string `json:"role" gorm:"type:varchar(16);default:''"` // request / response
    Data      []byte `json:"-" gorm:"type:bytea"` // PG bytea / MySQL longblob / SQLite blob
}
```

> **二进制列跨库类型**：GORM 对 `[]byte` 默认映射 PG `bytea`、MySQL `longblob`、SQLite `blob`。
> 若默认映射不一致，按 `common.UsingPostgreSQL/UsingMySQL/UsingSQLite` 分支设 `gorm:"type:..."`（参考 `model/main.go` 既有跨库写法）。
> **去重**：写入前按 `sha256` 查重，命中则不写二进制、仅复用既有 `media_id`。

### 3.3 迁移注册
- 在 `model/main.go` 的 `migrateDB()` 与 `migrateDBFast()` 的 `AutoMigrate` 列表追加 `&RequestDetail{}, &RequestMedia{}`。

---

## 4. 采集流程设计

### 4.1 中间件 `middleware/request_detail.go`

挂载在 relay 路由组。核心两步：

**(a) 进入时（pre-Next）**：若开关开启且命中目标 endpoint，则把 `c.Writer` 替换为抄录包装器：

```go
type captureWriter struct {
    gin.ResponseWriter
    buf      *bytes.Buffer
    limit    int64
    written  int64
    truncated bool
}
func (w *captureWriter) Write(b []byte) (int, error) {
    if w.written < w.limit {
        n := int64(len(b))
        if w.written+n > w.limit { b2 := b[:w.limit-w.written]; w.buf.Write(b2); w.truncated = true } else { w.buf.Write(b) }
        w.written += n
    } else { w.truncated = true }
    return w.ResponseWriter.Write(b) // 原样转发给客户端
}
func (w *captureWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
// 透传 Flush，保证 SSE 不被破坏
func (w *captureWriter) Flush() { w.ResponseWriter.Flush() }
```
- 必须完整实现 `gin.ResponseWriter` 接口（嵌入即可继承大部分），重点覆写 `Write/WriteString`，并保证 `Flush()`、`Hijack()`（WS）透传。
- `limit` 为响应抄录上限（配置项，默认如 16MB），保护内存；超限置 `truncated`，但**不影响转发给客户端**。

**(b) 离开时（post-Next）**：
1. **同步**取出字节（避免被 `BodyStorageCleanup` 关闭）：
   - 请求体：`storage, _ := common.GetBodyStorage(c)`；`storage.Seek(0,Start)`；`reqBytes, _ := storage.Bytes()`（按上限截断拷贝）。
   - 响应体：`respBytes := captureWriter.buf.Bytes()`（拷贝一份）。
   - 元数据：`request_id / user_id / model / channel / token / status / is_stream / endpoint`（从 `c` 与已有 context key 取）。
2. **异步**（`gopool.Go`）执行：媒体清洗 → 写 `request_media`（去重）→ 写 `request_details`。不阻塞主链路。

> **中间件注册顺序**：在 `router/relay-router.go` 中放在 `BodyStorageCleanup()` **之后** 注册。
> Gin 中后注册者的 post-Next 代码先执行，确保我们读取 body storage 时它尚未被清理关闭；同时 pre-Next 仍在 handler 之前完成 writer 包装。
> 即便如此，仍按上面"同步拷贝字节、异步落库"以彻底规避生命周期问题。

### 4.2 跳过条件（不记录）
- 全局开关关闭。
- 非目标 relay 端点（GET 模型列表等）。
- WebSocket / realtime（`Hijack` 后无常规响应体；P0 可只记录请求，或整体跳过，见执行计划）。
- 可选：响应明显为二进制下载（audio/speech）时仅记录请求 + 响应元数据。

### 4.3 请求体与压缩
- 已有 `DecompressRequestMiddleware`，`GetBodyStorage` 返回的是可重复读取的请求体；采集读取不影响 relay（seek 回 0）。
- 响应抄录的是写给客户端的最终字节（relay 一般不 gzip 响应、SSE 不压缩），即"用户最终看到的内容"。

---

## 5. 媒体清洗器（service 层）

**职责**：把请求/响应正文中的 base64 媒体提取出来，外置到 `request_media`，原位替换为占位符引用。

### 5.1 处理策略（混合）
1. **格式感知**（仅当 `Content-Type` / 正文为 JSON 时）按已知字段路径处理：

| 格式 | base64 字段位置 |
|------|----------------|
| OpenAI chat | `messages[].content[].image_url.url`（`data:<mime>;base64,<...>`）、`messages[].content[].input_audio.data` |
| Claude | `messages[].content[].source.data`（`source.type=="base64"`，含 `media_type`） |
| Gemini | `contents[].parts[].inlineData.data`（含 `mimeType`） |
| 响应（图片生成） | OpenAI images `data[].b64_json`、Gemini inlineData 等 |

2. **正则兜底**：检测超长 base64-like 字符串（如长度 > 阈值且字符集匹配），统一替换。
3. **SSE 响应**：非单一 JSON，P0 直接按文本原样存（媒体罕见）；可选对每行 `data:` 内 JSON 应用同一清洗。

### 5.2 占位符格式
原位 base64 替换为：
```
[media: <mime> · <size> · sha256:<short> · id=<media_id>]
```
- 超过大小上限：替换为 `[media: <mime> · <size> · oversized]`，**不写** `request_media`。
- `media_id`：稳定标识（可用 `sha256` 前缀或独立随机串），供前端按需回查。

### 5.3 去重
- 计算 `sha256(data)`；写 `request_media` 前查 `(sha256)`，命中复用既有记录的 `media_id`、不重复存二进制。

---

## 6. 查询接口（API 契约）

> 全部挂 `middleware.AdminAuth()`，控制器内二次校验管理员（纵深防御）。**不提供 `/self` 变体**。

### 6.1 正文
`GET /api/log/detail?request_id=<id>`
```jsonc
{
  "success": true,
  "data": {
    "request_id": "...",
    "endpoint": "/v1/chat/completions",
    "model_name": "gpt-4o",
    "is_stream": true,
    "status_code": 200,
    "request_body": "…(含占位符的可读文本)…",
    "response_body": "…",
    "request_truncated": false,
    "response_truncated": false,
    "media": [
      { "media_id": "ab12cd", "mime_type": "image/png", "size": 1234567, "role": "request" }
    ]
  }
}
```

### 6.2 媒体（按需）
`GET /api/log/detail/media?media_id=<id>`
- 返回二进制流（`Content-Type` 为存储的 `mime_type`），或 base64 包装的 JSON（前端取舍）。
- 同样 `AdminAuth()`。

### 6.3 路由注册（`router/api-router.go`，logRoute 组）
```go
logRoute.GET("/detail", middleware.AdminAuth(), controller.GetRequestDetail)
logRoute.GET("/detail/media", middleware.AdminAuth(), controller.GetRequestMedia)
```

---

## 7. 清理联动

在 `service/system_task.go` 的 `log_cleanup` 任务中按同一 `target_timestamp` 联动：
- `model.DeleteOldRequestDetailBatch(ctx, targetTimestamp, limit)`：分批删除 `request_details` 与 `request_media`（`created_at < target_timestamp`）。
- 与 `DeleteOldLogBatch` 使用相同的批量大小和取消上下文。

> P0 不引入独立自动 TTL；如需自动化，后续可加定时任务，不在本次范围。

---

## 8. 配置项

| 配置 | 位置 | 默认 | 说明 |
|------|------|------|------|
| `RequestDetailLogEnabled` | `common/constants.go` + `model/option.go` | `false` | 总开关 |
| `RequestDetailMediaMaxBytes` | 同上 | ~10MB | 单条媒体上限，超限只存占位符 |
| `RequestDetailMaxBytes` | 同上 | 1MB | 每侧正文/响应抄录上限，超限截断标记 |

- `model/option.go`：在 `InitOptionMap` 写入 `OptionMap`，并在 `updateOptionMap` 的 `switch` 增加对应 `case` 解析回写全局变量（参考 `LogConsumeEnabled`）。
- 前端运营设置页加开关与上限输入（含数据量/隐私提示文案，走 i18n）。

---

## 9. 前端实现

### 9.1 入口按钮
- 在 `features/usage-logs/components/dialogs/details-dialog.tsx` 的概览区（已展示 `request_id` 处）下方加「查看完整请求/响应」按钮。
- **显示条件**：`props.isAdmin && !!props.log.request_id`（无 `request_id` 不显示，避免死按钮）。是否进一步依赖"开关开启"由后端返回空数据时前端给"未记录"提示。

### 9.2 新弹窗 `components/dialogs/request-payload-dialog.tsx`
- 复用 `@/components/dialog` 的 `Dialog` + `ScrollArea`。
- 点击**懒加载**：React Query `useQuery(['request-detail', requestId], () => getRequestDetail(requestId), { enabled })`。
- 请求/响应 **Tab 切换**；JSON 正文用 `components/ai-elements/code-block.tsx`（`language="json"`）美化高亮；非 JSON（multipart/SSE）用 `<pre>`。
- 复制（`use-copy-to-clipboard`）、加载/错误态（`handleServerError`）。
- 媒体占位符：解析 `id=<media_id>`，渲染为可点击项，点击拉 `/api/log/detail/media` 预览（图片 inline，其余下载/提示）。

### 9.3 API（`features/usage-logs/api.ts`）
```ts
export const getRequestDetail = (requestId: string) =>
  api.get(`/api/log/detail?request_id=${encodeURIComponent(requestId)}`).then(r => r.data)
```
- 类型补充到 `types.ts`；文案走 i18n（同步 zh/en 等）。

### 9.4 经典前端
- `web/classic/` 视情况同步（可后置，非阻塞 P0 后端）。

---

## 10. 性能与安全

- **性能**：抄录在内存缓冲（有上限）；落库走 `gopool.Go` 异步；正文/列表查询**禁止 `SELECT *` 带 `data` 列**（PG TOAST 已外置大字段，独立表进一步隔离）。
- **内存**：响应抄录有 `RequestDetailMaxBytes` 上限；媒体外置后正文小。
- **安全**：完整正文/媒体含敏感 prompt → 路由 `AdminAuth()` + 控制器二次校验；无 `/self`；验收须用非管理员 token 验证 403。
- **SSE 兼容**：包装器透传 `Flush()`/`Hijack()`，充分测试流式不被破坏。

---

## 11. 三库兼容清单

- `text` 列：三库通用（正文）。
- 二进制列：`[]byte` → `bytea`/`longblob`/`blob`，按 DB 分支验证。
- 不使用 JSONB、`@>`、`GROUP_CONCAT` 等库专属能力。
- 迁移走 `AutoMigrate`；SQLite 不用 `ALTER COLUMN`。
- 三库分别跑通迁移、写入、查询、清理。

---

## 12. 执行计划（按依赖顺序）

> 建议先后端地基跑通，再接前端。每步可独立验证。

### 阶段一：后端数据层
1. **新增模型** `model/request_detail.go`：`RequestDetail`、`RequestMedia` 结构 + `DeleteOldRequestDetailBatch`。
2. **迁移注册**：`model/main.go` 两处 `AutoMigrate` 追加。
3. **三库迁移验证**：SQLite/MySQL/PG 各跑一次建表（重点验二进制列类型）。

### 阶段二：开关与配置
4. `common/constants.go` 增 `RequestDetailLogEnabled` / `RequestDetailMediaMaxBytes` / `RequestDetailMaxBytes`。
5. `model/option.go`：`OptionMap` 写入 + `switch` 解析回写。

### 阶段三：媒体清洗器
6. `service/request_detail_sanitizer.go`：格式感知 + 正则兜底 + 占位符 + sha256 + size cap；附单元测试（OpenAI/Claude/Gemini 各一例 + 兜底）。

### 阶段四：采集中间件
7. `middleware/request_detail.go`：`captureWriter` + 落库逻辑（同步拷贝 + 异步写）。
8. `router/relay-router.go`：在 `BodyStorageCleanup` 之后挂载到 relay 组。
9. 验证：开关关闭=零写库；开启=文本/多模态请求正确落库；SSE 与非流式都正确抄录。

### 阶段五：查询接口与清理
10. `controller/request_detail.go`：`GetRequestDetail`、`GetRequestMedia`（管理员二次校验）。
11. `router/api-router.go`：注册两接口（挂 `AdminAuth()`）。
12. `service/system_task.go` 的 `log_cleanup`：联动清理。
13. 验证：管理员可查回；**非管理员 token 直接调返回 403**；清理联动生效。

### 阶段六：前端
14. `api.ts` + `types.ts`：`getRequestDetail` 与类型。
15. `components/dialogs/request-payload-dialog.tsx`：弹窗（Tab + CodeBlock + 媒体）。
16. `details-dialog.tsx`：加按钮（`isAdmin && request_id`）。
17. 运营设置页：开关 + 上限输入 + i18n。
18. `bun run typecheck` / `lint` / `format` 通过。

### 阶段七：联调与回归
19. 三库回归；开关默认关回归（行为与现状一致）；大正文/多模态/去重/截断/清理全链路验收。

---

## 13. 测试与验收清单

- [ ] 开关关闭：无任何额外写库，relay 行为与现状一致。
- [ ] 开关开启：请求/响应正确落库，按 `request_id` 查回。
- [ ] 多模态请求：正文中 prompt 可读，base64 替换为占位符；媒体写入 `request_media` 可经 `media_id` 回查；`sha256` 去重生效。
- [ ] 响应含 base64（图片生成）：同样外置。
- [ ] SSE 流式与非流式均正确抄录，且流式未被破坏（Flush 正常）。
- [ ] 超大正文/媒体：截断标记 / `oversized` 占位，二进制不入库。
- [ ] 接口鉴权：非管理员 token 调 `/api/log/detail` 及媒体接口返回 403（服务端生效，非仅前端隐藏）。
- [x] 清理：`log_cleanup` 系统任务联动删除 `request_details` + `request_media`。
- [ ] 三库（SQLite/MySQL/PG）迁移与读写全部通过。
- [ ] 前端：按钮按条件出现，弹窗懒加载、JSON 高亮、Tab 切换、复制、加载/错误态、媒体预览均正常。
- [ ] 前端 typecheck/lint/format 通过。

---

## 14. 风险与对策

| 风险 | 对策 |
|------|------|
| 包装 Writer 破坏 SSE | 完整实现 `gin.ResponseWriter`，透传 `Flush`/`Hijack`；流式专项测试 |
| body storage 生命周期 | 同步拷贝字节后再异步落库；中间件注册在 `BodyStorageCleanup` 之后 |
| 大响应内存占用 | `RequestDetailMaxBytes` 上限 + 截断标记 |
| 二进制跨库差异 | `[]byte` 映射 + 按 DB 分支；三库验证 |
| 存储膨胀 | 默认关 + 媒体去重 + 大小上限 + 清理联动 |
| 敏感数据泄露 | `AdminAuth()` 服务端强制 + 无 `/self` + 控制器二次校验 |
