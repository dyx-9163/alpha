# AIFAR Runtime 发布历史与入口表格设计 QA

- Source visual truth: `C:\Users\Administrator\AppData\Local\Temp\codex-clipboard-1722b386-7f58-4da6-9358-f0a0bac185cd.png`，结合用户明确要求“发布历史移到最后、增加删除动作”；入口表格删列以 `C:\Users\Administrator\AppData\Local\Temp\codex-clipboard-88dd2f4d-9709-4114-a993-4b66e2459566.png` 的红框为范围。
- Implementation screenshot: `D:\workspace\aifar-deployment\.tmp\release-delete-qa.png`
- Viewport: 2048 x 1080 CSS px
- Source pixels: 2048 x 1080
- Implementation pixels: 2048 x 1080
- Density normalization: source and implementation are both 1x at equal pixel dimensions; no resampling required.
- State: 容器 > AIFAR 运行时 > 发布历史，已加载 10 条真实面板记录；全局任务浮层已关闭。

## Full-view comparison evidence

- 页面框架、侧栏、顶栏、运行时摘要、工具栏、表格密度、边框、圆角和状态色继续沿用现有 Ant Design 风格令牌。
- 发布历史从第二个标签移到最后，符合用户指定的信息架构变化；其余标签顺序与原页面保持一致。
- 发布历史表格新增红色描边“删除”动作，和已有橙色“回滚”并列，操作列宽度足够，没有遮挡、换行或横向溢出。
- 入口与发现页面的可见列已核对为“服务、应用名、发现地址、Endpoint”，不再显示“Nacos、最近错误”。

## Focused-region comparison evidence

- 聚焦检查了发布历史操作列：10 行均展示删除动作；不可回滚行保留禁用回滚状态，删除动作保持独立可用。
- 点击首行删除后出现确认框，明确提示“仅删除面板记录及关联索引，不删除远端制品、容器或运行状态”；随后取消，未执行删除。
- 浏览器控制台 error/warn 为 0。

## Findings

- 无 P0/P1/P2 视觉或交互问题。
- 字体与排版：沿用现有页面字体、字号和权重，新增按钮与相邻操作一致。
- 间距与布局：标签和表格节奏未改变；操作列扩宽后按钮间距正常。
- 颜色与令牌：删除使用现有 Element Plus danger 语义色，状态与回滚颜色保持原样。
- 图片与资产：本次界面没有新增或替换图片资产。
- 文案与内容：删除确认文案完整表达控制面删除边界，中英文均已提供。

## Comparison history

- Initial pass: 未发现需要修复的 P0/P1/P2 问题，因此没有产生视觉修复迭代。

## Implementation checklist

- [x] 删除入口表格的 Nacos 与最近错误列
- [x] 将发布历史放到资源标签最后
- [x] 添加发布记录删除动作、确认提示和行级 loading
- [x] 验证取消确认不会删除记录
- [x] 检查控制台无 error/warn

final result: passed
