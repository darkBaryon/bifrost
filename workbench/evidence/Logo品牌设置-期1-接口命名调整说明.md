# Logo品牌设置：接口命名调整评审修改记录

原独立需求已按用户要求并入 Logo品牌设置期1。以下保留当时范围和验证记录，不另行验收。

## 目标与范围

用户要求落实刚讨论的改动：brandingState/stateFor 改为 brandingResponse/toBrandingResponse；设置接口使用 POST 的 /get、/update、/reset；合并过碎的 handler 测试。图片仍 GET，字段、数据、鉴权和操作语义不变。同步 frontend brandingApi、smoke 和文档。

L2 接口改动，用户已确认规则并明确要求实施；前端共享 brandingApi 的三个请求定义为本次必要接缝白名单，不改其他上游功能。工作树交付，不提交或推送。
