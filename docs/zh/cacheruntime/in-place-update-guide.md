# CacheRuntime Master/Worker 原地更新指南

## 概述

CacheRuntime 的 Master 和 Worker 组件使用 **AdvancedStatefulSet** 作为工作负载类型，支持**原地更新（In-Place Update）**功能。原地更新允许在不重建 Pod 的情况下更新容器配置，从而实现更快的升级速度和更好的服务连续性。

## 什么是原地更新？

原地更新是指在不删除和重建 Pod 的情况下，直接更新运行中容器的某些配置。相比传统的滚动更新（删除旧 Pod → 创建新 Pod），原地更新具有以下优势：

### ✅ 优势

1. **更快的更新速度**：无需等待新 Pod 启动和就绪
2. **保持 Pod IP 不变**：对有状态应用非常重要
3. **减少资源抖动**：不需要临时创建额外 Pod
4. **服务连续性更好**：避免服务中断时间

### 🔄 智能降级机制

当修改不支持原地更新的字段时，AdvancedStatefulSet 会**自动降级**为传统的删除重建方式，确保最终一致性。

## 支持原地更新的场景

根据 `RuntimeComponentCommonSpec` 的定义，以下字段支持原地更新：

### 1. 容器镜像更新 ✅

**字段**: `runtimeVersion`

**示例**:
```yaml
apiVersion: data.fluid.io/v1alpha1
kind: CacheRuntime
metadata:
  name: my-cache
spec:
  master:
    runtimeVersion:
      image: fluid-cache
      imageTag: v1.1.0  # 从 v1.0.0 升级到 v1.1.0
      imagePullPolicy: IfNotPresent  # 推荐：优先使用本地缓存
```

**说明**:
- ✅ 支持原地更新容器镜像版本
- ✅ 保持 Pod 运行，仅替换容器镜像
- ⚠️ **`imagePullPolicy` 是启动时策略**，在原地更新时决定如何获取新镜像：
  - `IfNotPresent`（推荐）：节点已有镜像则直接使用，否则拉取
  - `Always`：每次都检查远程仓库，确保最新版本
  - `Never`：只使用本地镜像，离线环境适用
- 💡 **最佳实践**：生产环境使用固定版本标签 + `IfNotPresent`
- 📝 **注意**：需要同时指定 `image` 和 `imageTag` 字段

---

### 2. 资源限制调整 ✅

**字段**: `resources`

**示例**:
```yaml
spec:
  master:
    resources:
      requests:
        cpu: "2"      # 从 1 核调整为 2 核
        memory: 4Gi   # 从 2Gi 调整为 4Gi
      limits:
        cpu: "4"      # 从 2 核调整为 4 核
        memory: 8Gi   # 从 4Gi 调整为 8Gi
```

**说明**:
- ✅ 支持原地调整 CPU 和内存限制
- ✅ 可以单独调整 requests 或 limits
- ⚠️ 不能超过节点可用资源
- 💡 适合动态扩缩容场景

---

### 3. 环境变量更新（部分场景）✅

**字段**: `env`

**支持的场景**:
- ✅ 从 ConfigMap/Secret 引用的环境变量
- ✅ 通过 Downward API 注入的环境变量
- ❌ 硬编码的环境变量值（需要重建 Pod）

**示例**:
```yaml
spec:
  master:
    env:
    - name: CACHE_SIZE
      valueFrom:
        configMapKeyRef:
          name: cache-config
          key: cache-size  # 修改 ConfigMap 后自动更新
```

**说明**:
- ✅ 引用外部配置的环境变量支持原地更新
- ⚠️ 需要配合 ConfigMap/Secret 的热更新机制
- 💡 推荐将可变配置提取到 ConfigMap 中

---

### 4. Pod 元数据更新 ✅

**字段**: `podMetadata`

**示例**:
```yaml
spec:
  master:
    podMetadata:
      labels:
        version: v1.1.0      # 添加或修改标签
        environment: prod
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
```

**说明**:
- ✅ 支持原地添加或修改 Labels
- ✅ 支持原地添加或修改 Annotations
- 💡 可用于服务发现、监控配置等场景

---

## 不支持原地更新的场景（触发重建）

以下字段修改时会触发传统的 Pod 重建：

### ❌ 容器结构变更

```yaml
# 以下修改会触发重建
spec:
  master:
    # ❌ 添加或删除容器
    # ❌ 修改容器名称
    # ❌ 修改容器端口
    ports:
    - containerPort: 8080  # 修改端口号
    
    # ❌ 修改命令和参数
    args: ["--new-arg"]    # 修改启动参数
    
    # ❌ 修改卷挂载
    volumeMounts:
    - name: new-volume     # 添加新的卷挂载
      mountPath: /data
```

---

### ❌ 存储相关变更

```yaml
spec:
  master:
    # ❌ 添加或删除 volumes
    # ❌ 修改 PVC 引用
    # ❌ 修改 HostPath 路径
    volumeMounts:
    - name: cache-data
      mountPath: /cache    # 修改挂载路径
```

---

### ❌ 网络和调度相关变更

```yaml
spec:
  master:
    # ❌ 修改节点选择器
    nodeSelector:
      disktype: ssd        # 可能导致调度到不同节点
    
    # ❌ 修改容忍度
    tolerations:
    - key: "dedicated"     # 添加新的容忍度
      operator: "Equal"
      value: "cache"
    
    # ❌ 修改亲和性配置
    # ❌ 修改服务账户
```

---

### ❌ 安全上下文变更

```yaml
spec:
  master:
    # ❌ 修改 securityContext
    securityContext:
      runAsUser: 1000      # 修改运行用户
      runAsGroup: 1000
```

---

## 最佳实践

### 1. 优先使用原地更新支持的字段

**推荐做法**:
```yaml
# ✅ 好：使用 ConfigMap 管理配置
spec:
  master:
    env:
    - name: LOG_LEVEL
      valueFrom:
        configMapKeyRef:
          name: cache-config
          key: log-level
    resources:
      requests:
        cpu: "2"
        memory: 4Gi
```

**不推荐做法**:
```yaml
# ❌ 差：硬编码配置，修改需要重建
spec:
  master:
    env:
    - name: LOG_LEVEL
      value: "INFO"  # 硬编码
    args:
    - "--log-level=INFO"  # 硬编码在参数中
```

---

### 2. 灰度发布策略

使用 AdvancedStatefulSet 的 `Partition` 字段实现灰度发布：

```yaml
# 第一步：只更新最后一个 Pod
spec:
  updateStrategy:
    rollingUpdate:
      partition: 2  # 假设有 3 个副本，只更新第 3 个

# 第二步：验证通过后，逐步降低 partition
spec:
  updateStrategy:
    rollingUpdate:
      partition: 1  # 更新第 2、3 个

# 第三步：完成全部更新
spec:
  updateStrategy:
    rollingUpdate:
      partition: 0  # 更新所有 Pod
```

---

### 3. 监控原地更新状态

检查 Pod 的原地更新状态：

```bash
# 查看 Pod 的原地更新状态注解
kubectl get pod <pod-name> -o jsonpath='{.metadata.annotations.workload\.fluid\.io/inplace-update-state}'

# 查看原地更新就绪状态
kubectl get pod <pod-name> -o jsonpath='{.status.conditions[?(@.type=="InPlaceUpdateReady")].status}'
```

**状态说明**:
- `True`: 原地更新已完成，Pod 已就绪
- `False`: 原地更新进行中或未开始
- `Unknown`: 状态未知

---

### 4. 资源配置建议

**Master 组件**:
```yaml
spec:
  master:
    replicas: 3
    resources:
      requests:
        cpu: "2"
        memory: 4Gi
      limits:
        cpu: "4"
        memory: 8Gi
    runtimeVersion:
      image: fluid-cache
      imageTag: v1.1.0
```

**Worker 组件**:
```yaml
spec:
  worker:
    replicas: 5
    resources:
      requests:
        cpu: "4"
        memory: 8Gi
      limits:
        cpu: "8"
        memory: 16Gi
    runtimeVersion:
      image: fluid-cache
      imageTag: v1.1.0
```

---

## 更新流程示例

### 场景 1: 镜像升级（原地更新）

```bash
# 1. 修改 CacheRuntime 的镜像版本
kubectl patch cacheruntime my-cache --type='merge' -p '{
  "spec": {
    "master": {
      "runtimeVersion": {
        "imageTag": "v1.1.0"
      }
    },
    "worker": {
      "runtimeVersion": {
        "imageTag": "v1.1.0"
      }
    }
  }
}'

# 2. 观察更新状态
kubectl get pods -l fluid.io/cache-runtime-name=my-cache -w

# 3. 验证原地更新（Pod 名称不变，IP 不变）
kubectl get pods -l fluid.io/cache-runtime-name=my-cache -o wide
```

**预期结果**:
- ✅ Pod 名称保持不变（如 `my-cache-master-0`）
- ✅ Pod IP 保持不变
- ✅ 容器镜像版本更新为 v1.1.0
- ✅ 服务短暂不可用时间 < 1 秒

---

### 场景 2: 资源扩容（原地更新）

```bash
# 1. 增加资源限制
kubectl patch cacheruntime my-cache --type='merge' -p '{
  "spec": {
    "worker": {
      "resources": {
        "requests": {
          "cpu": "6",
          "memory": "12Gi"
        },
        "limits": {
          "cpu": "12",
          "memory": "24Gi"
        }
      }
    }
  }
}'

# 2. 验证资源更新
kubectl describe pod <worker-pod-name> | grep -A 5 "Limits:"
```

**预期结果**:
- ✅ CPU 和内存限制原地更新
- ✅ Pod 不重启
- ✅ 缓存数据不丢失

---

### 场景 3: 配置更新（触发重建）

```bash
# 修改 args 会触发重建
kubectl patch cacheruntime my-cache --type='merge' -p '{
  "spec": {
    "master": {
      "args": ["--new-config=true"]
    }
  }
}'
```

**预期结果**:
- ⚠️ Pod 会被删除并重新创建
- ⚠️ Pod IP 会改变
- ⚠️ 服务会有短暂中断
- 💡 建议通过 ConfigMap 管理此类配置

---

## 故障排查

### 问题 1: 原地更新卡住

**症状**: Pod 长时间处于 `Updating` 状态

**排查步骤**:
```bash
# 1. 检查原地更新状态
kubectl get pod <pod-name> -o yaml | grep -A 20 "inplace-update-state"

# 2. 检查容器日志
kubectl logs <pod-name> -c <container-name> --previous

# 3. 检查资源是否充足
kubectl describe node <node-name> | grep -A 10 "Allocated resources"
```

**解决方案**:
- 确保节点有足够资源
- 检查镜像拉取策略
- 必要时手动触发重建

---

### 问题 2: 更新后服务不可用

**症状**: 更新完成后服务无法正常访问

**排查步骤**:
```bash
# 1. 检查 Pod 就绪状态
kubectl get pod <pod-name> -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'

# 2. 检查 InPlaceUpdateReady 状态
kubectl get pod <pod-name> -o jsonpath='{.status.conditions[?(@.type=="InPlaceUpdateReady")].status}'

# 3. 检查应用日志
kubectl logs <pod-name> -f
```

**解决方案**:
- 等待 `InPlaceUpdateReady` 变为 `True`
- 检查应用健康检查配置
- 验证配置文件是否正确加载

---

## 总结

### ✅ 推荐使用原地更新的场景

| 场景 | 字段 | 更新速度 | 服务影响 |
|------|------|----------|----------|
| 镜像升级 | `runtimeVersion` | ⚡ 快 | 极小 |
| 资源调整 | `resources` | ⚡ 快 | 无 |
| 配置热更新 | `env` (ConfigMap) | ⚡ 快 | 无 |
| 元数据更新 | `podMetadata` | ⚡ 快 | 无 |

### ⚠️ 会触发重建的场景

| 场景 | 字段 | 更新速度 | 服务影响 |
|------|------|----------|----------|
| 容器结构变更 | `args`, `ports` | 🐢 慢 | 中等 |
| 存储变更 | `volumeMounts` | 🐢 慢 | 中等 |
| 调度变更 | `nodeSelector`, `tolerations` | 🐢 慢 | 较大 |
| 安全上下文 | `securityContext` | 🐢 慢 | 中等 |

### 💡 最佳实践要点

1. **优先使用原地更新支持的字段**进行配置变更
2. **将可变配置提取到 ConfigMap**，避免硬编码
3. **使用灰度发布策略**，逐步验证更新
4. **监控原地更新状态**，及时发现异常
5. **合理设置资源限制**，避免资源不足导致更新失败

通过合理利用原地更新功能，可以实现 CacheRuntime Master/Worker 组件的平滑升级和动态调整，显著提升运维效率和系统可用性。
