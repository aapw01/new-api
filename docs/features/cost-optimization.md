# 成本优化建议（P2/P3）— 设计文档

> 状态：**待实现**（草案，默认范围见 §3，可调整）
> 所属：「AI 成本雷达」路线图的 **P2 浪费识别 + P3 优化建议引擎**。前置 P0「完整日志」、P1「多维归因」已完成，分别见 `ai-cost-radar.md` / `cost-attribution.md`。

---

## 1. 背景与定位

「AI 成本雷达」的北极星能力：**标出"高成本低价值调用"，并给出替代模型 / 缓存 / 压缩 prompt 的建议**。

P1 回答了"**谁/哪个 key/哪个模型在烧 token**"，但它只做归因、不下判断。P2/P3 在归因之上再进一步：**主动识别浪费模式，并量化"预计可省多少"，给出可执行建议**。

### 1.1 核心理念：不度量"价值"，而是检测"浪费模式"

一次调用"值不值"是主观的，系统无法直接判断。因此本功能**不试图度量价值**，而是：

1. 检测一组**可量化的浪费模式**（如"大输入小输出"、"杀鸡用牛刀"）。
2. 对每条命中项估算**预计可省 quota**。
3. 把"高成本（quota 高）+ 命中浪费模式"的调用按预计可省金额排序，浮到最上面。
4. 判断权交给管理员——系统只负责"发现可疑、给出建议、算清省钱空间"。

### 1.2 关键事实（代码基线）

P1 已证明：每条 consume 日志（`logs` 表）现成可用以下字段，**P2 聚合规则零新增表、零依赖 P0 正文**：

- 结构化：`quota` / `prompt_tokens` / `completion_tokens` / `use_time` / `is_stream` / `model_name` / `username` / `token_id` / `token_name` / `channel_id` / `group` / `created_at` / `type`。
- `Other` JSON（见 `service/log_info_generate.go`）：`model_ratio` / `completion_ratio` / `cache_tokens` / `cache_ratio` / `model_price` 等。
- 全局倍率表（`setting/ratio_setting`）：`GetModelRatioCopy()` / `GetCompletionRatioCopy()` / `GetModelPriceCopy()`，可精确重算"换成另一模型同样 token 花多少"。

**精确请求去重 / 基于正文的 prompt 压缩**需要 P0 已落地的请求正文数据，归入 P2 增强档（§3.3）。

---

## 2. 目标

- 管理员在一个看板里看到：**本周期预计可省 ¥X**，以及按"浪费模式"分类的命中清单。
- 每条命中给出：涉及的用户/令牌/模型、当前花费、**预计可省金额**、**具体建议**（替代模型 / 开启缓存 / 压缩 prompt）。
- 建议可解释、可量化、不夸大——明确标注"建议，不保证质量"。

---

## 3. 范围

### 3.1 默认范围内（P1 实施档 — 纯聚合，当前分支即可）

**放置位置**：数据看板新增 Tab「成本优化建议」，紧邻「成本归因」，仅管理员可见。复用 P1 的时间预设 + AdminAuth 基建。

**三条核心规则**（全部基于聚合，不需要 prompt 正文）：

| # | 浪费模式 | 检测信号 | 建议类型 | 预计可省估算 |
|---|---|---|---|---|
| R1 | **大输入小输出** | `prompt_tokens` 大（默认 >4000）且 `completion_tokens` 极小（默认 <50） | 压缩 prompt / 缓存系统提示 | `(prompt_tokens − 目标阈值) × model_ratio × group_ratio` 折算 quota |
| R2 | **杀鸡用牛刀** | 高倍率模型（按 ratio 排序的 Top 档）+ 单次 token 量小 / 输出短 | 降级到同族更便宜模型 | 用分级表候选模型倍率重算差额（见 §5） |
| R3 | **缓存未利用** | 支持缓存的模型 `cache_tokens=0` 但 `prompt_tokens` 大且同签名重复出现 | 开启 prompt caching | `重复 prompt_tokens × (1 − cache_ratio) × ratio` |

**汇总**：顶部大卡显示三类规则合计"预计可省 quota / 折算货币"，每类规则一张卡片，可展开看 Top-N 命中项（按预计可省降序）。

### 3.2 默认范围外（本期不做）

- "价值"打分 / 业务价值标签（无可靠数据来源）。
- 自动改写 prompt、自动切换模型（仅给建议，不自动执行）。

### 3.3 增强档（P2 +，需合并 P0 正文日志）

- **R4 精确请求去重**：基于请求正文 SHA256，识别真正重复调用 → 缓存建议（当前分支只能用 token 数签名近似）。
- **R5 真·prompt 压缩建议**：基于正文长度、重复 system prompt 抽取，给出"可裁剪 N token"。

---

## 4. 评分与排序

- 每条命中的排序键 = **预计可省 quota**（estimated_saving），降序。
- 这自然同时满足"高成本"（saving 通常正比于 quota）与"有改进空间"（命中了某条规则）。
- 看板默认只展示每类规则 Top-N（沿用 P1 的 `attributionMaxTop=200` 硬上限思路），并强制时间窗下界兜底（默认 30 天），避免全表扫描。

---

## 5. 替代模型建议（R2 关键设计）

倍率重算是精确的，**难点是"哪些模型是同档替代"**——需要一份**可配置的模型能力分级表**（管理员可编辑 JSON，放系统设置）：

```jsonc
// 示例：每组从强到弱，建议在同组内向右降级
{
  "claude":  ["claude-opus-*", "claude-sonnet-*", "claude-haiku-*"],
  "openai":  ["gpt-5", "gpt-5-mini", "gpt-4o-mini"],
  "gemini":  ["gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite"]
}
```

算法：
1. 对命中的 `(model, group)` 取平均 `prompt_tokens` / `completion_tokens`。
2. 在分级表中找到该 model 所在组的**更低档候选**。
3. 用候选模型的 `model_ratio` + `completion_ratio`（`GetModelRatioCopy` / `GetCompletionRatioCopy`）按相同 token 量重算 quota。
4. 输出"迁移到 X：预计省 Y%（Z quota）"。

**强约束**：质量不保证，UI 必须标注"仅供参考，需自行评估效果"。若分级表未配置该模型，则跳过 R2，不臆测。

---

## 6. 技术设计

### 6.1 后端

- `model/log.go`（或新文件 `model/log_optimization.go`）：
  - 复用 P1 的 `attributionBase` 时间窗 / 过滤 / 兜底逻辑。
  - 每条规则一个聚合查询，返回命中群组 + 累计 quota + 平均 token + 预计可省。
  - 跨库兼容（SQLite / MySQL / PG），沿用 P1 的列引用与类型处理约定。
- `setting/`：模型分级表（admin 可编辑 JSON，含校验）。
- `controller/log.go` + `router/api-router.go`：新增 `GET /api/log/optimization`（`middleware.AdminAuth()`），按规则类型返回 findings。

### 6.2 前端（`web/src/features/dashboard`）

- `section-registry.tsx`：注册 `optimization` section（`adminOnly`，加入 `ADMIN_ONLY_SECTIONS`）。
- `index.tsx`：懒加载 + `&& isAdmin` 门控渲染分支。
- 新组件 `components/optimization/optimization-panel.tsx`：顶部 SummaryCard（预计可省）+ 每规则一张可展开卡片 + Top-N 命中表 + 建议文案。
- `types.ts` / `api.ts`：新增 findings 类型与 API 客户端。
- i18n：所有文案走 `t()`，补齐 en/zh/fr/ja/ru/vi。

### 6.3 默认参数（可配）

- R1 阈值：`prompt_tokens > 4000` 且 `completion_tokens < 50`。
- R2 "高倍率"：按当前倍率表取 Top 档，或固定倍率阈值。
- 时间窗：默认 30 天，Top-N 上限 200。

---

## 7. 局限与风险

- 聚合规则是**启发式**，会有误报；UI 用语需保守（"疑似""预计""建议"）。
- 省钱估算是**理论上界**，实际取决于业务是否接受降级/压缩/缓存。
- R2 依赖人工维护的分级表，覆盖不全则建议缺失——可接受，宁缺毋滥。
- R4/R5 的精度依赖 P0 正文日志，未合并时只能近似或不提供。

---

## 8. 验收标准（P1 实施档）

1. 看板出现「成本优化建议」Tab，仅管理员可见。
2. 三条规则各能在测试数据上正确命中并给出预计可省。
3. R2 在配置了分级表时给出候选模型与省钱比例；未配置时安全跳过。
4. 所有查询有时间窗下界 + Top-N 上限；跨三库通过。
5. 后端单测覆盖三条规则的命中与估算；前端 typecheck + eslint 通过。
