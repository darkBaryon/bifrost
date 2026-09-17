# SQLite 单副本部署

部署文件位于项目根目录的 `deploy/` 下；应用清单需要手动执行，不会自动连接集群。

已确认：使用块存储或本地磁盘 PVC 保留 SQLite 数据，访问地址为 https://bifrost.zxiaowo.com。存储已选用 node1 的本地 XFS 目录 /data/ai-gateway，StorageClass 为 ai-gateway-local；镜像版本仍需填写。现有 nfs-provisioner 不用于数据库：项目配置库和日志库固定使用 WAL，SQLite 官方不支持 WAL 数据库使用网络文件系统。依据：[SQLite WAL](https://sqlite.org/wal.html)。

`ai-gateway-storage.yaml` 包含 ai-tool Namespace、专用 StorageClass、10Gi Local PV/PVC；`ai-gateway-k8s.yaml` 包含 Namespace、单副本 Deployment、ClusterIP Service。应用默认在 /app/data 创建 config.db 和 logs.db，无需 PostgreSQL 或 Redis。`ai-gateway-ingress.yaml` 是可选 HTTPS 入口模板。

## 1. 填写环境值

| 替换项 | 填什么 |
|---|---|
| REPLACE_IMAGE_TAG | Jenkins 实际推送成功的版本，例如 0.1.123；不要使用未经构建的示例版本 |
| 存储（已填写） | ai-gateway-local，绑定 node1:/data/ai-gateway；不要改成 nfs-provisioner |
| 域名（已填写） | bifrost.zxiaowo.com，Deployment 的 EE_PUBLIC_ORIGIN 和可选 Ingress 已保持一致 |
| replace-ingress-class | 可选 Ingress 使用的真实 IngressClass 名称 |

SQLite 保持 replicas=1 和 Recreate；更新会短暂中断服务。不要为该 Deployment 配置 HPA，也不要额外启动另一个实例共用此 PVC。ReadWriteOnce 约束节点访问模式，本身不保证只有一个 Pod 写入。

本地目录已在 node1 创建，属主为 1000:1000，权限为 0770，位于本地 XFS 文件系统。PV 通过节点亲和性绑定 node1，回收策略为 Retain。10Gi 是声明容量，并非目录硬配额；需监控真实磁盘空间。node1 故障时应用无法自动转移到其他节点，需恢复节点或从备份恢复数据。

存储清单可单独应用，不会启动业务应用：

```bash
kubectl apply -f ai-gateway-storage.yaml
kubectl -n ai-tool get pvc ai-gateway-data
kubectl get pv ai-gateway-local-pv
```

StorageClass 使用 WaitForFirstConsumer，首次没有消费 Pod 时 PVC 可能保持 Pending；消费 Pod 应使用 nodeSelector/节点亲和性或由调度器依据 PV 选节点，不要用 nodeName 绕过调度。不要删除现有 PVC 来切换存储，也不要删除 Namespace 重启应用。Retain 不等于备份，仍需定期做 SQLite 一致性备份。

集群连接使用桌面的 kubeconfig，且需指定证书名称（保留 CA 校验）：

```bash
alias kubectl='kubectl --kubeconfig=/Users/admin/Desktop/kubectl/config --tls-server-name=172.18.240.132'
```

注意：实测集群版本为 Kubernetes 1.14.2。现有业务模板的 startupProbe、networking.k8s.io/v1 Ingress 不兼容此版本；本次仅落地存储，启动业务前需先适配业务清单或升级集群，不要直接执行下文业务部署命令。

## 2. 准备 Secret

先确认操作的是预期集群，再准备命名空间：

```bash
kubectl config current-context
kubectl create namespace ai-tool --dry-run=client -o yaml | kubectl apply -f -
```

在 **ai-tool** 中通过现有运维方式创建以下 Secret，不要把真实密钥填入部署 YAML：

| Secret 名称 | 类型和内容 |
|---|---|
| ai-gateway-auth | Opaque，包含 BIFROST_SETUP_TOKEN 和 EE_INITIAL_PASSWORD 两个键；使用自行生成并妥善保存的随机值，不带尾部换行 |
| ai-gateway-registry | kubernetes.io/dockerconfigjson，包含 registry.cn-hangzhou.aliyuncs.com 的拉取凭据；也可将部署文件改为该命名空间中已有凭据名称 |
| ai-gateway-tls | 仅使用可选 Ingress 时需要，kubernetes.io/tls，含匹配实际域名的 tls.crt 和 tls.key |

BIFROST_SETUP_TOKEN 用于创建首个管理员；EE_INITIAL_PASSWORD 用于后续管理员建号或重置，不是跳过初始化的默认管理员密码。后者必须非空且不超过 72 字节。

## 3. 首次部署

在当前文件所在目录操作。确认已替换所有占位项后，先用目标集群验证，再应用：

```bash
kubectl apply --dry-run=server -f ai-gateway-k8s.yaml
kubectl apply -f ai-gateway-k8s.yaml
kubectl -n ai-tool get pvc ai-gateway-data
kubectl -n ai-tool rollout status deployment/ai-gateway --timeout=600s
kubectl -n ai-tool get pods -l app.kubernetes.io/name=ai-gateway
```

只有需要 Ingress 且集群已有对应控制器时执行：

```bash
kubectl apply --dry-run=server -f ai-gateway-ingress.yaml
kubectl apply -f ai-gateway-ingress.yaml
```

Ingress 不会自动安装控制器、申请证书或配置 DNS。将域名解析到实际入口。若使用已有外部 HTTPS 反代，将其接到 Service 的 8080 端口，确保模型流式请求和 WebSocket 的超时、缓冲设置适合业务。

## 4. 更新与数据保留

首次部署就绪后，可在原 Jenkins 流水线中开启 deploy，目标 namespace/Deployment/container 已匹配。首次部署等待上限使用 600 秒；原流水线日常 rollout 仍为 180 秒，两者不同，慢启动应先检查原因。

后续镜像版本由 Jenkins 更新，不要反复应用带旧版本的本地清单，否则会把镜像改回旧版；调整资源配置前先同步清单中的当前镜像标签。

SQLite 数据保存在 ai-gateway-data PVC，不在镜像层。普通发布保留 PVC；不要执行 `kubectl delete -f ai-gateway-k8s.yaml` 来重启应用，这会请求删除整个 ai-tool 命名空间及其中的 PVC。备份及历史认证升级遵循项目原恢复文档。

常见未就绪原因：PVC Pending 表示存储尚未绑定；ImagePullBackOff 检查镜像版本与拉取 Secret；CreateContainerConfigError 检查身份 Secret；应用启动失败检查外部 HTTPS origin、卷权限及脱敏日志。

## 本次验证边界

已连接集群检查 node1:/data 为本地 XFS，并创建专用目录、StorageClass、PV/PVC。已用 UID 1000、GID 0、fsGroup 1000 写入测试文件，删除写入 Pod 后由新 Pod 成功读取；测试文件及临时 Pod 已清理，PV/PVC 为 Bound。未创建业务 Secret、未启动 ai-gateway。镜像版本与 Ingress 配置尚需准备，业务模板还需适配 Kubernetes 1.14.2。

参考：[Kubernetes 持久卷](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) · [Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/)。
