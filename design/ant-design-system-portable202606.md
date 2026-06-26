# Ant Design 设计系统规范（通用版）

> 本文件为 Ant Design v6 完整量化设计规范的独立打包版本，可直接用于 Codex、Cursor、Claude 等 AI 工具。
> 将此文件放入项目根目录或作为 system prompt 的一部分即可生效。

<!--  作者：Charles.Chen 微信：yixiusztx  如有问题，欢迎指正，谢谢。  -->

---

## 使用说明

本规范用于指导 AI 生成基于 Ant Design 的前端页面原型图。

### 工作流程

1. 读取下方「品牌配置」章节，获取当前项目的品牌参数
2. 品牌配置中的值覆盖 Design Token 默认值
3. 间距、圆角、字号等使用「全局 Design Token」中的标准值
4. 具体组件样式参考「组件级设计参数」
5. 页面整体布局参考「B 端页面布局指南」

### 关键约束

- 所有间距必须是 4px 的倍数（sizeUnit = 4px）
- 圆角使用标准梯度：2px / 4px / 6px / 8px
- 控件高度使用标准梯度：24px / 32px / 40px
- 字号使用标准梯度：12px / 14px / 16px / 20px / 24px / 30px / 38px
- 颜色优先使用语义化 Token（colorPrimary、colorSuccess 等），不直接使用色值
- 主色相关的派生色（hover、active、背景等）由算法自动生成，只需配置 Seed Token

### 覆盖优先级

```
品牌配置中的值 > Design Token 默认值
```

---

# 第一部分：品牌配置

> ⚠️ 修改此部分以适配你的项目品牌。未配置的项使用 Ant Design 默认值。

## 1. 品牌基础信息

| 配置项       | 值                  | 说明                       |
| --------- | ------------------ | ------------------------ |
| brandName | `你的品牌名`            | 品牌名称，用于页面标题、侧边栏 Logo 旁文字 |
| logoPath  | `/public/logo.svg` | Logo 文件路径（相对项目根目录）       |
| logoSize  | `32px`             | Logo 显示尺寸（侧边栏/顶栏）        |

## 2. 主题色配置

> 只需配置 Seed Token，派生色由 Ant Design 算法自动生成。

| Token        | 值         | 说明              |
| ------------ | --------- | --------------- |
| colorPrimary | `#1677FF` | 品牌主色（按钮/链接/选中态） |
| colorSuccess | `#52C41A` | 成功状态            |
| colorWarning | `#FAAD14` | 警告状态            |
| colorError   | `#FF4D4F` | 错误/危险状态         |
| colorInfo    | `#1677FF` | 信息提示（通常与主色一致）   |

### 主色派生色（由算法自动生成，无需手动配置）

| 派生 Token                | 用途                  |
| ----------------------- | ------------------- |
| colorPrimaryBg          | 主色浅背景（选中态背景、Tag 背景） |
| colorPrimaryBgHover     | 主色浅背景 hover         |
| colorPrimaryBorder      | 主色边框                |
| colorPrimaryBorderHover | 主色边框 hover          |
| colorPrimaryHover       | 主色 hover（按钮 hover）  |
| colorPrimaryActive      | 主色 active（按钮按下）     |
| colorPrimaryText        | 主色文字                |
| colorPrimaryTextHover   | 主色文字 hover          |
| colorPrimaryTextActive  | 主色文字 active         |

## 3. 布局配置

> 以下为 Pro 风格推荐值。antd 原生默认值：headerHeight=64px, siderWidth=200px, collapsedWidth=80px。

| 配置项                 | 值       | 说明                     |
| ------------------- | ------- | ---------------------- |
| siderWidth          | `208px` | 侧边栏展开宽度（antd 默认 200px） |
| siderCollapsedWidth | `48px`  | 侧边栏收起宽度（antd 默认 80px）  |
| headerHeight        | `48px`  | 顶栏高度（antd 默认 64px）     |
| contentPadding      | `24px`  | 内容区内边距                 |
| siderTheme          | `light` | 侧边栏主题：`light` / `dark` |
| headerTheme         | `light` | 顶栏主题：`light` / `dark`  |

## 4. 字体配置

| 配置项            | 值        | 说明     |
| -------------- | -------- | ------ |
| fontFamily     | （留空使用默认） | 品牌正文字体 |
| fontFamilyCode | （留空使用默认） | 代码字体   |

> 默认字体栈：`-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'Noto Sans', sans-serif`

---

# 第二部分：全局 Design Token

## 一、色彩体系

### 1.1 品牌色与语义色（Seed Token）

| Token        | 默认值       | 用途                       |
| ------------ | --------- | ------------------------ |
| colorPrimary | `#1677FF` | 品牌主色，按钮/链接/选中态           |
| colorSuccess | `#52C41A` | 成功状态（Result/Tag/Alert）   |
| colorWarning | `#FAAD14` | 警告状态（Alert/Notification） |
| colorError   | `#FF4D4F` | 错误/危险状态（按钮/校验）           |
| colorInfo    | `#1677FF` | 信息提示（同主色）                |
| colorLink    | `#1677FF` | 超链接默认色                   |

### 1.2 预设调色板

| Token    | 值         | Token   | 值         |
| -------- | --------- | ------- | --------- |
| red      | `#F5222D` | blue    | `#1677FF` |
| orange   | `#FA8C16` | purple  | `#722ED1` |
| yellow   | `#FADB14` | cyan    | `#13C2C2` |
| gold     | `#FAAD14` | green   | `#52C41A` |
| volcano  | `#FA541C` | magenta | `#EB2F96` |
| geekblue | `#2F54EB` | lime    | `#A0D911` |
| pink     | `#EB2F96` | —       | —         |

### 1.3 中性色（文字）

| Token                | 值                  | 用途           |
| -------------------- | ------------------ | ------------ |
| colorText            | `rgba(0,0,0,0.88)` | 主文字（标题/正文）   |
| colorTextSecondary   | `rgba(0,0,0,0.65)` | 次要文字（标签/描述）  |
| colorTextTertiary    | `rgba(0,0,0,0.45)` | 辅助文字（说明/占位符） |
| colorTextQuaternary  | `rgba(0,0,0,0.25)` | 最弱文字（禁用/提示）  |
| colorTextHeading     | `rgba(0,0,0,0.88)` | 标题文字         |
| colorTextDisabled    | `rgba(0,0,0,0.25)` | 禁用态文字        |
| colorTextPlaceholder | `rgba(0,0,0,0.25)` | 占位符文字        |
| colorTextLabel       | `rgba(0,0,0,0.65)` | 表单标签文字       |

### 1.4 背景色

| Token              | 值                  | 用途              |
| ------------------ | ------------------ | --------------- |
| colorBgContainer   | `#FFFFFF`          | 容器背景（卡片/输入框/按钮） |
| colorBgElevated    | `#FFFFFF`          | 浮层背景（弹窗/下拉/菜单）  |
| colorBgLayout      | `#F5F5F5`          | 页面整体布局背景        |
| colorBgMask        | `rgba(0,0,0,0.45)` | 遮罩层背景           |
| colorBgSpotlight   | `rgba(0,0,0,0.85)` | Tooltip 背景      |
| colorBgSolid       | `rgb(0,0,0)`       | 实色背景（v6 实色按钮）   |
| colorBgSolidHover  | `rgba(0,0,0,0.75)` | 实色背景 hover      |
| colorBgSolidActive | `rgba(0,0,0,0.95)` | 实色背景 active     |
| colorBgBlur        | `transparent`      | 毛玻璃容器背景         |

### 1.5 填充色

| Token               | 值                  | 用途   |
| ------------------- | ------------------ | ---- |
| colorFill           | `rgba(0,0,0,0.15)` | 最深填充 |
| colorFillSecondary  | `rgba(0,0,0,0.06)` | 二级填充 |
| colorFillTertiary   | `rgba(0,0,0,0.04)` | 三级填充 |
| colorFillQuaternary | `rgba(0,0,0,0.02)` | 最弱填充 |

### 1.6 边框色

| Token                | 值                  | 用途    |
| -------------------- | ------------------ | ----- |
| colorBorder          | `#D9D9D9`          | 默认边框  |
| colorBorderSecondary | `#F0F0F0`          | 次级边框  |
| colorBorderDisabled  | `#D9D9D9`          | 禁用态边框 |
| colorSplit           | `rgba(5,5,5,0.06)` | 分割线   |


---

## 二、字体体系

| Token            | 值        | 用途        |
| ---------------- | -------- | --------- |
| fontSize         | `14px`   | 基准字号（正文）  |
| fontSizeSM       | `12px`   | 小字号       |
| fontSizeLG       | `16px`   | 大字号       |
| fontSizeXL       | `20px`   | 超大字号（h4）  |
| fontSizeHeading3 | `24px`   | h3 标题     |
| fontSizeHeading2 | `30px`   | h2 标题     |
| fontSizeHeading1 | `38px`   | h1 标题     |
| lineHeight       | `1.5714` | 14px 正文行高 |
| fontWeightStrong | `600`    | 标题加粗      |

---

## 三、间距体系

基础单位：`sizeUnit = 4px`

| Token     | 值      | Token      | 值      |
| --------- | ------ | ---------- | ------ |
| marginXXS | `4px`  | paddingXXS | `4px`  |
| marginXS  | `8px`  | paddingXS  | `8px`  |
| marginSM  | `12px` | paddingSM  | `12px` |
| margin    | `16px` | padding    | `16px` |
| marginMD  | `20px` | paddingMD  | `20px` |
| marginLG  | `24px` | paddingLG  | `24px` |
| marginXL  | `32px` | paddingXL  | `32px` |
| marginXXL | `48px` | —          | —      |

---

## 四、圆角体系

| Token          | 值     | 用途           |
| -------------- | ----- | ------------ |
| borderRadius   | `6px` | 基础圆角（按钮/输入框） |
| borderRadiusSM | `4px` | 小圆角          |
| borderRadiusLG | `8px` | 大圆角（卡片/弹窗）   |
| borderRadiusXS | `2px` | 极小圆角         |

---

## 五、控件高度

| Token           | 值      | 用途   |
| --------------- | ------ | ---- |
| controlHeightSM | `24px` | 小控件  |
| controlHeight   | `32px` | 标准控件 |
| controlHeightLG | `40px` | 大控件  |

---

## 六、阴影体系

| Token             | 值                                                                                                 | 用途   |
| ----------------- | ------------------------------------------------------------------------------------------------- | ---- |
| boxShadow         | `0 6px 16px 0 rgba(0,0,0,0.08), 0 3px 6px -4px rgba(0,0,0,0.12), 0 9px 28px 8px rgba(0,0,0,0.05)` | 一级阴影 |
| boxShadowTertiary | `0 1px 2px 0 rgba(0,0,0,0.03), 0 1px 6px -1px rgba(0,0,0,0.02), 0 2px 4px 0 rgba(0,0,0,0.02)`     | 三级阴影 |

---

## 七、动效体系

| Token              | 值                                      | 用途     |
| ------------------ | -------------------------------------- | ------ |
| motionDurationFast | `0.1s`                                 | 快速交互   |
| motionDurationMid  | `0.2s`                                 | 中速交互   |
| motionDurationSlow | `0.3s`                                 | 慢速/页面级 |
| motionEaseInOut    | `cubic-bezier(0.645, 0.045, 0.355, 1)` | 标准进出   |
| motionEaseOut      | `cubic-bezier(0.215, 0.61, 0.355, 1)`  | 标准退出   |

---

## 八、响应式断点

| Token      | 值        | Min      | Max      |
| ---------- | -------- | -------- | -------- |
| screenXS   | `480px`  | `480px`  | `575px`  |
| screenSM   | `576px`  | `576px`  | `767px`  |
| screenMD   | `768px`  | `768px`  | `991px`  |
| screenLG   | `992px`  | `992px`  | `1199px` |
| screenXL   | `1200px` | `1200px` | `1599px` |
| screenXXL  | `1600px` | `1600px` | `1919px` |
| screenXXXL | `1920px` | `1920px` | —        |

---

## 九、交互态

| Token                    | 值                  | 用途           |
| ------------------------ | ------------------ | ------------ |
| controlItemBgHover       | `rgba(0,0,0,0.04)` | 列表项 hover    |
| controlItemBgActive      | `#E6F4FF`          | 列表项选中        |
| controlItemBgActiveHover | `#BAE0FF`          | 列表项选中+hover  |
| colorBgContainerDisabled | `rgba(0,0,0,0.04)` | 禁用态背景        |
| colorFillContentHover    | `rgba(0,0,0,0.15)` | 内容区 hover 背景 |

---

## 十、v6 新增 Token

| Token               | 值                  | 用途          |
| ------------------- | ------------------ | ----------- |
| colorBgSolid        | `rgb(0,0,0)`       | 实色按钮背景      |
| colorBgSolidHover   | `rgba(0,0,0,0.75)` | 实色按钮 hover  |
| colorBgSolidActive  | `rgba(0,0,0,0.95)` | 实色按钮 active |
| colorBgBlur         | `transparent`      | 毛玻璃背景       |
| colorBorderDisabled | `#D9D9D9`          | 禁用态边框       |
| colorErrorAffix     | `#FF4D4F`          | 错误态前/后缀     |
| colorWarningAffix   | `#FAAD14`          | 警告态前/后缀     |
| linkDecoration      | `none`             | 链接装饰        |
| linkHoverDecoration | `none`             | 链接 hover 装饰 |

### 语义色完整派生梯度

每个语义色（Success/Warning/Error/Info）均有以下派生 Token（由算法自动生成）：

| 后缀          | 用途        | Success 示例值 |
| ----------- | --------- | ----------- |
| Bg          | 浅背景       | `#F6FFED`   |
| BgHover     | 浅背景 hover | `#D9F7BE`   |
| Border      | 边框        | `#B7EB8F`   |
| BorderHover | 边框 hover  | `#95DE64`   |
| Hover       | 色值 hover  | `#95DE64`   |
| Active      | 色值 active | `#389E0D`   |
| Text        | 文字        | `#52C41A`   |
| TextHover   | 文字 hover  | `#73D13D`   |
| TextActive  | 文字 active | `#389E0D`   |


---

# 第三部分：B 端页面布局指南

## 设计画布

| 属性     | 推荐值      | 说明                |
| ------ | -------- | ----------------- |
| 标准画布宽度 | `1440px` | Ant Design 团队统一标准 |
| 大屏画布   | `1920px` | 大屏/数据看板           |
| 笔记本画布  | `1280px` | 小屏适配              |

## antd 原生 Layout 默认值

| Token                | 默认值         | 说明            |
| -------------------- | ----------- | ------------- |
| headerHeight         | `64px`      | Header 高度     |
| headerBg             | `#001529`   | Header 背景（深色） |
| headerPadding        | `0 50px`    | Header 内边距    |
| Sider width          | `200px`     | Sider 展开宽度    |
| Sider collapsedWidth | `80px`      | Sider 收起宽度    |
| siderBg              | `#001529`   | Sider 背景（深色）  |
| lightSiderBg         | `#FFFFFF`   | Sider 背景（亮色）  |
| bodyBg / footerBg    | `#F5F5F5`   | 内容区/Footer 背景 |
| footerPadding        | `24px 50px` | Footer 内边距    |
| triggerHeight        | `48px`      | 触发器高度         |

### 导航高度计算公式

| 场景        | 值/公式       |
| --------- | ---------- |
| 顶部导航（一级）  | `64px`     |
| 顶部导航（二级）  | `48px`     |
| 落地页导航（一级） | `80px`     |
| 顶部导航通用公式  | `48 + 8n`  |
| 侧边导航通用公式  | `200 + 8n` |

## B 端推荐配置（Pro 风格覆盖值）

> 以下为 Ant Design Pro 风格的 B 端管理系统推荐值，覆盖 antd 原生默认值。

```
┌─────────────────────────────────────────────────┐
│                  Header (48px)                  │
├──────────┬──────────────────────────────────────┤
│  Sider   │           Content                    │
│ (208px)  │        (padding: 24px)               │
└──────────┴──────────────────────────────────────┘
```

| 区域          | Pro 推荐值   | antd 默认值  | 说明            |
| ----------- | --------- | --------- | ------------- |
| 顶部栏高度       | `48px`    | `64px`    | Pro 更紧凑       |
| 顶部栏内边距      | `0 24px`  | `0 50px`  | —             |
| 顶部栏背景       | `#FFFFFF` | `#001529` | Pro 亮色主题      |
| 侧边栏宽度（展开）   | `208px`   | `200px`   | Pro 略宽        |
| 侧边栏宽度（收起）   | `48px`    | `80px`    | Pro 更紧凑       |
| 侧边栏背景       | `#FFFFFF` | `#001529` | Pro 亮色主题      |
| 内容区 padding | `24px`    | —         | 四周统一          |
| 内容区背景色      | `#F5F5F5` | `#F5F5F5` | colorBgLayout |
| 卡片间距        | `16px`    | —         | margin        |

## 栅格系统

| 属性            | 值               | 说明               |
| ------------- | --------------- | ---------------- |
| 布局网格基础单位      | `8px`           | 官方推荐，布局尺寸为 8 的倍数 |
| 栅格列数          | `24`            | —                |
| 间距（gutter）    | `16px` 或 `24px` | 根据内容密度           |
| 内容区宽度（1440画布） | `1168px`        | 24 栅格分配          |

> 注意：布局级用 8px 网格，组件级用 4px（sizeUnit）。

## 卡片/区块

| 属性     | 推荐值               |
| ------ | ----------------- |
| 卡片内边距  | `24px`            |
| 卡片圆角   | `8px`             |
| 卡片阴影   | boxShadowTertiary |
| 卡片背景   | `#FFFFFF`         |
| 区块标题字号 | `16px`            |
| 区块标题字重 | `600`             |
| 区块间距   | `24px`            |

## 表格参数

| 属性         | 推荐值       |
| ---------- | --------- |
| 表头背景       | `#FAFAFA` |
| 单元格内边距     | `16px`    |
| 行 hover 背景 | `#FAFAFA` |
| 行分割线       | `#F0F0F0` |

## 表单参数

| 属性    | 推荐值     |
| ----- | ------- |
| 输入框高度 | `32px`  |
| 输入框圆角 | `6px`   |
| 表单项间距 | `24px`  |
| 标签对齐  | `right` |
| 标签栅格  | `6~8`   |

## 页面间距规范

| 层级  | 间距     | 用途      |
| --- | ------ | ------- |
| 页面级 | `24px` | 内容区与边界  |
| 区块级 | `24px` | 卡片之间    |
| 组件级 | `16px` | 卡片内元素之间 |
| 元素级 | `8px`  | 紧凑元素之间  |
| 最小级 | `4px`  | 图标与文字   |

> 布局级间距遵循 8px 网格，组件级间距遵循 4px 基础单位。

---

# 第四部分：组件级设计参数

## Button 按钮

| 尺寸     | 高度     | 圆角    | 字号     | 水平内边距  |
| ------ | ------ | ----- | ------ | ------ |
| small  | `24px` | `4px` | `14px` | `7px`  |
| middle | `32px` | `6px` | `14px` | `15px` |
| large  | `40px` | `8px` | `16px` | `15px` |

| 属性     | 值         |
| ------ | --------- |
| 主按钮文字色 | `#FFF`    |
| 默认按钮边框 | `#D9D9D9` |
| 危险按钮色  | `#FF4D4F` |

## Input 输入框

| 属性        | 值                        |
| --------- | ------------------------ |
| 字号        | `14px`                   |
| 横向内边距     | `11px`                   |
| 激活态边框     | 主色                       |
| 激活态阴影     | `0 0 0 2px rgba(主色,0.1)` |
| hover 边框色 | 主色浅                      |

## Table 表格

| 属性         | 值                |
| ---------- | ---------------- |
| 单元格字号      | `14px`           |
| 单元格内边距     | `16px`           |
| 表头背景       | `#FAFAFA`        |
| 表头圆角       | `8px`            |
| 行 hover 背景 | `#FAFAFA`        |
| 行选中背景      | `#E6F4FF`（随主色变化） |
| 选择列宽度      | `32px`           |

## Modal 对话框

| 属性   | 值                  |
| ---- | ------------------ |
| 默认宽度 | `520px`            |
| 圆角   | `8px`              |
| 内边距  | `24px`             |
| 标题字号 | `16px`             |
| 标题字重 | `600`              |
| 遮罩背景 | `rgba(0,0,0,0.45)` |
| 阴影   | boxShadow（一级）      |

## Form 表单

| 属性    | 值         |
| ----- | --------- |
| 标签对齐  | `right`   |
| 标签栅格  | `6~8`     |
| 控件栅格  | `16~18`   |
| 表单项间距 | `24px`    |
| 必填星号色 | `#FF4D4F` |

## Tag 标签

| 语义         | 背景        | 文字                 | 边框        |
| ---------- | --------- | ------------------ | --------- |
| success    | `#F6FFED` | `#52C41A`          | `#B7EB8F` |
| processing | `#E6F4FF` | `#1677FF`          | `#91CAFF` |
| warning    | `#FFFBE6` | `#FAAD14`          | `#FFE58F` |
| error      | `#FFF2F0` | `#FF4D4F`          | `#FFCCC7` |
| default    | `#FAFAFA` | `rgba(0,0,0,0.88)` | `#D9D9D9` |

Tag 基础参数：字号 `12px`，圆角 `4px`，内边距 `0 7px`，行高 `20px`

## Select 选择器

| 属性     | 值                                |
| ------ | -------------------------------- |
| 高度     | `32px`（标准）/ `40px`（大）/ `24px`（小） |
| 圆角     | `6px`                            |
| 下拉面板圆角 | `8px`                            |
| 选项选中背景 | `#E6F4FF`（随主色变化）                 |

## Tabs 标签页

| 属性    | 值      |
| ----- | ------ |
| 标签字号  | `14px` |
| 标签间距  | `32px` |
| 选中字色  | 主色     |
| 下划线宽度 | `2px`  |
| 下划线色  | 主色     |

## Menu 导航菜单

| 属性    | 值      |
| ----- | ------ |
| 菜单项高度 | `40px` |
| 选中背景  | 主色浅背景  |
| 选中文字  | 主色     |
| 子菜单缩进 | `24px` |

## Drawer 抽屉

| 属性    | 值       |
| ----- | ------- |
| 默认宽度  | `378px` |
| 大宽度   | `736px` |
| 内容内边距 | `24px`  |
| 标题字号  | `16px`  |
| 标题字重  | `600`   |

## Pagination 分页

| 属性   | 值      |
| ---- | ------ |
| 按钮尺寸 | `32px` |
| 圆角   | `6px`  |
| 选中背景 | 主色     |
| 选中文字 | `#FFF` |

## Alert 警告提示

| 语义      | 背景        | 边框        |
| ------- | --------- | --------- |
| success | `#F6FFED` | `#B7EB8F` |
| info    | `#E6F4FF` | `#91CAFF` |
| warning | `#FFFBE6` | `#FFE58F` |
| error   | `#FFF2F0` | `#FFCCC7` |

圆角 `8px`，字号 `14px`

## 其他常用组件速查

| 组件           | 关键参数                                           |
| ------------ | ---------------------------------------------- |
| Card         | 内边距 `24px`，圆角 `8px`，阴影 boxShadowTertiary       |
| Tooltip      | 背景 `rgba(0,0,0,0.85)`，文字 `#FFF`，圆角 `6px`       |
| Popover      | 背景 `#FFF`，圆角 `8px`，阴影 boxShadow                |
| Dropdown     | 背景 `#FFF`，圆角 `8px`，选项 hover `rgba(0,0,0,0.04)` |
| Badge        | 背景 `#FF4D4F`，文字 `#FFF`，字号 `12px`               |
| Avatar       | 默认 `32px`，小 `24px`，大 `40px`                    |
| Progress     | 高度 `8px`，全圆角，默认色=主色                            |
| Switch       | 高度 `22px`，宽 `44px`，开启=主色                       |
| Checkbox     | 尺寸 `16px`，圆角 `2px`，选中=主色                       |
| Radio        | 尺寸 `16px`，圆点 `8px`，选中=主色                       |
| Steps        | 圆点 `32px`，完成色=主色，等待色=`#F0F0F0`                 |
| Spin         | 默认 `20px`，大 `48px`，颜色=主色                       |
| Skeleton     | 背景 `rgba(0,0,0,0.06)`，圆角 `4px`                 |
| Divider      | 颜色 `rgba(5,5,5,0.06)`，上下间距 `24px`              |
| Tree         | 节点高 `28px`，选中背景 `#E6F4FF`，缩进 `24px`            |
| Timeline     | 圆点 `10px`，连接线 `#F0F0F0`，宽 `2px`                |
| Message      | 圆角 `8px`，阴影 boxShadow，z-index `1010`           |
| Notification | 宽度 `384px`，z-index `2050`                      |
| Upload       | 拖拽区圆角 `8px`，虚线边框 `#D9D9D9`，hover=主色            |

---

# 使用提示

## 在 Codex 中使用

将此文件放在项目根目录，Codex 会自动将其作为上下文。或在 `AGENTS.md` 中引用：

```markdown
## Design System
When generating UI code, follow the design specifications in `ant-design-system-portable.md`.
```

## 在 Cursor 中使用

将此文件添加到 `.cursorrules` 或项目的 docs 目录中，Cursor 会自动索引。

## 在 Claude Projects 中使用

将此文件作为 Project Knowledge 上传即可。

## 自定义品牌色

修改「第一部分：品牌配置」中的 `colorPrimary` 值即可。所有标注"主色"或"随主色变化"的参数会自动适配。

<!--  作者：Charles.Chen 微信：yixiusztx  如有问题，欢迎指正，谢谢。  -->
