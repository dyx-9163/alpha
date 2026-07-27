# AIFAR 登录后功能培训手册 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 生成一份面向日常操作员和实施运维工程师、仅覆盖登录后功能、包含当前真实界面截图的中文 Word 培训手册。

**Architecture:** 先从当前源码和页面建立功能事实清单，再启动本地 AIFAR 采集脱敏截图。使用可复现的文档构建脚本把结构化正文、截图、提示框和速查表生成 DOCX，最后通过逐页渲染、视觉检查和结构检查闭环验收。

**Tech Stack:** AIFAR Vue 3 前端与 Go 后端、本地浏览器截图、Python `python-docx`/OOXML、文档技能自带 `render_docx.py`、Poppler/LibreOffice 渲染链路。

## Global Constraints

- 仅讲登录后的产品功能，不包含安装、启动、停止、升级、备份、恢复、构建、打包和源码开发。
- 同时服务日常操作员和实施运维工程师，正文区分“操作员必学”和“运维进阶”。
- 截图来自当前分支真实页面，不伪造现场成功状态，不包含密码、私钥、Token、完整敏感连接信息或真实生产数据。
- 每个关键流程使用“目的—适用角色—前提条件—操作步骤—预期结果—风险提示”。
- Storage 的 bucket、object、user、access-key、replica 明确为控制面记录。
- Runtime 的停止任务、服务下线和“全部重启（读取新配置）”按当前实际语义分别说明。
- Word 文档使用 A4 纵向企业培训手册版式，并通过逐页 PNG 渲染进行视觉复核。

---

### Task 1: 建立当前功能事实与页面取证清单

**Files:**
- Read: `web/src/router/index.ts`
- Read: `web/src/App.vue`
- Read: `web/src/views/*.vue`
- Read: `web/src/components/*.vue`
- Read: `web/src/i18n/messages.ts`
- Read: `backend/internal/httpapi/api.go`
- Read: `memory.md`
- Create: `.tmp/aifar-training/content-facts.md`

**Interfaces:**
- Consumes: 已确认设计 `docs/superpowers/specs/2026-07-27-aifar-post-login-training-manual-design.md`。
- Produces: 按菜单和任务场景组织的事实清单、风险边界和截图列表，供 Task 2 与 Task 3 使用。

- [ ] **Step 1: 列出当前登录后路由和侧边导航**

  从路由、App 布局和中文 i18n 中核对 Dashboard、服务器、应用商店、容器、数据库、Nacos、存储、凭据、终端、任务、工具箱、审计和设置的当前名称及权限限制。

- [ ] **Step 2: 核对关键交互和状态语义**

  阅读对应页面及后端路由，记录服务器探测、应用部署、实例检测/删除、容器操作、Runtime 扩缩容/下线/重启、任务取消、终端多会话和审计的入口、前提、异步任务行为及预期结果。

- [ ] **Step 3: 写入事实清单**

  在 `.tmp/aifar-training/content-facts.md` 中固定四个部分：菜单说明、日常主线、运维场景、风险与能力边界。每条事实注明对应源码文件，避免依据旧文档推测。

- [ ] **Step 4: 检查事实清单完整性**

  运行：

  ```powershell
  rg -n "Dashboard|服务器|应用商店|容器|Runtime|数据库|Nacos|存储|凭据|终端|任务|工具箱|审计|设置" .tmp/aifar-training/content-facts.md
  ```

  预期：全部主要菜单至少出现一次，且 Runtime、Storage、终端和删除/卸载边界均有独立说明。

### Task 2: 启动本地环境并采集脱敏真实截图

**Files:**
- Read: `package.json`
- Read: `scripts/dev.mjs`
- Create: `.tmp/aifar-training/screenshots/*.png`
- Create: `.tmp/aifar-training/screenshot-index.md`

**Interfaces:**
- Consumes: Task 1 的页面清单和截图目标。
- Produces: 统一分辨率的当前真实界面截图和图注索引，供 Task 4 文档构建使用。

- [ ] **Step 1: 启动当前本地开发环境**

  使用仓库脚本启动后端和前端，确认浏览器能打开登录页，API 登录可用，并记录实际访问地址。

- [ ] **Step 2: 使用本地测试账号登录并核对权限**

  只使用本机开发环境的测试账号；不连接真实服务器，不输入生产凭据。确认目标菜单均可见，若权限隐藏页面则按当前权限模型记录而不绕过鉴权。

- [ ] **Step 3: 采集核心页面截图**

  以统一浏览器视口采集：界面总览、仪表盘、服务器、应用商店、容器/Runtime、数据库、Nacos、存储、凭据、终端多会话、任务详情、工具箱、审计和设置。优先记录关键操作入口；依赖真实目标环境的区域保留真实空状态或配置状态。

- [ ] **Step 4: 做敏感信息检查和截图索引**

  对每张截图检查用户名以外的敏感字段、地址、Token、密码、密钥和业务数据。将文件名、页面、用途、环境前提及计划插入章节写入 `.tmp/aifar-training/screenshot-index.md`。

- [ ] **Step 5: 检查截图可读性**

  逐张以原始尺寸打开，确认没有遮挡、弹窗截断、浏览器调试元素或过小文字；不合格截图重新采集。

### Task 3: 编写结构化中文正文

**Files:**
- Create: `.tmp/aifar-training/AIFAR登录后功能培训手册.md`
- Read: `.tmp/aifar-training/content-facts.md`
- Read: `.tmp/aifar-training/screenshot-index.md`

**Interfaces:**
- Consumes: Task 1 的事实清单和 Task 2 的截图索引。
- Produces: 完整、无占位符、可直接排版的中文正文。

- [ ] **Step 1: 编写开篇与学习路径**

  完成封面信息、适用对象、范围说明、学习目标、角色分工、安全原则、界面总览和状态/权限认知。

- [ ] **Step 2: 编写日常操作主线**

  按服务器接入与探测、应用部署、任务跟踪、结果确认编写完整流程；每个流程包含目的、角色、前提、步骤、预期结果和风险提示。

- [ ] **Step 3: 编写全部功能模块说明**

  覆盖仪表盘、服务器、应用商店、容器、Runtime、数据库、Nacos、存储、凭据、终端、任务、工具箱、审计和设置；为对应页面插入明确的截图引用标记。

- [ ] **Step 4: 编写运维场景与故障定位**

  覆盖状态核对、日志查看、Runtime 扩缩容/下线/全部重启、取消任务、实例检测与删除、终端连接，以及“先看任务步骤和目标日志，再判断远程环境”的排障顺序。

- [ ] **Step 5: 编写练习、验收与速查**

  提供一条不执行破坏性变更的综合练习、讲师验收清单、状态含义速查和菜单入口速查。

- [ ] **Step 6: 执行正文审校**

  搜索并消除占位符、源码口吻和越界章节；复核所有风险操作均有提示，所有主要菜单均有说明，所有截图引用均能在截图索引中找到。

### Task 4: 生成可编辑 Word 培训手册

**Files:**
- Create: `.tmp/aifar-training/build_training_manual.py`
- Create: `docs/AIFAR登录后功能培训手册.docx`
- Read: `.tmp/aifar-training/AIFAR登录后功能培训手册.md`
- Read: `.tmp/aifar-training/screenshots/*.png`

**Interfaces:**
- Consumes: Task 3 的正文、Task 2 的截图和文档设计令牌。
- Produces: A4 纵向、包含真实截图、可编辑的最终 DOCX。

- [ ] **Step 1: 固定文档设计令牌**

  按文档技能的设计预设明确页面边距、中文字体、标题层级、段落间距、列表缩进、表格宽度、警告/进阶/边界提示框颜色、页眉页脚和图片最大宽度，不依赖 Word 默认格式。

- [ ] **Step 2: 编写可复现的构建脚本**

  使用工作区依赖提供的 Python 与 `python-docx`，创建真实标题样式、编号列表、表格几何、页眉页脚、图题、提示框和分页控制；正文和截图文件路径均为显式输入。

- [ ] **Step 3: 生成 DOCX**

  运行构建脚本生成 `docs/AIFAR登录后功能培训手册.docx`，确认文件存在且非空。

- [ ] **Step 4: 执行结构检查**

  用 `python-docx` 读取文档，检查章节标题、表格、图片、页眉页脚和页码字段存在；确认文档正文不含密码、Token、私钥、完整连接串和占位符。

### Task 5: 渲染、逐页检查并修订

**Files:**
- Read: `docs/AIFAR登录后功能培训手册.docx`
- Create: `.tmp/aifar-training/rendered/page-*.png`
- Modify: `.tmp/aifar-training/build_training_manual.py`
- Modify: `docs/AIFAR登录后功能培训手册.docx`

**Interfaces:**
- Consumes: Task 4 的 DOCX。
- Produces: 通过视觉和结构验收的最终 DOCX。

- [ ] **Step 1: 渲染全部页面**

  使用文档技能自带 `render_docx.py` 将 DOCX 渲染为逐页 PNG；若工具支持，同时生成内部检查用 PDF。

- [ ] **Step 2: 逐页打开并检查**

  在 100% 视图检查标题孤行、图片过小或拉伸、图片/图题分离、表格溢出、文字遮挡、提示框断裂、页眉页脚错位、页码缺失和大块无意义空白。

- [ ] **Step 3: 修订并重新渲染**

  对发现的问题修改构建脚本后重新生成 DOCX，再次渲染全部页面。重复执行，直到所有页面无明显排版问题。

- [ ] **Step 4: 最终内容核验**

  对照设计说明逐项检查：双角色、登录后范围、真实截图、全部主要菜单、日常主线、运维场景、风险边界、练习、验收和速查均已覆盖。

### Task 6: 收口交付与项目记忆

**Files:**
- Modify: `memory.md`
- Final: `docs/AIFAR登录后功能培训手册.docx`

**Interfaces:**
- Consumes: Task 5 验收通过的 DOCX 和本轮验证记录。
- Produces: 可下载的最终手册链接，以及精简的项目记忆记录。

- [ ] **Step 1: 追加项目记忆**

  在 `memory.md` 的 `2026-07-27` 章节追加本轮问题、最终手册路径、覆盖范围、截图来源和视觉检查结论，不记录任何凭据或长日志。

- [ ] **Step 2: 核对工作区变更**

  确认最终交付物已生成，既有 `memory.md` 修改得到保留，不把临时截图、渲染页或本地凭据作为交付物。

- [ ] **Step 3: 交付**

  向用户提供 `docs/AIFAR登录后功能培训手册.docx` 的可点击绝对路径，并简要说明覆盖内容、页数、截图数量和视觉验证状态。

