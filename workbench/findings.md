# Finding 台账

手维护(非生成)。条目要件与生命周期见 [规范/流程/finding台账.md](规范/流程/finding台账.md):`观察中 → 已确认 → 已转Case / 已接受`;每次被再次观察到,追加 `triggered_by`。**台账即触发器**——攒到碍眼时由用户打包转 REF Case,不设定期巡检。

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
triggered_by: [ee包壳骨架, Logo品牌设置]
evidence: "ee/transports/bifrost-http/server/bootstrap.go:32,39-64 的 attach 把表/插件/路由/中间件四步串在一个函数,注释已预告 B1 审计中间件、B2 IsEnterprise 都往这里加;现 4 步 64 行不构成问题,但是被指定的惯性点;来源:ee包壳骨架 期1 收敛评审1 挂账1"
convert_when: "B1 第二次往 attach 加四件套时,先评是否拆成按功能注册(每功能一个 Register(s) 文件,attach 只剩列表)"
```

```yaml
id: FIND-013
status: 已转Case
triggered_by: [ee包壳骨架, Logo品牌设置, EE现有代码架构迁移]
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
status: 已转Case
triggered_by: [ee包壳骨架, EE现有代码架构迁移]
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


## Logo品牌设置复查（2026-09-08）

- FIND-012：本案 bootstrap 净增10行，仍为薄装配；收敛评审1未发现职责失控，继续观察。
- FIND-013：首个业务功能触发已到达；用户 Gate 1「可以」接受本案保留公共探针现状，交整合阶段处理。本案未关闭此项，正式对外构建前仍须按原口径处置，不据此次延期视为永久豁免。
- FIND-015：新品牌 smoke 已使用独立临时数据/动态端口/自有PID，但旧骨架 smoke 未改，不关闭旧条目。

## Logo品牌设置复查2（2026-09-09）

收敛评审2（按修订后规范重做）对账：FIND-012 bootstrap 仅改注释与错误前缀，未恶化；FIND-013 探针未动，本轮拒绝顺手改其迁移方式；FIND-014/015/016 未触及。无新增 Finding。

## EE 现有代码架构迁移（2026-09-11）

- FIND-013 转入本案：用户明确要求移除骨架探针，已删除接口、空插件、表注册及响应/页面标记；既有数据库旧表不再使用，不执行删表。
- FIND-015 转入本案：删除旧 smoke.sh，make smoke 使用自建临时配置、随机端口和仅清理自有 PID 的品牌冒烟。

## 账号认证（2026-09-12，收敛评审5）

```yaml
id: FIND-017
status: 观察中
triggered_by: [账号认证]
evidence: "SQLite busy/locked 整笔重试在 ee/internal/branding/persistence/migration.go 与 ee/internal/identity/persistence/diagnostics.go 各一份，参数已分别命名并互相注明；两个模块的迁移独立演进，不为 10 行驱动兼容代码建共享包；来源:收敛评审5 取舍项 B"
convert_when: "第三个模块需要同样的 busy 重试，或两份策略出现实质分叉时，提取为 EE 内共享的小包"
```

- 取舍项 A（品牌冒烟复用身份冒烟的进程夹具）、C（配置投影注释与 RBAC 替换提示）、D（`make smoke` 接入身份冒烟）均已采纳，不挂账。

```yaml
id: FIND-018
status: 观察中
triggered_by: [账号认证]
evidence: "ee/internal/identity/persistence/store.go:262 的 ReserveLogin 复用 s.transaction，每次登录尝试（含将被限流拒绝的那次）都 UPDATE state.revision 抢全局写锁，未认证流量可与账号管理事务争锁，SQLite 下等于串行化整个身份子系统；方案 §256 已把容量问题后置，三个冒烟含双节点 PG 未出现失败；来源:代码评审5 挂账；身份模块整理 期1 的 FIND-020 清理在同一 state 写锁事务内新增一次无索引 DELETE，代码评审1 定向复核实测：稳态 SQLite 0.13ms/PG 0.35ms，1 万行积压 3.5ms/2.2ms，10 万行 303ms/25ms，100 万行 7.1s/1.55s（一次性，升级后首登），量级上不叠加"
convert_when: "出现真实登录并发容量问题，或把限流桶挪出 state 锁时一并处理"
```

```yaml
id: FIND-019
status: 已接受
triggered_by: [账号认证]
evidence: "哈希槽耗尽与登录限流共用 ErrLimited，Retry-After 一律取登录窗口 60 秒（ee/internal/identity/http/protocol.go:121），而槽位争抢通常百毫秒内消散；429 与 Retry-After:60 由方案 4.3 与 ee/docs/账号认证接口.md:44 规定为契约；来源:代码评审5 挂账"
convert_when: "豁免(改动要动已批准契约)；若前端按 Retry-After 退避导致可感知延迟再评估"
```

## 账号认证（2026-09-12，代码通读）

```yaml
id: FIND-020
status: 已转Case
triggered_by: [账号认证, 身份模块整理]
evidence: "ee_identity_sessions 与 ee_identity_ws_tickets 无任何删除路径（ee/internal/identity/persistence/store.go 全文只有 login_limits 的 Delete，:265），过期行永久累积；逻辑过期由 verify 保证，只是运维成本；来源:用户与 Claude Code 通读代码时确认"
convert_when: "已转入 身份模块整理 期1 方案 §4.6：写入新会话/票据时顺带删除 expires_at 已过的行；期1 已实现（store.go sweepExpired，收敛评审1 核对与 §4.6 拍板一致），随期1 交付关账"
```

```yaml
id: FIND-021
status: 观察中
triggered_by: [身份模块整理]
evidence: "ee/internal/identity/persistence/store.go 的到期清理 sweepExpired 自取 time.Now().UTC()，而同文件 ReserveLogin 与 Tx 的 RevokeSession/ConsumeTicket 均由业务层传 at；identity 侧只有 core.go now() 一个时钟。改 Tx.InsertSession/InsertTicket 签名超出方案 §6 白名单，本期不做；R5 已把时钟读取收敛到一处；来源:收敛评审1 取舍项 S1"
convert_when: "下次需要可注入时钟（测试或时钟漂移排查），或 Tx 因其他原因要改签名时一并收口"
```

## 国内定价（2026-09-13，用户通读代码）

```yaml
id: FIND-022
status: 观察中
triggered_by: [国内定价]
evidence: "五轮收敛评审（claude-opus-5/high，轮次 1–5）均未发现 ee/internal/pricing/README.md 违反编码规范第 43 节「先讲负责什么、解决什么问题」——原文通篇只有文件表，没有一句交代包为何存在；评审第 2 轮反而肯定了该 README 的其他方面。同轮次对 Service.Sync 87 行的控制流判为「读得顺、无问题」，用户读后认为难读。两处均由用户在 Gate 2 前自行发现（3a00bf7f6 已修）。来源:用户直接指出"
convert_when: "下一个 Change 的收敛评审启动前，评估是否在提示词模板中加入「逐条对照编码规范明文要求」的机械检查项；若再次出现规范明文违规被评审放过，升级为流程 Change"
```

```yaml
id: FIND-023
status: 观察中
triggered_by: [国内定价]
evidence: "上游覆盖匹配区分大小写：datasheet 基准查询用 makeKey(model,provider,mode) 拼接字典键（framework/modelcatalog/datasheet/types.go:380），覆盖精确匹配用 c.exact[model] 直接查（overrides.go:226），两条路径都不做大小写归一。本地定价表里同一模型两种写法并存（wafer/GLM-5.1 大写、zai/glm-4.5 小写，全表 glm 行 27 条小写 4 条含大写）。2026-09-15 实测智谱官方 /models 返回 10 个模型全为小写，与价格文件 8 条同名项大小写完全一致，本期未踩到；但若将来某厂商官方端点使用大写模型名，覆盖会静默失效、费用回落为零且无任何报错。来源:用户提问后核实"
convert_when: "新增厂商或收到费用为零的反馈时，先核对官方模型名大小写；若需根治，向上游提 issue 让覆盖匹配忽略大小写（改上游代码，不在本 fork 原则内）"
```

## EE 架构（2026-09-15，用户读代码后提出）

```yaml
id: FIND-024
status: 观察中
triggered_by: [国内定价, 角色权限后端]
evidence: "ee/internal/host/ 是按技术分类切的目录，放在 feature-first 的 ee/internal/ 下不一致；其中 auth.go 与 config.go 全部是身份模块的宿主接入（会话中间件、旧 session 路由接管、WebSocket 重验、旧管理员导入、/api/config 认证字段投影），引用方只有 app/bootstrap.go 与 app/identity.go。RBAC 新增的宿主适配已按用户决定放在 rbac/host/，实践上已否定「统一放 internal/host」。正确位置是 ee/internal/identity/host/，届时架构 §7 第一条应改为「宿主适配归所属模块的 host 子包」，internal/host 不再存在。来源:用户提出"
convert_when: "角色权限后端合入 develop 之后立重构 Change：搬两个文件、改包名、改两处引用，按重构类流程给基线行为盘点与公共符号白名单；不在权限线推进期间做，避免它每批重对基线"
```

```yaml
id: FIND-025
status: 观察中
triggered_by: [角色权限后端]
evidence: "RBAC 为接入判权，在 ee/internal/host/auth.go 上开了三个可选接缝：WithConsoleAccess（认证后交由权限组件判权）、WithAdditionalRoutes（其他自管模块的路由归属）、WithRecoveryAnchor（恢复锚点查询），均为纯增量且遵守架构 §7「只声明窄接口、由 app 注入」。期1 设计时 RBAC 不存在，未预留扩展点是合理的 YAGNI，不算缺陷。问题在于这种「一个合作者一个构造选项」的开法不可扩展：审计模块要的是「每次管理操作后记一笔」，形状与三者都不同，接入时认证包要加第四个选项并重跑鉴权边界测试。来源:用户提问「是不是应该修改认证模块本身」"
convert_when: "第三个模块需要在认证链上开缝时，先重构认证的扩展模型（认证只负责识别身份，授权/审计/限制各自注册进一条定义好的处理管线，认证包不再知道具体合作者名字），不要加第四个构造选项；届时手上有三个真实用例可供设计"
```

## 内容安全框架（2026-09-16，独立验证与转交核对）

```yaml
id: FIND-026
status: 观察中
triggered_by: [内容安全框架]
evidence: "既有 ee/scripts/branding-smoke.py:126 使用 TemporaryDirectory，异常离开 with 时也删除数据库和 server.log。内容安全框架代码评审 1/2 额外运行品牌冒烟，因未嵌入 UI 在第 67 行失败，现场随即清理，无法继续定位。安全框架不改品牌脚本，失败事实已保留在两份独立评审记录。"
convert_when: "下次修改品牌冒烟脚本或调查品牌冒烟失败时，改成失败保留并打印现场路径，成功时清理；保留目录中的测试凭据仍应限制权限"
```

```yaml
id: FIND-027
status: 观察中
triggered_by: [内容安全框架]
evidence: "workbench/tools/convergence_review.py:71 的 section 仅匹配 startswith('转收敛评审')，但代码评审模板产出的栏目带数字，例如本案代码评审 2 的「## 5. 转收敛评审 / 挂账」，导致 :174 提取为空、脚本未自动附上转交项。已将原栏目不作改写地附入方案 v2 的实施记录供差量复核读取；未修改评审记录、提示词或工具代码。"
convert_when: "下次修改独立评审启动工具时，支持编号标题并增加带编号/不带编号的提取回归，避免两侧转交项静默遗漏"
```
