# Finding 台账

手维护(非生成)。条目要件与生命周期见 [规范/change/findings.md](规范/change/findings.md):`观察中 → 已确认 → 已转Case / 已接受`;每次被再次观察到,追加 `triggered_by`。**台账即触发器**——攒到碍眼时由用户打包转 REF Case,不设定期巡检。

---

```yaml
id: FIND-001
status: 观察中
triggered_by: [UI完整中文化]
evidence: "115 条跨节点碎片(文本被内嵌元素或 {expr} 插值打断)白名单化未翻译;清单见 evidence/UI完整中文化-期1-行级旁路分流.md;来源:方案v4 实施偏差记录3"
convert_when: "有浏览器可视化核验条件或用户反馈指认具体页面时,逐条确认后转期次处理"
```

```yaml
id: FIND-002
status: 已接受
triggered_by: [UI完整中文化]
evidence: "启发式正则提取(非 AST)对『能被触发为候选的文本』之外的形态固有枚举不完备(如两个 <span> 之间的中段文本);方案 §3 已声明不做 AST 为非目标;来源:代码评审2 质疑2"
convert_when: "豁免(方案既定非目标);若某期决定引入 TS AST 提取则整体重估"
```

```yaml
id: FIND-003
status: 观察中
triggered_by: [UI完整中文化]
evidence: "FIND-001 的 115 条中混有『标签+装饰性标记』模式(Rule Name <span>*</span> 等),翻译标签本身无语序错乱风险,可低成本拆出处理;来源:代码评审2 质疑1"
convert_when: "期2 立中文化 case 时拆出该结构模式单独处理"
```

```yaml
id: FIND-004
status: 观察中
triggered_by: [UI完整中文化]
evidence: "rg 全量反证探针(message 键/单引号三元/任意键位英文串)未固化进 checklist.yaml,仅在评审3 与验收抽查中临时使用;来源:代码评审3 遗留、方案v4 §7"
convert_when: "下次触碰 zh-coverage.mjs 时一并把探针收进 self-test 或 checklist"
```

```yaml
id: FIND-005
status: 观察中
triggered_by: [UI完整中文化]
evidence: "alt= 属性文案(如 logDetailView.tsx:2067 'Attached image')不在运行时 ATTRS(placeholder/title/aria-label)内,翻译层不可达,扫描器亦不提取;来源:方案v4 §7"
convert_when: "用户反馈 a11y/图片占位文案需中文时,扩运行时 ATTRS + 扫描通道同步"
```

```yaml
id: FIND-006
status: 已接受
triggered_by: [UI完整中文化]
evidence: "代码示例位 content:/input: 键的英文串(emptyState 里的 LangChain 示例等)渲染于 CODE/PRE 内,运行时 SKIP_TAGS 跳过,属方案非目标;来源:方案v4 §7"
convert_when: "豁免(代码示例保英文是产品决定);若产品口径变化再启"
```

```yaml
id: FIND-007
status: 观察中
triggered_by: [UI完整中文化]
evidence: "zh-coverage --check/--self-test 门禁仅挂 workbench checklist.yaml,不在 ui/package.json scripts 与 CI;upstream merge 引入新英文文案要到下次手动预飞才被发现;来源:收敛评审1 挂账1"
convert_when: "下次合 upstream 前加 package.json 'zh:check' 别名(10 分钟);是否进 CI 由 fork 策略裁决"
```

```yaml
id: FIND-008
status: 观察中
triggered_by: [UI完整中文化]
evidence: "zhDict.json 键按字母排序仅靠 zhLocale.ts 注释承诺,parseZhDict 查重不查序,乱序插入会静默劣化 diff/merge;实测 3047 键 0 乱序;来源:收敛评审1 挂账2"
convert_when: "下次触碰 parseZhDict 时加一行序检查(15 分钟含自测)"
```

```yaml
id: FIND-009
status: 观察中
triggered_by: [UI完整中文化]
evidence: "⑧slug 合成算法在 zh-coverage.mjs:273-276 与 topbar.utils.ts:25-27,77 是两份抄写,算法漂移无护栏(数据漂移已有 parseTopbarMaps 兜底);来源:收敛评审1 挂账3"
convert_when: "REF 需求『zh中文化层TS-mjs孪生收敛』落地即消(见 reports/,triggered_by 本案)"
```

---

**不立账说明**:方案v4 实施偏差记录1 的遗留动作「建可复用浏览器巡检驱动脚本」属工具便利项而非结构债,不入台账;需要时按一次性任务处理。
