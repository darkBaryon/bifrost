# SQLite 单副本首次部署

本次发布镜像为 registry.cn-hangzhou.aliyuncs.com/yxdocker/ai-gateway:0.0.1.11，域名为 https://bifrost.zxiaowo.com。清单已适配实测 Kubernetes 1.14.2 与 nginx-ingress-controller 0.24.1，不使用 startupProbe、ingressClassName 或 networking.k8s.io/v1 Ingress。

存储已创建：ai-tool/ai-gateway-data PVC 为 Bound，绑定 ai-gateway-local-pv，实际目录 node1:/data/ai-gateway，XFS、1000:1000、0770；PV 回收策略 Retain，声明 10Gi（不是目录硬配额）。node1 故障时不能自动切换到其他节点。不要把 SQLite WAL 放到 NFS；NAS 构建缓存与数据库数据目录相互独立。

## 1. 连接并确认集群

以下命令在你的 Mac 终端执行，先定义函数，后续所有命令都通过该函数明确使用目标 kubeconfig，保留 CA 校验：

```bash
cd /Users/admin/work/ai-gateway/deploy
k() {
  kubectl --kubeconfig=/Users/admin/Desktop/kubectl/config \
    --tls-server-name=172.18.240.132 "$@"
}
k config current-context
k get node node1
k -n ai-tool get pvc ai-gateway-data
```

当前 PVC 已就绪，不需要重新创建存储。ai-gateway-storage.yaml 保留用于恢复配置参考；不要删除 PVC 或清空节点目录。Deployment 为单副本 Recreate，更新会短暂中断，禁止配置 HPA 或额外实例共用 SQLite。

## 2. 首次准备三个 Secret

检查已有 Secret；下面的 create 命令仅在对应名称不存在时执行，不覆盖现有密钥：

```bash
k -n ai-tool get secrets
```

复制镜像拉取凭据。已核实 default/registry-aliyun 配置的仓库地址为 registry.cn-hangzhou.aliyuncs.com，实际拉取授权仍需由 Pod 拉取验证。命令只转移 type/data，不复制旧 namespace、UID 或 annotations，不将凭据写入文件：

```bash
k -n default get secret registry-aliyun -o json |
  python3 -c 'import json,sys; s=json.load(sys.stdin); print(json.dumps({"apiVersion":"v1","kind":"Secret","metadata":{"name":"ai-gateway-registry","namespace":"ai-tool"},"type":s["type"],"data":s["data"]}))' |
  k create -f -
```

复制 HTTPS 证书。已核实 default/zxiaowo 的公开证书覆盖 *.zxiaowo.com，有效至 2027-02-05。跨命名空间复制后，源证书续期不会自动更新目标 Secret，需同步续期：

```bash
k -n default get secret zxiaowo -o json |
  python3 -c 'import json,sys; s=json.load(sys.stdin); print(json.dumps({"apiVersion":"v1","kind":"Secret","metadata":{"name":"ai-gateway-tls","namespace":"ai-tool"},"type":s["type"],"data":s["data"]}))' |
  k create -f -
```

生成初始化令牌和后续建号/重置密码，使用临时受限文件传给 kubectl，成功或失败都会清理临时文件。不要开启 shell 命令跟踪或把密钥复制到仓库：

```bash
(
  set -eu
  umask 077
  secret_dir="$(mktemp -d)"
  trap 'rm -f "$secret_dir/auth.env"; rmdir "$secret_dir"' EXIT
  {
    printf 'BIFROST_SETUP_TOKEN='
    openssl rand -hex 32
    printf 'EE_INITIAL_PASSWORD='
    openssl rand -hex 24
  } > "$secret_dir/auth.env"
  k -n ai-tool create secret generic ai-gateway-auth \
    --from-env-file="$secret_dir/auth.env"
)
```

BIFROST_SETUP_TOKEN 用于首次创建管理员。EE_INITIAL_PASSWORD 是后续管理员建号或重置使用的密码，不是免初始化默认登录密码；生成值为 48 个 ASCII 字符，小于应用的 72 字节上限。请妥善保管这些值，不要发送到聊天中。

## 3. 创建应用与入口

在执行以下命令前确认三个 Secret 均已存在，以及 bifrost.zxiaowo.com 的 DNS 指向实际 Nginx Ingress 入口。清单不会自动配置 DNS，也不会自动申请证书。若已有外部 HTTPS 反代，可不应用 Ingress 文件，但仍需保证外部 origin 为 https://bifrost.zxiaowo.com。

```bash
k -n ai-tool get secret ai-gateway-auth ai-gateway-registry ai-gateway-tls
k apply --dry-run=server -f ai-gateway-k8s.yaml -f ai-gateway-ingress.yaml
k apply -f ai-gateway-k8s.yaml
k -n ai-tool rollout status deployment/ai-gateway --timeout=600s
k apply -f ai-gateway-ingress.yaml
k -n ai-tool get pods,service,ingress
k -n ai-tool get endpoints ai-gateway
```

以上 dry-run 需要支持 --dry-run=server 的客户端，建议使用 Mac 上的 kubectl；不要在旧版 roffe/kubectl 容器中照搬该参数。服务端已实测支持 dry-run。

就绪探针从启动 10 秒后开始检查 /health，只有通过才接收 Service 流量；存活探针延迟 300 秒以留出首次建库时间。两者 timeoutSeconds=12。Ingress 使用旧版 extensions/v1beta1 格式与 nginx class 注解，关闭响应缓冲，读写超时为 600 秒，以支持模型流式响应。

部署成功后访问 https://bifrost.zxiaowo.com 进行首个管理员初始化。需要在自己的终端读取初始化令牌时执行（输出属于敏感信息，不要贴日志或聊天）：

```bash
k -n ai-tool get secret ai-gateway-auth -o 'jsonpath={.data.BIFROST_SETUP_TOKEN}' | base64 --decode
printf '\n'
```

## 4. 后续更新与排查

首次资源创建并验证成功后，再开启 Jenkins 的 deploy 参数，目标仍为 ai-tool / ai-gateway / ai-gateway。Jenkins 只更新镜像，不负责首次创建资源。流水线 rollout 等待为 180 秒，首次手工部署为 600 秒。

后续手工 apply 前，应将清单镜像版本同步到正在发布的版本，避免把 Jenkins 更新过的镜像回退到 0.0.1.11。回滚镜像可用 kubectl rollout undo，但若版本包含数据库迁移，应先确认数据库向后兼容并准备备份。

```bash
k -n ai-tool describe pod -l app.kubernetes.io/name=ai-gateway
k -n ai-tool logs deployment/ai-gateway --tail=100
k -n ai-tool get events --sort-by=.metadata.creationTimestamp
```

ImagePullBackOff 检查 node1 到阿里云仓库的网络与凭据；CreateContainerConfigError 检查 Secret 名称与键；Pending 检查 node1、PVC 和节点资源；CrashLoopBackOff 查看应用日志。Retain 不等于备份，仍需做 SQLite 一致性备份。不要通过删除 ai-tool Namespace、PVC 或 PV 重启应用。

## 本次检查记录

已只读确认存储绑定、node1 为 Ready/amd64、Ingress 控制器版本、源镜像凭据仓库地址和公开证书域名/有效期。0.0.1.11 镜像 manifest 存在，平台 linux/amd64，摘要 sha256:6be49fadc76a48ebc94679b6039adefc8749f472bd20fb524f50de1ef275107e。Deployment、Service、Ingress 已通过目标集群服务端 dry-run；本地资源引用、域名一致性及文档 shell 语法检查通过。

未实际创建业务资源或 Secret，未验证 Pod 镜像拉取、应用启动、数据库迁移、DNS 或 HTTPS 外部访问。本次未改存储清单、未提交 Git。
