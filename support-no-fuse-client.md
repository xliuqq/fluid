# CacheRuntime 支持无 FUSE Client 架构

## 1. 背景

### 1.1 当前架构

Fluid 的 CacheRuntime 目前支持三种组件架构：**Master + Worker + Client**。其中 `Client` 组件作为 FUSE 守护进程，提供 POSIX 兼容的数据访问挂载点。运行时配置在 reconcile 期间生成，并存储在 ConfigMap（`fluid-runtime-config-{name}`）中，包含 `runtime.json`，该文件会挂载到所有组件的 Pod 中的 `/etc/fluid/config/runtime.json`。

关键代码参考：
- 运行时配置生成：`pkg/ddc/cache/engine/cm.go:102-193`（`generateRuntimeConfigData`）
- 配置文件名：`pkg/ddc/cache/engine/util.go:73-75`（`getRuntimeConfigFileName` 返回 `"runtime.json"`）
- ConfigMap 挂载：`pkg/ddc/cache/engine/transform.go:140-169`（`transformRuntimeConfigVolume`）
- ConfigMap 名称：`common.GetCacheRuntimeConfigConfigMapName()` → `fluid-runtime-config-{name}`

### 1.2 Mooncake 架构

Mooncake 是一种专为高性能 LLM 推理服务设计的 KV-cache 存储系统，利用 RDMA 加速。与传统 Fluid 运行时（Alluxio、JindoFS 等）不同，Mooncake **不使用 FUSE** 进行数据访问，而是提供原生客户端 SDK，直接读取配置。

这导致了与当前 CacheRuntime 架构的根本性不匹配：
- `Client` 组件（FUSE 守护进程）对于 Mooncake 而言是不必要的开销
- 运行时配置需要采用 Mooncake 原生客户端所要求的不同格式（`runtime.sh`）
- App Pod 需要通过 webhook 注入运行时配置，而非依赖基于 PVC 的数据访问方式

### 1.3 问题陈述

当前 CacheRuntime 需要通过 `Client` 组件来支持基于 FUSE 的数据访问。对于 Mooncake 等无 FUSE 缓存系统，我们需要支持一种**无 FUSE Client** 架构，具体要求如下：
1. `Client` 组件可以被禁用（已通过 `Spec.Client.Disabled: true` 支持）
2. 运行时配置以 `runtime.sh` 形式额外提供（除了 `runtime.json`）
3. 通过通用的 webhook 插件将运行时配置注入到 App Pod
4. **runtime 仍然创建 PV/PVC**，但 App Pod 无需指定数据集卷

### 1.4 关键设计决策

1. **`fluid.io/dataset` 是一个注解**（而非标签），用于标识 Pod 使用的数据集。多个数据集以逗号分隔的方式指定。
2. **`fluid.io/inject: "true"` 是一个通用标签**，用于触发 webhook 的 mutate 操作，不区分 serverful 或 serverless。
3. **webhook 插件是通用的**（而非 Mooncake 专用）—— 它可以处理任意无 FUSE 运行时，注入运行时配置 ConfigMap。
4. **仍保留 PV/PVC 的创建** —— 并非 Worker 组件实际需要，而是为了将代码改动控制在最小范围。
5. **App Pod 不需要数据集卷** —— 它们只需要运行时配置来连接到缓存集群。

---

## 2. 设计

### 2.1 概述

本设计引入两个关键变更：

1. **双配置文件生成**：在 CacheRuntime reconcile 期间，同时在同一个 ConfigMap 中生成 `runtime.json`（向后兼容）和 `runtime.sh`（用于 Mooncake 等无 FUSE 运行时）。
2. **通用运行时配置注入插件**：新增一个 webhook 插件，检测带有 `fluid.io/inject: "true"` 标签的 Pod，并将运行时配置 ConfigMap 的 `runtime.sh` 作为文件注入到这些 Pod 中。

### 2.2 架构图

```
┌─────────────────────────────────────────────────────────────────────────┐
│                     CacheRuntime Reconcile（后台执行）                  │
│                                                                         │
│  pkg/ddc/cache/engine/cm.go: generateRuntimeConfigData()                │
│  ┌──────────────────────┐      ┌──────────────────────┐                │
│  │ runtime.json         │      │ runtime.sh           │                │
│  │（JSON 格式，现有）    │      │（Shell 脚本，新增）  │                │
│  │ - 用于 Alluxio/Jindo │      │ - 用于 Mooncake 等   │                │
│  │   等带 FUSE 的运行时  │      │   无 FUSE 的运行时    │                │
│  └──────────┬───────────┘      └──────────┬───────────┘                │
│             │                              │                            │
│             └──────────────┬───────────────┘                            │
│                            ▼                                            │
│                 ┌──────────────────────┐                               │
│                 │ ConfigMap            │                               │
│                 │ fluid-runtime-       │                               │
│                 │ config-{运行时名称}   │                               │
│                 │   - runtime.json     │                               │
│                 │   - runtime.sh       │                               │
│                 └──────────┬───────────┘                               │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│                        Pod 创建（App Pod）                               │
│                                                                         │
│  Pod 标签/注解：                                                        │
│    fluid.io/inject: "true"             → 触发 webhook                  │
│    fluid.io/dataset: "数据集名称"      → 标识数据集（可选）               │
│    （多个时： "ds1,ds2"）                                               │
│                                                                         │
│                            │                                            │
│                            ▼                                            │
│                 ┌──────────────────────┐                               │
│                 │ Fluid Mutating        │                               │
│                 │ Webhook               │                               │
│                 └──────────┬───────────┘                               │
│                            │                                            │
│                            ▼                                            │
│                 ┌──────────────────────┐                               │
│                 │ RuntimeConfigInjector │                               │
│                 │（新增 webhook 插件）   │                               │
│                 └──────────┬───────────┘                               │
│                            │                                            │
│                            ▼                                            │
│                 ┌──────────────────────┐                               │
│                 │ 注入到 Pod：          │                               │
│                 │   卷：                │                               │
│                 │     fluid-runtime-   │                               │
│                 │     {运行时名}-config │                               │
│                 │   挂载路径：          │                               │
│                 │     /etc/fluid/config │                               │
│                 │   环境变量：          │                               │
│                 │     FLUID_RUNTIME_   │                               │
│                 │     CONFIG_PATH       │                               │
│                 └──────────────────────┘                               │
└─────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────┐
│                     组件 Pod（Master/Worker）                            │
│                                                                         │
│  无需改动 —— 继续通过现有的 transform 逻辑挂载                            │
│  ConfigMap                                                              │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.3 组件详情

#### 2.3.1 双配置文件生成（后端）

**文件**：`pkg/ddc/cache/engine/cm.go`

修改 `generateRuntimeConfigData()` 以同时生成 `runtime.sh`：

```go
func (e *CacheEngine) generateRuntimeConfigData(ctx context.Context, runtime *datav1alpha1.CacheRuntime) (map[string]string, error) {
    // ... 现有代码构建配置 ...

    b, _ := json.Marshal(config)
    data := map[string]string{
        e.getRuntimeConfigFileName(): string(b),
        // 新增：为无 FUSE 运行时生成 runtime.sh
        // JSON -> Shell 变量导出示例：
        // {"mounts":[{"name":"default","path":"/dataset/default","mountPoint":"hdfs://..."}],
        //  "master":{"enabled":true,"name":"mooncake-master","replicas":1},
        //  "worker":{"enabled":true,"name":"mooncake-worker","replicas":3},
        //  "targetPath":"/runtime-mnt/cache/fluid/cache-fuse"}
        // ↓
        // export MOUNTS_COUNT=1
        // export MOUNT_0_NAME="default"
        // export MOUNT_0_PATH="/dataset/default"
        // export MOUNT_0_MOUNTPOINT="hdfs://..."
        // export MASTER_ENABLED="true"
        // export MASTER_NAME="mooncake-master"
        // export MASTER_REPLICAS="1"
        // export RUNTIME_TARGETPATH="/runtime-mnt/cache/fluid/cache-fuse"
        e.getRuntimeShFileName(): e.generateRuntimeSh(config),
    }
    return data, nil
}
```

在 `util.go` 中添加 `getRuntimeShFileName()` 和 `generateRuntimeSh()` 两个辅助方法，将 JSON 配置转换为 Shell 脚本格式（`export KEY="VALUE"` 形式）。

##### 命名约定

`generateRuntimeSh()` 将 JSON 结构扁平化为 shell 变量，命名规则如下：

| JSON 类型 | 命名规则 | 示例 |
|-----------|---------|------|
| 标量（string/bool/int） | `PREFIX_CASE="value"` | `MASTER_ENABLED="true"` |
| 数组元素 | `PREFIX_INDEX_FIELD="value"` | `MOUNT_0_NAME="default"` |
| 数组计数 | `PREFIX_COUNT=N` | `MOUNTS_COUNT=1` |
| 嵌套对象 | `PREFIX_SUBFIELD="value"` | `MASTER_SERVICE_NAME="mooncake-master-svc"` |
| Map 类型 | `PREFIX_OPTIONS='{"k":"v"}'` | `MASTER_OPTIONS='{"key":"val"}'` |

嵌套层级通过连续下划线分隔，数组索引通过 `_N` 表示。复杂类型（map、嵌套数组）编码为 JSON 字符串。

#### 2.3.2 无 FUSE Client 支持

**文件**：`pkg/ddc/cache/engine/volume.go`

PV/PVC 的创建逻辑保持不变，仅为了将代码改动控制在最小范围，无需修改。仅在 Client 被禁用时跳过客户端相关的初始化。

```go
func (e *CacheEngine) CreateVolume(ctx context.Context) (err error) {
    // 即使 Client 被禁用（无 FUSE 架构），也保留 PV/PVC 的创建，以最小化代码改动
    if err = e.createFusePersistentVolume(ctx); err != nil {
        return err
    }
    if err = e.createFusePersistentVolumeClaim(ctx); err != nil {
        return err
    }
    return nil
}
```

**文件**：`pkg/ddc/cache/engine/setup.go`

Setup 已正确处理 `Client.Disabled` —— 仅在 `runtimeValue.Client.Enabled` 为 true 时才设置客户端组件。

**文件**：`pkg/ddc/cache/engine/client.go`

无需改动 —— `SetupClientComponent()` 和 `ShouldSetupClient()` 已能正确处理禁用情况。

#### 2.3.3 通用运行时配置注入 Webhook 插件

**新增文件**：`pkg/webhook/plugins/runtimeconfiginjector/runtime_config_injector.go`

```go
package runtimeconfiginjector

import (
    "fmt"
    "strings"

    "github.com/fluid-cloudnative/fluid/pkg/common"
    "github.com/fluid-cloudnative/fluid/pkg/ddc/base"
    "github.com/fluid-cloudnative/fluid/pkg/webhook/plugins/api"
    corev1 "k8s.io/api/core/v1"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

const Name = "RuntimeConfigInjector"

type RuntimeConfigInjector struct {
    client client.Client
    name   string
}

var _ api.MutatingHandler = &RuntimeConfigInjector{}

func NewPlugin(c client.Client, args string) (api.MutatingHandler, error) {
    return &RuntimeConfigInjector{
        client: c,
        name:   Name,
    }, nil
}

func (p *RuntimeConfigInjector) GetName() string { return p.name }

func (p *RuntimeConfigInjector) Mutate(pod *corev1.Pod, runtimeInfos map[string]base.RuntimeInfoInterface) (bool, error) {
    // 通过 fluid.io/inject 标签触发（通用标签，不区分 serverful/serverless）
    if pod.Labels[common.Inject] != common.True {
        return false, nil
    }
    datasetStr, ok := pod.Annotations[common.LabelAnnotationDataset]
    if !ok || datasetStr == "" {
        return false, nil
    }
    for _, ds := range strings.Split(datasetStr, ",") {
        ds = strings.TrimSpace(ds)
        if ds == "" {
            continue
        }
        cmName := common.GetCacheRuntimeConfigConfigMapName(ds)
        // 验证 ConfigMap 存在
        if _, err := p.client.Get(pod.Context, client.ObjectKey{Namespace: pod.Namespace, Name: cmName}, &corev1.ConfigMap{}); err != nil {
            continue
        }
        // 构造卷名，避免与引擎内部 getRuntimeConfigVolumeName() 冲突
        volumeName := fmt.Sprintf("fluid-runtime-config-%s", ds)
        pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
            Name: volumeName,
            VolumeSource: corev1.VolumeSource{
                ConfigMap: &corev1.ConfigMapVolumeSource{
                    LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
                },
            },
        })
        if len(pod.Spec.Containers) > 0 {
            pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts,
                corev1.VolumeMount{Name: volumeName, MountPath: common.RuntimeConfigMountPath, ReadOnly: true})
            pod.Spec.Containers[0].Env = append([]corev1.EnvVar{{
                Name:  common.RuntimeConfigPathEnvName,
                Value: common.RuntimeConfigMountPath + "/" + common.RuntimeShFileName,
            }}, pod.Spec.Containers[0].Env...)
        }
        pod.Labels[common.InjectSidecarDone] = common.True
        return true, nil
    }
    return false, nil
}
```

#### 2.3.4 路由逻辑变更

**文件**：`pkg/webhook/handler/mutating/mutating_handler.go`

需要两处变更：

##### 1) 新增从 annotation 收集 runtimeInfo

当前 `CollectRuntimeInfosFromPVCs` 只从 PVC 收集。对于无 FUSE 的 Pod，没有 PVC 但有 `fluid.io/dataset` 注解，需要额外从注解收集：

```go
// 新增辅助函数，从 fluid.io/dataset 注解收集 runtimeInfo
func CollectRuntimeInfosFromAnnotations(client client.Reader, pod *corev1.Pod) (map[string]base.RuntimeInfoInterface, error) {
    datasetStr, ok := pod.Annotations[common.LabelAnnotationDataset]
    if !ok || datasetStr == "" {
        return nil, nil
    }
    runtimeInfos := make(map[string]base.RuntimeInfoInterface)
    for _, ds := range strings.Split(datasetStr, ",") {
        ds = strings.TrimSpace(ds)
        if ds == "" {
            continue
        }
        ri, err := base.GetRuntimeInfo(client, ds, pod.Namespace)
        if err != nil {
            continue
        }
        runtimeInfos[ds] = ri
    }
    return runtimeInfos, nil
}
```

在 `MutatePod()` 中合并两种收集方式：

```go
pvcNames := kubeclient.GetPVCNamesFromPod(pod)
runtimeInfos, err := webhookutils.CollectRuntimeInfosFromPVCs(handlerClient, pvcNames, pod.Namespace, setupLog,
    utils.SkipPrecheckEnable(pod.Annotations))
if err != nil {
    return err
}
// 新增：从 annotation 补充 runtimeInfo
annotationInfos, err := CollectRuntimeInfosFromAnnotations(handlerClient, pod)
if err != nil {
    return err
}
for k, v := range annotationInfos {
    runtimeInfos[k] = v
}
```

##### 2) 新增 `fluid.io/inject` 路由分支

```go
switch {
case utils.InjectEnabled(pod.GetLabels()):
    // 通用注入：根据 runtimeInfos 是否为空选择 handler 组
    if len(runtimeInfos) == 0 {
        pluginsList = pluginsRegistry.GetServerlessPodWithoutDatasetHandler()
    } else {
        pluginsList = pluginsRegistry.GetServerlessPodWithDatasetHandler()
    }
case utils.ServerlessEnabled(pod.GetLabels()):
    // 现有 serverless 逻辑...
case utils.ServerfulFuseEnabled(pod.GetLabels()):
    // 现有 serverful 逻辑...
}
```

在 `pkg/utils/annotations.go` 中添加辅助方法：

```go
func InjectEnabled(infos map[string]string) (match bool) {
    return enabled(infos, common.Inject)
}
```

#### 2.3.5 插件注册

**文件**：`pkg/webhook/plugins/plugins_impl.go`

注册新插件：

```go
_ = registry.Register(runtimeconfiginjector.Name, runtimeconfiginjector.NewPlugin)
```

在 `charts/fluid/fluid/values.yaml` 中配置插件到 `serverless.withDataset` handler 组：

```yaml
webhook:
  pluginsProfile:
    plugins:
      serverful:
        withDataset: []
        withoutDataset: []
      serverless:
        withDataset:
          - FilePrefetcher
          - FuseSidecar
          - DatasetUsageInjector
          - RuntimeConfigInjector  # 无 FUSE 运行时新增插件
        withoutDataset:
          - FilePrefetcher
          - FuseSidecar
          - DatasetUsageInjector
          - RuntimeConfigInjector  # 无 FUSE 运行时新增插件
```

##### 插件行为分析

对于 no-fuse app pod（`fluid.io/inject: "true"` + `fluid.io/dataset: "..."` + 无 PVC），
以下现有插件的行为：

| 插件 | runtimeInfos | 行为 | 影响 |
|------|-------------|------|------|
| `RuntimeConfigInjector` | 非空 | ✅ 注入 runtime.sh | 期望行为 |
| `DatasetUsageInjector` | 非空 | ✅ 添加 datasets-in-use 注解 | 无害 |
| `PreferNodesWithoutCache` | 非空 | ❌ 跳过（有 dataset） | 正确 |
| `FuseSidecar` | 非空 | ⚠️ 进入循环，注入 FUSE 容器 | ❌ **不能注册** |
| `FilePrefetcher` | 非空 | 检查 annotation，默认不触发 | 安全 |
| `RequireNodeWithFuse` | 非空 | ✅ 添加 FuseNodeSelector | ⚠️ 不必要 |
| `MountPropagationInjector` | 非空 | ✅ 注入 mountPropagation | ⚠️ 不必要 |
| `NodeAffinityWithCache` | 非空 | ✅ 注入亲和性 | ⚠️ 可能影响调度 |

**结论**：
- 只需注册 `RuntimeConfigInjector` 到 `serverlessPodWithDatasetHandler`
- **不能**注册 `FuseSidecar`（会注入 FUSE 容器，与 no-fuse 目标矛盾）
- 其他插件（`RequireNodeWithFuse`、`MountPropagationInjector`、`NodeAffinityWithCache`）虽然会执行，但危害有限，可以不注册以避免不必要的副作用

#### 2.3.6 常量定义

**文件**：`pkg/common/constants.go`

添加新常量：

```go
// Inject 是通用注入标签，不区分 serverful/serverless
const Inject = "fluid.io/inject"

// RuntimeConfigMountPath 是无 FUSE 运行时的运行时配置挂载路径
const RuntimeConfigMountPath = "/etc/fluid/config"

// RuntimeConfigPathEnvName 是运行时配置路径环境变量名
const RuntimeConfigPathEnvName = "FLUID_RUNTIME_CONFIG_PATH"

// RuntimeShFileName 是运行时 Shell 脚本文件名
const RuntimeShFileName = "runtime.sh"
```

#### 2.3.7 Webhook 配置

**文件**：`charts/fluid/fluid/templates/webhook/webhookconfiguration.yaml`

新增 webhook 规则，匹配带有 `fluid.io/inject: "true"` 标签的 Pod：

```yaml
- name: runtimeconfig.fluid.io
  rules:
    - apiGroups:   [""]
      apiVersions: ["v1"]
      operations:  ["CREATE"]
      resources:   ["pods"]
  clientConfig:
    service:
      namespace: {{ include "fluid.namespace" . }}
      name: fluid-pod-admission-webhook
      path: "/mutate-fluid-io-v1alpha1-schedulepod"
      port: 9443
  timeoutSeconds: {{ .Values.webhook.timeoutSeconds }}
  failurePolicy: Fail
  reinvocationPolicy: {{ .Values.webhook.reinvocationPolicy }}
  sideEffects: None
  admissionReviewVersions: ["v1","v1beta1"]
  objectSelector:
    matchLabels:
      fluid.io/inject: "true"
```

### 2.4 Pod 突变流程

```
1. App Pod 创建，带有：
   标签：
     fluid.io/inject: "true"
   注解：
     fluid.io/dataset: "mooncake-dataset"
     （多个时： "ds1,ds2"）
   PVC：无

2. Webhook 接收准入请求

3. 调用 FluidMutatingHandler.MutatePod()（mutating_handler.go:131）

4. PVC 收集：GetPVCNamesFromPod(pod) → 返回空（无 PVC）
   CollectRuntimeInfosFromPVCs → runtimeInfos = {} (空)

5. 新增：从 annotation 收集 runtimeInfo
   CollectRuntimeInfosFromAnnotations(pod)
   → fluid.io/dataset: "mooncake-dataset"
   → GetRuntimeInfo("mooncake-dataset", namespace)
   → runtimeInfos = {"mooncake-dataset": CacheRuntimeInfo}

6. 插件选择（mutating_handler.go:156-169）：
   - InjectEnabled(pod.Labels) → true（因 fluid.io/inject: "true"）
   - len(runtimeInfos) > 0 → GetServerlessPodWithDatasetHandler()

7. 插件列表包含 RuntimeConfigInjector（来自 plugins.profile 的 serverless.withDataset）

8. RuntimeConfigInjector.Mutate() 执行：
   - 检查 fluid.io/inject: "true" → 匹配
   - 检查 fluid.io/dataset 注解 → "mooncake-dataset"
   - 查找 ConfigMap：fluid-runtime-config-mooncake-dataset
   - 将卷和挂载注入到 Pod 规格中
   - 设置 done.sidecar.fluid.io/inject 标签
   - 返回 shouldStop=true

9. Pod 完成突变，注入运行时配置
```

---

## 3. 实现计划

### 3.1 阶段一：双配置文件生成

**需修改的文件**：
- `pkg/ddc/cache/engine/cm.go` —— 添加 `runtime.sh` 生成逻辑
- `pkg/ddc/cache/engine/util.go` —— 添加 `getRuntimeShFileName()` 和 `generateRuntimeSh()`
- `pkg/ddc/cache/engine/cm_test.go` —— 添加 `runtime.sh` 生成测试

**步骤**：
1. 为 `CacheEngine` 添加 `generateRuntimeSh()` 方法
2. 修改 `generateRuntimeConfigData()`，将 `runtime.sh` 包含到 ConfigMap 数据中
3. 添加单元测试，验证 Shell 脚本格式正确性
4. 确认现有测试仍然通过

### 3.2 阶段二：无 FUSE Client 支持

**需修改的文件**：
- `pkg/ddc/cache/engine/volume.go` —— 验证无 FUSE 场景下的 PV/PVC 创建（无需改动）
- `pkg/ddc/cache/engine/setup.go` —— 验证客户端跳过逻辑（无需改动）
- `pkg/ddc/cache/engine/client.go` —— 验证禁用客户端的处理（无需改动）

**步骤**：
1. 验证 `CreateVolume()` 在 Client 禁用时正常工作
2. 确保无 FUSE 架构相关测试通过
3. 添加无 FUSE CacheRuntime 的集成测试

### 3.3 阶段三：RuntimeConfigInjector Webhook 插件

**需新建/修改的文件**：
- `pkg/webhook/plugins/runtimeconfiginjector/runtime_config_injector.go` —— 新插件
- `pkg/webhook/plugins/runtimeconfiginjector/runtime_config_injector_test.go` —— 单元测试
- `pkg/webhook/plugins/plugins_impl.go` —— 注册插件
- `pkg/common/constants.go` —— 添加常量
- `pkg/utils/annotations.go` —— 添加 `InjectEnabled()` 辅助方法
- `pkg/webhook/handler/mutating/mutating_handler.go` —— 新增路由分支 + 从 annotation 收集 runtimeInfo
- `pkg/webhook/utils/runtime_info.go` —— 新增 `CollectRuntimeInfosFromAnnotations()` 辅助函数

**步骤**：
1. 在 `pkg/common/constants.go` 中添加常量
2. 在 `pkg/utils/annotations.go` 中添加 `InjectEnabled()`
3. 在 `pkg/webhook/utils/runtime_info.go` 中添加 `CollectRuntimeInfosFromAnnotations()`
4. 在 `mutating_handler.go` 中：
   - 调用 `CollectRuntimeInfosFromAnnotations()` 补充 runtimeInfo
   - 新增 `fluid.io/inject` 路由分支
5. 创建 RuntimeConfigInjector 插件
6. 在 `plugins_impl.go` 中注册插件
7. 编写单元测试
8. 添加集成测试

### 3.4 阶段四：端到端测试

**步骤**：
1. 部署一个 `Client.Disabled: true` 的 Mooncake CacheRuntime
2. 验证 `runtime.json` 和 `runtime.sh` 均正确生成
3. 创建带有 `fluid.io/inject: "true"` 和 `fluid.io/dataset: "mooncake-ds"` 的 Pod
4. 验证 webhook 正确注入了 ConfigMap 卷
5. 验证 Mooncake 客户端能够读取配置并成功连接

---

## 4. API 变更

### 4.1 CacheRuntimeSpec

无需变更。现有 `Spec.Client.Disabled: true` 字段已足够禁用 FUSE 客户端。

### 4.2 CacheRuntimeClass

无需变更。

### 4.3 App Pod 上的新标签和注解

| 键 | 类型 | 值 | 说明 |
|-----|------|-------|-------------|
| `fluid.io/inject` | 标签 | `"true"` | 触发 webhook 突变（通用，不区分 serverful/serverless） |
| `fluid.io/dataset` | 注解 | `"数据集名称"` 或 `"ds1,ds2"` | 标识 Pod 使用的数据集 |

---

## 5. ConfigMap 数据结构

### 5.1 runtime.json（现有）

```json
{
  "mounts": [
    {
      "name": "default",
      "mountPoint": "hdfs://...",
      "path": "/dataset/default",
      "readOnly": false
    }
  ],
  "accessModes": ["ReadOnlyMany"],
  "targetPath": "/var/lib/fluid/cache/dataset-name/cache-fuse",
  "master": {
    "enabled": true,
    "name": "mooncake-master",
    "replicas": 1
  },
  "worker": {
    "enabled": true,
    "name": "mooncake-worker",
    "replicas": 3
  }
}
```

### 5.2 runtime.sh（新增）

```sh
#!/bin/sh
# Auto-generated by Fluid CacheRuntime
# source /etc/fluid/config/runtime.sh

# 顶层简单值
export RUNTIME_TARGETPATH="/runtime-mnt/cache/fluid-ns/cache-fuse"

# mounts 数组（2 个数据集）
export MOUNTS_COUNT=2
export MOUNT_0_NAME="default"
export MOUNT_0_PATH="/dataset/default"
export MOUNT_0_MOUNTPOINT="hdfs://namenode:9000/dataset/default"
export MOUNT_0_READONLY="false"
export MOUNT_0_SHARED="false"
export MOUNT_0_OPTIONS='{}'
export MOUNT_1_NAME="model"
export MOUNT_1_PATH="/dataset/model"
export MOUNT_1_MOUNTPOINT="hdfs://namenode:9000/dataset/model"
export MOUNT_1_READONLY="true"
export MOUNT_1_SHARED="false"
export MOUNT_1_OPTIONS='{"cache.type":"none"}'

# accessModes 数组
export ACCESSMODES_COUNT=1
export ACCESSMODES_0="ReadOnlyMany"

# master 组件
export MASTER_ENABLED="true"
export MASTER_NAME="mooncake-master"
export MASTER_REPLICAS="1"
export MASTER_SERVICE_NAME="mooncake-master-svc"
export MASTER_OPTIONS='{"bootstrap.mode":"all"}'

# worker 组件
export WORKER_ENABLED="true"
export WORKER_NAME="mooncake-worker"
export WORKER_REPLICAS="3"
export WORKER_SERVICE_NAME="mooncake-worker-svc"
export WORKER_OPTIONS='{}'
export WORKER_TIEREDSTORELEVELS_COUNT=2
export WORKER_TIEREDSTORELEVELS_0_MEDIUMTYPE="MEM"
export WORKER_TIEREDSTORELEVELS_0_MOUNTPATHS='["/dev/shm"]'
export WORKER_TIEREDSTORELEVELS_0_HIGH="0.9"
export WORKER_TIEREDSTORELEVELS_0_LOW="0.7"
export WORKER_TIEREDSTORELEVELS_1_MEDIUMTYPE="SSD"
export WORKER_TIEREDSTORELEVELS_1_MOUNTPATHS='["/mnt/ssd1","/mnt/ssd2"]'
export WORKER_TIEREDSTORELEVELS_1_QUOTAS='["100Gi","100Gi"]'
export WORKER_TIEREDSTORELEVELS_1_HIGH="0.8"
export WORKER_TIEREDSTORELEVELS_1_LOW="0.6"

# client 组件（禁用）
export CLIENT_ENABLED="false"
export CLIENT_NAME="mooncake-client"
export CLIENT_OPTIONS='{}'
```

**消费者使用示例**：

```sh
# 直接读取变量
source /etc/fluid/config/runtime.sh
echo "Worker: $WORKER_NAME (replicas=$WORKER_REPLICAS)"
echo "Target: $RUNTIME_TARGETPATH"

# 遍历 mounts
for i in $(seq 0 $((MOUNTS_COUNT - 1))); do
    eval "name=\$MOUNT_${i}_NAME"
    eval "path=\$MOUNT_${i}_PATH"
    eval "endpoint=\$MOUNT_${i}_MOUNTPOINT"
    echo "Mount: $name → $path ($endpoint)"
done

# 解析 JSON 选项
worker_opts=$(echo "$WORKER_OPTIONS" | jq -r '.["bootstrap.mode"] // ""')
```

---

## 6. 安全考虑

1. **ConfigMap 访问**：注入的 ConfigMap 为只读，仅包含非敏感的配置信息。
2. **标签/注解校验**：webhook 会验证 `fluid.io/inject: "true"` 和 `fluid.io/dataset` 注解，防止未授权的注入。
3. **命名空间隔离**：插件会验证请求的运行时 ConfigMap 与 Pod 处于同一命名空间。

---

## 7. 向后兼容性

- 现有运行时（Alluxio、JindoFS 等）仅使用 `runtime.json`，继续正常工作
- `runtime.sh` 文件是可选的 —— 现有基于 FUSE 的运行时会忽略它
- webhook 插件通过 `plugins.profile` 配置按需启用
- 不修改现有 API 类型或行为
- PV/PVC 创建保持不变 —— 仅为最小化代码改动

---

## 8. 测试策略

### 8.1 单元测试

- `pkg/ddc/cache/engine/cm_test.go`：测试 `generateRuntimeSh()` 输出
- `pkg/webhook/plugins/runtimeconfiginjector/runtime_config_injector_test.go`：测试插件突变逻辑

### 8.2 集成测试

- 部署无 FUSE 的 CacheRuntime（`Client.Disabled: true`）
- 验证 ConfigMap 同时包含 `runtime.json` 和 `runtime.sh`
- 创建带有 `fluid.io/inject: "true"` 和 `fluid.io/dataset: "..."` 的测试 Pod
- 验证 webhook 注入正常工作
- 验证无 FUSE 的 Pod 在缺少 FUSE 客户端的情况下能够正常启动

### 8.3 测试用例

| 测试用例 | 描述 | 预期结果 |
|---------|------|---------|
| 无 FUSE 客户端启用 | `Client.Disabled: true` | 不创建客户端，仍创建 PV/PVC |
| runtime.sh 生成 | 为无 FUSE 运行时生成配置 | ConfigMap 中包含合法的 Shell 脚本 |
| Webhook 注入 | Pod 带有 `fluid.io/inject: "true"` + `fluid.io/dataset` | 注入 ConfigMap 卷 |
| 多数据集 | `fluid.io/dataset: "ds1,ds2"` | 注入第一个匹配的运行时配置 |
| 向后兼容 | 带 FUSE 的 Alluxio 运行时 | `runtime.sh` 非必需，现有行为不变 |
| 无注入 | 不带 `fluid.io/inject` 的 Pod | 不应用任何突变 |

---

## 9. 风险与缓解措施

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| Shell 脚本格式错误 | 无 FUSE 客户端 source 失败 | 在单元测试中添加验证 |
| Webhook 注入冲突 | Pod 突变失败 | 确保插件返回 `shouldStop: true` 以防止其他插件重复处理 |
| ConfigMap 名称冲突 | 注入错误的配置 | 注入前验证运行时是否存在 |
| 性能影响 | Webhook 延迟增加 | 缓存运行时信息；限制插件仅处理带有 `fluid.io/inject` 的 Pod |

---

## 10. 结论

本提案使 Fluid CacheRuntime 能够支持无 FUSE Client 架构（如 Mooncake），具体措施包括：

1. **reconcile 期间同时生成 `runtime.json` 和 `runtime.sh`** —— `runtime.sh` 为无 FUSE 运行时提供所需的配置格式（`source` 后可直接用 shell 变量）
2. **正确处理被禁用的客户端组件** —— 保留 PV/PVC 的创建以最小化代码改动，但不部署 FUSE 客户端
3. **引入通用的 `RuntimeConfigInjector` webhook 插件** —— 任何无 FUSE 运行时均可通过在 App Pod 上设置 `fluid.io/inject: "true"` 和 `fluid.io/dataset: "<运行时名称>"` 来使用此插件

该设计充分利用了 Fluid 现有的基础设施（webhook 插件系统、ConfigMap 挂载），对核心代码库的改动最小。Mooncake 仅是其中一个示例 —— 同样的方法适用于任意无 FUSE 缓存运行时。
