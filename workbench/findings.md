# Finding 台账

手维护(非生成)。条目要件与生命周期见 [规范/机制/finding台账.md](规范/机制/finding台账.md):`观察中 → 已确认 → 已转Case / 已接受`;每次被再次观察到,追加 `triggered_by`。**台账即触发器**——攒到碍眼时由用户打包转 REF Case,不设定期巡检。

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

```yaml
id: FIND-010
status: 已转Case
triggered_by: [workbench外壳重设计]
evidence: "执行序以同等粒度写在 AGENTS.md 流程锚点与 规范.md 单线图两处,出生即分叉一处;来源:workbench外壳重设计 期1 收敛评审1 挂账1"
convert_when: "已关账(2026-09-02,本 case 期2 页面重排):执行序唯一权威收敛至 规范/流程.md,AGENTS 锚点降为本仓落地信号,规范.md 主页只留页面地图"
```

```yaml
id: FIND-011
status: 观察中
triggered_by: [workbench外壳重设计]
evidence: "findings.md 台账(id 唯一性/status 枚举/triggered_by 指向)与需求 triggered_by 字段均无机械校验,与铁律3『悬空即拦』不对称;findings-count 检查仅在本案 checklist 非常驻;来源:workbench外壳重设计 期1 收敛评审1 挂账2"
convert_when: "台账超 30 条或出现一次实际漂移事故时,build_views 加约 25 行校验 + 2 条测试;或用户确认手维护定位则转豁免"
```

---

**不立账说明**:方案v4 实施偏差记录1 的遗留动作「建可复用浏览器巡检驱动脚本」属工具便利项而非结构债,不入台账;需要时按一次性任务处理。

```yaml
id: FIND-012
status: 观察中
triggered_by: [ee包壳骨架]
evidence: "ee/transports/bifrost-http/server/bootstrap.go:32,39-64 的 attach 把表/插件/路由/中间件四步串在一个函数,注释已预告 B1 审计中间件、B2 IsEnterprise 都往这里加;现 4 步 64 行不构成问题,但是被指定的惯性点;来源:ee包壳骨架 期1 收敛评审1 挂账1"
convert_when: "B1 第二次往 attach 加四件套时,先评是否拆成按功能注册(每功能一个 Register(s) 文件,attach 只剩列表)"
```

```yaml
id: FIND-013
status: 观察中
triggered_by: [ee包壳骨架]
evidence: "骨架探针会随生产二进制发出:ee_probe 表(attach 每次启动 AutoMigrate 建、永不删)、ee-probe 插件、无鉴权 /api/ee/ping、X-Bifrost-EE 响应头、x-bifrost-ee meta,散在 lib/tables.go、lib/shell.go、handlers/probe.go、handlers/middlewares.go、server/probe_plugin.go 五处 + attach 4 行;来源:ee包壳骨架 期1 收敛评审1 挂账2"
convert_when: "第一个 B 功能落地或第一次对外构建前,决定保留为健康探针(包鉴权)或整体删除(删表需 REF『ee骨架对齐上游建表与构建约定』的迁移体系配 Rollback)"
```

```yaml
id: FIND-014
status: 观察中
triggered_by: [ee包壳骨架]
evidence: "ee/transports/bifrost-http/main.go 是上游 main.go 抄本(除注释仅 4 处差异),同步只靠包注释一句『以 diff 同步』,无机械护栏;上游 main.go 共 15 次提交,近期两次实质改动(#4475 pprof、#2473 bootstrap 计时);来源:ee包壳骨架 期1 收敛评审1 挂账3"
convert_when: "下次合并上游若 main.go 有改动,在 ee 的 checklist 加『上游 main.go 与抄本 diff 只允许 4 处已知 hunk』检查(约 10 行脚本)"
```

```yaml
id: FIND-015
status: 观察中
triggered_by: [ee包壳骨架]
evidence: "ee/scripts/smoke.sh 依赖本机状态:个人 ~/.config/bifrost/config.db(T1 要求 provider 集合非空)、固定端口 18080/18081、lsof 按端口 kill(可能误杀无关进程)、sqlite3/python3;仅能单人本机跑,不可进 CI;来源:ee包壳骨架 期1 收敛评审1 挂账4"
convert_when: "第二个人要跑或要进 CI 时,改为脚本自建最小 config.json + 动态端口 + 只杀自己起的 pid"
```

```yaml
id: FIND-016
status: 观察中
triggered_by: [ee包壳骨架]
evidence: "上游 server.go Bootstrap 调 RegisterAPIRoutes(s.Ctx, s, ...) 硬编码传 s 自己,ServerCallbacks 参数与其内三处 callbacks.(XxxProvider) 断言(:2198/2201/2320)无法从 ee 替换;03 文档 §1.4 已订正为不可用;来源:方案v3 §8 指定由收敛评审登记,ee包壳骨架 期1 收敛评审1 挂账5"
convert_when: "任一功能(集群广播、日志脱敏映射、治理路由整体替换)需要此通路时,向上游提 PR 加导出字段,不在 ee 内绕"
```
