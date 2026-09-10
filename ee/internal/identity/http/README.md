# 身份HTTP接口

`handler.go`负责固定路径注册、严格JSON解码（含重复键拒绝）、同源检查、Cookie和安全错误响应。账号规则全部调用identity.Service，不在handler中写数据库。

新接口统一POST，兼容上游会话路径及其空体登出；公开品牌资源不属于本包。Cookie仅允许明确配置的外部origin，非回环部署必须HTTPS。不信任请求代理头，不返回原始会话凭据到JSON。
