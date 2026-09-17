# Jenkins NAS 缓存构建

需求：builder 容器执行 npm ci 和原有 make -C ee build，Go 与 npm 下载缓存保存到 NAS；根 Dockerfile 只封装编译产物。配置继续使用 develop 命名空间下的 Jenkins Agent、node2 构建节点、原有 SSH 凭据和阿里云仓库。

## 首次准备

1. 使用本目录完整 Jenkinsfile 替换 Jenkins 任务的 Pipeline Script；如果采用 SCM 加载，脚本路径为 deploy/Jenkinsfile。保留任务现有 branch、majorver、deploy 参数，并禁用并发构建。deploy 默认不启用，只构建并推送。
2. 同时提交根 Dockerfile、.dockerignore、.gitignore；不能只更新其中一侧。Dockerfile 不再接受纯源码直接编译，必须先生成 .jenkins-artifacts。
3. builder 镜像是私有镜像，须先在 node2 预拉取，或为 Pod 配置 develop 命名空间中有效的 imagePullSecrets。容器内的 /root/.docker 配置不用于 kubelet 拉取 Pod 镜像。
4. node2 应已有 docker:18.06.3-ce、roffe/kubectl:latest、jenkins/inbound-agent:4.3-4；所有容器使用 IfNotPresent。builder 必须是 AMD64 镜像，保留其版本标签不覆盖。

在 node2 上预拉取 builder（先通过现有凭据登录阿里云）：

```bash
sudo docker pull registry.cn-hangzhou.aliyuncs.com/yxdocker/ai-gateway:buildbase-go1.27-node25-v1-amd64
sudo docker image inspect registry.cn-hangzhou.aliyuncs.com/yxdocker/ai-gateway:buildbase-go1.27-node25-v1-amd64 --format '{{.Os}}/{{.Architecture}} {{.RepoDigests}}'
```

预期架构 linux/amd64，摘要 sha256:291a045df1c1d656583d3b2717bd74a05992183d3cbdca0d56d9355025105e20。若用 sudo，需确保登录凭据对 sudo docker 同样可用。

## NAS 目录及权限

NAS 挂载源为 2f2de4a311-mgj14.cn-shenzhen.nas.aliyuncs.com:/golib。builder 以 1000:1000 运行，与 jnlp 的工作区用户对齐。

| 缓存 | 容器路径 | NAS 路径 |
|---|---|---|
| Go 模块下载 | /cache/golib/ai-gateway | /golib/ai-gateway |
| npm 下载包 | /cache/golib/ai-gateway-npm | /golib/ai-gateway-npm |
| Go 编译缓存 | /tmp/go-build | 不保存到 NAS，随 Pod 删除 |

在已确认 /mnt 挂载该 NAS 根目录的 node1 上，仅准备这两个专用目录：

```bash
findmnt -T /mnt
sudo mkdir -p /mnt/golib/ai-gateway /mnt/golib/ai-gateway-npm
sudo chown 1000:1000 /mnt/golib/ai-gateway /mnt/golib/ai-gateway-npm
sudo chmod 0770 /mnt/golib/ai-gateway /mnt/golib/ai-gateway-npm
```

不要递归更改已有 /golib 的权限。若已有专用目录内部文件属于其他用户，需单独确认迁移权限；流水线在编译前进行实际写入探测。npm 使用 --prefer-offline，命中缓存仍重新安装 node_modules，缺少的包仍需联网。Go 也会下载新增依赖，不保证完全离线。缓存仅用于同一可信任务，禁止构建期间清空缓存。

## 构建及产物

顺序为 Checkout → 编译前后端 → Docker build → Docker push → 清理本次镜像 → 可选 Deploy。所有业务源码操作都在共享工作区 ai-gateway 子目录执行。

编译入口仍为 make -C ee build VERSION="$IMAGE_TAG"，前端嵌入 Go 二进制。验证产物存在后复制到 .jenkins-artifacts/main 和 .jenkins-artifacts/docker-entrypoint.sh。Docker 构建上下文按 .dockerignore 白名单只包含这两个产物及 Dockerfile。NAS 缓存、源码与凭据不进入镜像。

镜像版本仍为 majorver.BUILD_NUMBER，默认前缀 0.1。最终镜像校验 linux/amd64；清理仅针对本次 UUID 标签，不清理基础镜像或 NAS 缓存。最终 Alpine 运行镜像仍需安装少量运行库，本次未制作独立运行基础镜像。

部署目标仍为 ai-tool 命名空间中的 ai-gateway Deployment 和同名容器。deploy=true 只更新已有 Deployment，不创建资源。部署清单已适配 Kubernetes 1.14.2；首次创建资源和准备 Secret 见 K8s部署步骤.md。

## 验证边界

2026-09-17：Pod YAML 解析、挂载引用、节点选择、全部嵌入 shell 的 sh -n 检查通过。隔离替身测试 7 项通过：正常编译产物交接、架构错误、npm 失败、make 失败、UI 产物缺失、二进制缺失、缓存目录不可用；确认已有缓存文件未被删除。git diff --check 通过。

上述行为测试使用临时命令替身，不等于真实前后端编译。真实 Jenkins 的 Groovy/CPS、NAS 权限和完整前后端编译需以首次 Jenkins 运行验收；本次未运行 Jenkins、未重新构建业务镜像、未部署集群。修改未提交 Git，无数据库变更。

回滚时同时恢复旧流水线、Dockerfile 和 .dockerignore，即可回到 Docker 内编译。NAS 缓存保留，不涉及数据库变更。
