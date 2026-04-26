# Cache Engine 去 Helm 化实施方案

## 背景

目前 Cache Engine 在进行 DataLoad Reconcile 时，通过 helm install 的形式安装 Job。为了简化架构和减少依赖，需要对 Cache Engine 进行去 Helm 化改造。

## 设计目标

1. **接口抽象**：将 Helm 的 install/check/delete 操作抽象为统一接口
2. **双实现并存**：提供 HelmInstaller（现有逻辑）和 K8sInstaller（直接操作 K8s API）两种实现
3. **最小改动**：只影响 Cache Engine，其他 Engine 继续使用 Helm
4. **向后兼容**：默认行为不变，通过注入方式选择实现

## 核心设计

### 1. 定义 DataOperationInstaller 接口

**文件**: `pkg/ddc/base/operation_installer.go` (新建)

```go
package base

import (
	"github.com/fluid-cloudnative/fluid/pkg/dataoperation"
	cruntime "github.com/fluid-cloudnative/fluid/pkg/runtime"
)

// DataOperationInstaller defines the interface for installing data operation resources
type DataOperationInstaller interface {
	// Install installs the data operation resource if not exists
	Install(ctx cruntime.ReconcileRequestContext, operation dataoperation.OperationInterface, 
		yamlGenerator DataOperatorYamlGenerator) error
	
	// Delete deletes the data operation resource
	Delete(name, namespace string) error
	
	// Exists checks if the data operation resource exists
	Exists(name, namespace string) (bool, error)
}
```

### 2. HelmInstaller 实现（现有逻辑封装）

**文件**: `pkg/ddc/base/operation_helm.go` (修改)

保留现有的 `InstallDataOperationHelmIfNotExist` 函数作为向后兼容，同时添加 HelmInstaller 结构：

```go
// HelmInstaller implements DataOperationInstaller using Helm
type HelmInstaller struct{}

var _ DataOperationInstaller = &HelmInstaller{}

func NewHelmInstaller() *HelmInstaller {
	return &HelmInstaller{}
}

func (h *HelmInstaller) Install(ctx cruntime.ReconcileRequestContext, operation dataoperation.OperationInterface,
	yamlGenerator DataOperatorYamlGenerator) error {
	// 调用现有的 InstallDataOperationHelmIfNotExist 逻辑
	return InstallDataOperationHelmIfNotExist(ctx, operation, yamlGenerator)
}

func (h *HelmInstaller) Delete(name, namespace string) error {
	return helm.DeleteReleaseIfExists(name, namespace)
}

func (h *HelmInstaller) Exists(name, namespace string) (bool, error) {
	return helm.CheckRelease(name, namespace)
}
```

### 3. K8sInstaller 实现（直接操作 K8s API）

**文件**: `pkg/ddc/base/operation_k8s.go` (新建)

```go
package base

import (
	"context"
	"fmt"
	"os"

	"github.com/fluid-cloudnative/fluid/pkg/dataoperation"
	fluiderrors "github.com/fluid-cloudnative/fluid/pkg/errors"
	cruntime "github.com/fluid-cloudnative/fluid/pkg/runtime"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// K8sInstaller implements DataOperationInstaller using Kubernetes client directly
type K8sInstaller struct {
	client.Client
}

var _ DataOperationInstaller = &K8sInstaller{}

func NewK8sInstaller(client client.Client) *K8sInstaller {
	return &K8sInstaller{Client: client}
}

func (k *K8sInstaller) Install(ctx cruntime.ReconcileRequestContext, operation dataoperation.OperationInterface,
	yamlGenerator DataOperatorYamlGenerator) error {
	log := ctx.Log.WithName("K8sInstaller.Install")

	namespacedName := operation.GetReleaseNameSpacedName()

	existed, err := k.Exists(namespacedName.Name, namespacedName.Namespace)
	if err != nil {
		log.Error(err, "failed to check if job exists")
		return err
	}

	if !existed {
		log.Info("job not exists, will create", "jobName", namespacedName.Name)
		
		// Generate values file
		valueFileName, err := yamlGenerator.GetDataOperationValueFile(ctx, operation)
		if err != nil {
			log.Error(err, "failed to generate value file")
			return err
		}
		defer os.Remove(valueFileName) // Clean up temp file

		// Build Job object from values
		job, err := k.buildJobFromValues(ctx, operation, valueFileName)
		if err != nil {
			log.Error(err, "failed to build job from values")
			return err
		}

		// Create Job
		if err := k.Create(ctx, job); err != nil {
			log.Error(err, "failed to create job")
			return err
		}
		
		log.Info("job successfully created", "namespace", namespacedName.Namespace, "jobName", namespacedName.Name)
	}

	return nil
}

func (k *K8sInstaller) Delete(name, namespace string) error {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	
	deletePolicy := metav1.DeletePropagationForeground
	deleteOptions := &client.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}
	
	err := k.Delete(context.TODO(), job, deleteOptions)
	if err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

func (k *K8sInstaller) Exists(name, namespace string) (bool, error) {
	job := &batchv1.Job{}
	err := k.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, job)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// buildJobFromValues builds a Kubernetes Job from the values file
func (k *K8sInstaller) buildJobFromValues(ctx cruntime.ReconcileRequestContext, 
	operation dataoperation.OperationInterface, valueFile string) (*batchv1.Job, error) {
	
	valueBytes, err := os.ReadFile(valueFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read value file: %w", err)
	}

	jobBuilder, ok := operation.(JobBuilder)
	if !ok {
		return nil, fluiderrors.NewNotSupported(
			schema.GroupResource{},
			fmt.Sprintf("operation %T does not support JobBuilder interface", operation))
	}

	return jobBuilder.BuildJobFromValues(ctx, valueBytes)
}

// JobBuilder is an optional interface that operations can implement to build Jobs from values
type JobBuilder interface {
	BuildJobFromValues(ctx cruntime.ReconcileRequestContext, valueBytes []byte) (*batchv1.Job, error)
}
```

### 4. 修改 EngineOperationReconciler 支持注入 Installer

**文件**: `pkg/ddc/base/operation.go` (修改)

```go
// OperationEngine defines the interface needed for operation reconciliation
type OperationEngine interface {
	DataOperatorYamlGenerator
	CheckRuntimeReady() bool
}

type EngineOperationReconciler struct {
	Engine    OperationEngine
	Client    client.Client
	Installer DataOperationInstaller  // 新增：支持依赖注入
}

// ReconcileOperation 保持不变...

func (e *EngineOperationReconciler) reconcileExecuting(ctx cruntime.ReconcileRequestContext, opStatus *datav1alpha1.OperationStatus,
	operation dataoperation.OperationInterface) (ctrl.Result, error) {
	log := ctx.Log.WithName("reconcileExecuting")

	// 使用注入的 Installer，如果没有则使用默认的 HelmInstaller
	installer := e.Installer
	if installer == nil {
		installer = NewHelmInstaller()
	}

	// 1. Install the data operation resource if not exists
	err := installer.Install(ctx, operation, e.Engine)
	if err != nil {
		object := operation.GetOperationObject()
		if fluiderrs.IsNotSupported(err) {
			log.Error(err, "not support current data operation, set status to failed")
			ctx.Recorder.Eventf(object, v1.EventTypeWarning, common.DataOperationNotSupport,
				"RuntimeType %s not support %s", ctx.RuntimeType, operation.GetOperationType())

			opStatus.Phase = common.PhaseFailed
			if err = operation.UpdateOperationApiStatus(opStatus); err != nil {
				log.Error(err, "failed to update api status")
				return utils.RequeueIfError(err)
			}
			return utils.NoRequeue()
		}
		ctx.Recorder.Eventf(object, v1.EventTypeWarning, common.DataOperationExecutionFailed, "fail to execute data operation: %v", err)
		return utils.RequeueAfterInterval(20 * time.Second)
	}

	// 2. update data operation's status by helm status
	statusHandler := operation.GetStatusHandler()
	if statusHandler == nil {
		err = fmt.Errorf("fail to get status handler")
		log.Error(err, "status handler is nil")
		return utils.RequeueIfError(err)
	}
	opStatusToUpdate, err := statusHandler.GetOperationStatus(ctx, opStatus)
	if err != nil {
		log.Error(err, "failed to update status")
		return utils.RequeueIfError(err)
	}
	if !reflect.DeepEqual(opStatus, opStatusToUpdate) {
		if err = operation.UpdateOperationApiStatus(opStatusToUpdate); err != nil {
			log.Error(err, "failed to update api status")
			return utils.RequeueIfError(err)
		}
		log.V(1).Info(fmt.Sprintf("update operation status to %s successfully", opStatusToUpdate.Phase), "opstatus", opStatusToUpdate)
	}

	return utils.RequeueAfterInterval(20 * time.Second)
}
```

### 5. 修改 TemplateEngine 使用新的 reconciler

**文件**: `pkg/ddc/base/template_engine_operation.go` (修改 - **之前遗漏**)

在第 30-34 行附近，需要注入 Installer：

```go
// 原代码：
e := EngineOperationReconciler{
	Engine: t,
	Client: t.Client,
}

// 修改为：
e := EngineOperationReconciler{
	Engine:    t,
	Client:    t.Client,
	Installer: NewHelmInstaller(), // 显式使用 HelmInstaller，保持向后兼容
}
```

### 6. 修改 operation_controller.go 的删除逻辑

**文件**: `pkg/controllers/operation_controller.go` (修改 - **之前遗漏**)

在 `ReconcileDeletion` 方法中（第 74-121 行），需要根据 engine 类型选择正确的删除方式：

```go
func (o *OperationReconciler) ReconcileDeletion(ctx dataoperation.ReconcileRequestContext,
	implement dataoperation.OperationInterface) (ctrl.Result, error) {
	log := ctx.Log.WithName("ReconcileDeletion")

	// 1. Delete data operation resource using the appropriate installer
	namespacedName := implement.GetReleaseNameSpacedName()
	
	// 根据 EngineImpl 选择对应的 Installer
	var installer base.DataOperationInstaller
	if ctx.EngineImpl == common.CacheEngineImpl {
		// For CacheEngine, use K8sInstaller
		installer = base.NewK8sInstaller(o.Client)
	} else {
		// For other engines, use HelmInstaller
		installer = base.NewHelmInstaller()
	}
	
	err := installer.Delete(namespacedName.Name, namespacedName.Namespace)
	if err != nil {
		log.Error(err, "can't delete data operation resource", "name", namespacedName.Name)
		return utils.RequeueIfError(err)
	}

	// 2. Release lock on target dataset if necessary
	err = base.ReleaseTargetDataset(ctx.ReconcileRequestContext, implement)
	if utils.IgnoreNotFound(err) != nil {
		log.Error(err, "can't release lock on target dataset")
		return utils.RequeueIfError(err)
	}

	// 3. delete engine
	namespacedNames := implement.GetPossibleTargetDatasetNamespacedNames()
	for _, namespacedName := range namespacedNames {
		o.RemoveEngine(namespacedName)
	}

	object := implement.GetOperationObject()
	// 4. remove finalizer
	if !object.GetDeletionTimestamp().IsZero() {
		objectMeta, err := utils.GetObjectMeta(object)
		if err != nil {
			return utils.RequeueIfError(err)
		}

		finalizers := utils.RemoveString(objectMeta.GetFinalizers(), ctx.DataOpFinalizerName)
		objectMeta.SetFinalizers(finalizers)

		if err := o.Update(ctx, object); err != nil {
			log.Error(err, "Failed to remove finalizer")
			return utils.RequeueIfError(err)
		}
		log.Info("Finalizer is removed")
	}
	return utils.NoRequeue()
}
```

### 7. 修改 status_handler.go 中的异常处理逻辑

**需要修改的文件清单**:
- `pkg/controllers/v1alpha1/dataload/status_handler.go` (2处)
- `pkg/controllers/v1alpha1/datamigrate/status_handler.go` (2处)
- `pkg/controllers/v1alpha1/dataprocess/status_handler.go` (1处)

**注意**: `pkg/controllers/v1alpha1/databackup/status_handler.go` 使用 Pod 而非 Job，无需修改。

#### 7.1 dataload/status_handler.go

**第一处修改** (第 62-74 行 - OnceStatusHandler):
```go
if err != nil {
	// Job missing, delete the resource and requeue
	if utils.IgnoreNotFound(err) == nil {
		ctx.Log.Info("Related Job missing, will delete resource and retry", 
			"namespace", ctx.Namespace, "jobName", jobName)
		
		// 根据 EngineImpl 选择对应的删除方式
		var installer base.DataOperationInstaller
		if ctx.EngineImpl == common.CacheEngineImpl {
			installer = base.NewK8sInstaller(ctx.Client)
		} else {
			installer = base.NewHelmInstaller()
		}
		
		releaseName := utils.GetDataLoadReleaseName(object.GetName())
		if err = installer.Delete(releaseName, ctx.Namespace); err != nil {
			ctx.Log.Error(err, "can't delete dataload resource", 
				"namespace", ctx.Namespace, "releaseName", releaseName)
			return
		}
	}
	// other error
	ctx.Log.Error(err, "can't get dataload job", "namespace", ctx.Namespace, "jobName", jobName)
	return
}
```

**第二处修改** (第 120-130 行左右 - CronStatusHandler):
找到类似的 `helm.DeleteReleaseIfExists` 调用，用相同的方式替换。

#### 7.2 datamigrate/status_handler.go

**第一处修改** (第 68-80 行左右 - OnceStatusHandler):
```go
if err != nil {
	// helm release found but job missing, delete the helm release and requeue
	if utils.IgnoreNotFound(err) == nil {
		ctx.Log.Info("Related Job missing, will delete resource and retry", "namespace", ctx.Namespace, "jobName", jobName)
		
		// 根据 EngineImpl 选择对应的删除方式
		var installer base.DataOperationInstaller
		if ctx.EngineImpl == common.CacheEngineImpl {
			installer = base.NewK8sInstaller(ctx.Client)
		} else {
			installer = base.NewHelmInstaller()
		}
		
		if err = installer.Delete(releaseName, ctx.Namespace); err != nil {
			m.Log.Error(err, "can't delete DataMigrate resource", "namespace", ctx.Namespace, "releaseName", releaseName)
			return
		}
		return
	}
	// other error
	ctx.Log.Error(err, "can't get DataMigrate job", "namespace", ctx.Namespace, "jobName", jobName)
	return
}
```

**第二处修改** (第 125-135 行左右 - CronStatusHandler):
找到类似的 `helm.DeleteReleaseIfExists` 调用，用相同的方式替换。

#### 7.3 dataprocess/status_handler.go

**修改** (第 50-63 行左右):
```go
if err != nil {
	// In case of NotFound error
	if utils.IgnoreNotFound(err) == nil {
		ctx.Log.Info("Related job missing, will delete resource and retry", "namespace", ctx.Namespace, "jobName", jobName)
		
		// 根据 EngineImpl 选择对应的删除方式
		var installer base.DataOperationInstaller
		if ctx.EngineImpl == common.CacheEngineImpl {
			installer = base.NewK8sInstaller(handler.Client)
		} else {
			installer = base.NewHelmInstaller()
		}
		
		if err = installer.Delete(releaseName, ctx.Namespace); err != nil {
			ctx.Log.Error(err, "failed to delete dataprocess resource", "namespace", ctx.Namespace, "releaseName", releaseName)
			return
		}
	}

	// In cases of other error
	ctx.Log.Error(err, "can't get dataprocess job", "namespace", ctx.Namespace, "jobName", jobName)
	return
}
```

### 8. Cache Engine 实现 JobBuilder 接口

**文件**: `pkg/ddc/cache/engine/dataload.go` (修改)

添加 `BuildJobFromValues` 方法：

```go
import (
	"strconv"
	
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"github.com/fluid-cloudnative/fluid/pkg/utils/transformer"
	cdataload "github.com/fluid-cloudnative/fluid/pkg/dataload"
)

// BuildJobFromValues implements the JobBuilder interface
func (e *CacheEngine) BuildJobFromValues(ctx cruntime.ReconcileRequestContext, valueBytes []byte) (*batchv1.Job, error) {
	// Parse the values
	var dataLoadValue cdataload.DataLoadValue
	if err := yaml.Unmarshal(valueBytes, &dataLoadValue); err != nil {
		return nil, fmt.Errorf("failed to unmarshal values: %w", err)
	}

	// Get the dataload object to get metadata
	dataload := &v1alpha1.DataLoad{}
	key := types.NamespacedName{
		Name:      dataLoadValue.Name,
		Namespace: ctx.Namespace,
	}
	if err := e.Get(ctx, key, dataload); err != nil {
		return nil, fmt.Errorf("failed to get dataload: %w", err)
	}

	// Build the Job
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.GetDataLoadJobName(dataload.Name),
			Namespace: dataload.Namespace,
			Labels: map[string]string{
				"release":       utils.GetDataLoadReleaseName(dataload.Name),
				"role":          "dataload-job",
				"app":           "cache",
				"targetDataset": dataload.Spec.Dataset.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				transformer.GenerateOwnerReferenceFromObject(dataload),
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To[int32](int32(dataLoadValue.BackoffLimit)),
			Completions:  ptr.To[int32](1),
			Parallelism:  ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("%s-loader", dataload.Name),
					Annotations: map[string]string{
						"sidecar.istio.io/inject": "false",
					},
					Labels: map[string]string{
						"release":       utils.GetDataLoadReleaseName(dataload.Name),
						"role":          "dataload-pod",
						"app":           "cache",
						"targetDataset": dataload.Spec.Dataset.Name,
					},
				},
				Spec: corev1.PodSpec{
					RestartPolicy:    corev1.RestartPolicyNever,
					ImagePullSecrets: dataLoadValue.ImagePullSecrets,
					Containers: []corev1.Container{
						{
							Name:            "dataloader",
							Image:           dataLoadValue.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         dataLoadValue.Command,
							Args:            dataLoadValue.Args,
							Resources:       dataLoadValue.Resources,
							Env:             buildDataLoadEnvs(dataLoadValue),
						},
					},
				},
			},
		},
	}

	// Apply affinity, nodeSelector, tolerations, schedulerName from DataLoadInfo
	if dataLoadValue.Affinity != nil {
		job.Spec.Template.Spec.Affinity = dataLoadValue.Affinity
	}
	if dataLoadValue.NodeSelector != nil {
		job.Spec.Template.Spec.NodeSelector = dataLoadValue.NodeSelector
	}
	if len(dataLoadValue.Tolerations) > 0 {
		job.Spec.Template.Spec.Tolerations = dataLoadValue.Tolerations
	}
	if dataLoadValue.SchedulerName != "" {
		job.Spec.Template.Spec.SchedulerName = dataLoadValue.SchedulerName
	}

	return job, nil
}

func buildDataLoadEnvs(value cdataload.DataLoadValue) []corev1.EnvVar {
	// Build target paths string
	var targetPaths string
	for i, tp := range value.TargetPaths {
		if i > 0 {
			targetPaths += ":"
		}
		targetPaths += tp.Path
	}

	// Build path replicas string
	var pathReplicas string
	for i, tp := range value.TargetPaths {
		if i > 0 {
			pathReplicas += ":"
		}
		replicas := tp.Replicas
		if replicas == 0 {
			replicas = 1
		}
		pathReplicas += strconv.Itoa(int(replicas))
	}

	envs := []corev1.EnvVar{
		{
			Name:  "FLUID_DATALOAD_METADATA",
			Value: strconv.FormatBool(value.LoadMetadata),
		},
		{
			Name:  "FLUID_DATALOAD_DATA_PATH",
			Value: targetPaths,
		},
		{
			Name:  "FLUID_DATALOAD_PATH_REPLICAS",
			Value: pathReplicas,
		},
	}
	
	// Add custom envs
	for _, env := range value.Envs {
		envs = append(envs, corev1.EnvVar{
			Name:  env.Name,
			Value: env.Value,
		})
	}
	
	return envs
}
```

### 9. Cache Engine 的 Operate 方法注入 K8sInstaller

**文件**: `pkg/ddc/cache/engine/data_operation.go` (修改)

```go
func (e *CacheEngine) Operate(ctx cruntime.ReconcileRequestContext, opStatus *v1alpha1.OperationStatus, operation dataoperation.OperationInterface) (ctrl.Result, error) {
	r := base.EngineOperationReconciler{
		Engine:    e,
		Client:    e.Client,
		Installer: base.NewK8sInstaller(e.Client), // 注入 K8sInstaller
	}

	return r.ReconcileOperation(ctx, opStatus, operation)
}
```

## 实施步骤

### Phase 1: 基础架构（不影响现有功能）
1. ✅ 创建 `pkg/ddc/base/operation_installer.go` - 定义接口
2. ✅ 修改 `pkg/ddc/base/operation_helm.go` - 添加 HelmInstaller
3. ✅ 创建 `pkg/ddc/base/operation_k8s.go` - 实现 K8sInstaller
4. ✅ 修改 `pkg/ddc/base/operation.go` - EngineOperationReconciler 支持注入

### Phase 2: 适配层修改
5. ✅ 修改 `pkg/ddc/base/template_engine_operation.go` - 显式注入 HelmInstaller
6. ✅ 修改 `pkg/controllers/operation_controller.go` - ReconcileDeletion 使用接口
7. ✅ 修改所有 status_handler.go 文件 - 异常处理使用接口（共5处）
   - `pkg/controllers/v1alpha1/dataload/status_handler.go` (2处)
   - `pkg/controllers/v1alpha1/datamigrate/status_handler.go` (2处)
   - `pkg/controllers/v1alpha1/dataprocess/status_handler.go` (1处)
   
   **注意**: `pkg/controllers/v1alpha1/databackup/status_handler.go` 使用 Pod 而非 Job，无需修改。

### Phase 3: Cache Engine 实现
8. ✅ 修改 `pkg/ddc/cache/engine/dataload.go` - 实现 JobBuilder 接口
9. ✅ 修改 `pkg/ddc/cache/engine/data_operation.go` - 注入 K8sInstaller

### Phase 4: 测试验证
10. 运行单元测试
11. 运行集成测试
12. 验证 Cache Engine 的 DataLoad 功能
13. 验证其他 Engine 不受影响

## 注意事项

1. **兼容性**: 所有修改必须保证其他 Engine（Alluxio、Jindo、JuiceFS 等）继续正常工作
2. **测试覆盖**: 需要为新的 Installer 接口编写单元测试
3. **错误处理**: K8sInstaller 的错误处理要与 HelmInstaller 保持一致
4. **资源清理**: 确保 Job 删除时使用正确的传播策略（Foreground）
5. **临时文件**: K8sInstaller 中生成的 values.yaml 临时文件要及时清理

## 回滚方案

如果出现问题，可以通过以下方式快速回滚：
1. 在 `pkg/ddc/cache/engine/data_operation.go` 中将 Installer 改回 nil（使用默认 HelmInstaller）
2. 或者完全 revert 相关 commit

## 后续优化

1. 可以考虑将其他 Engine 也逐步迁移到 K8sInstaller
2. 可以提取公共的 Job 构建逻辑，减少重复代码
3. 可以考虑支持更多的资源类型（Pod、StatefulSet 等）

---

## 附录：完整修改文件清单

### 新建文件 (2个)
1. `pkg/ddc/base/operation_installer.go` - 定义 DataOperationInstaller 接口
2. `pkg/ddc/base/operation_k8s.go` - 实现 K8sInstaller

### 修改文件 (10个)

#### 基础架构层 (4个)
1. `pkg/ddc/base/operation_helm.go` - 添加 HelmInstaller 实现
2. `pkg/ddc/base/operation.go` - EngineOperationReconciler 支持 Installer 注入
3. `pkg/ddc/base/template_engine_operation.go` - 显式注入 HelmInstaller
4. `pkg/controllers/operation_controller.go` - ReconcileDeletion 使用接口

#### Status Handler 层 (3个文件，共5处修改)
5. `pkg/controllers/v1alpha1/dataload/status_handler.go` - **2处修改**
   - OnceStatusHandler.GetOperationStatus (第62-74行)
   - CronStatusHandler.GetOperationStatus (第120-130行左右)
6. `pkg/controllers/v1alpha1/datamigrate/status_handler.go` - **2处修改**
   - OnceStatusHandler.GetOperationStatus (第68-80行左右)
   - CronStatusHandler.GetOperationStatus (第125-135行左右)
7. `pkg/controllers/v1alpha1/dataprocess/status_handler.go` - **1处修改**
   - OnceStatusHandler.GetOperationStatus (第50-63行左右)

**注意**: `pkg/controllers/v1alpha1/databackup/status_handler.go` 使用 Pod 而非 Job，无需修改。

#### Cache Engine 实现层 (2个)
8. `pkg/ddc/cache/engine/dataload.go` - 实现 JobBuilder 接口
9. `pkg/ddc/cache/engine/data_operation.go` - Operate 方法注入 K8sInstaller

### 需要替换的 helm.DeleteReleaseIfExists 调用位置

通过 `grep` 搜索找到以下6处调用：

1. ✅ `pkg/controllers/operation_controller.go:L81` - 已包含在上面的修改清单中
2. ✅ `pkg/controllers/v1alpha1/dataload/status_handler.go:L66` - OnceStatusHandler
3. ✅ `pkg/controllers/v1alpha1/dataload/status_handler.go:L125` - CronStatusHandler
4. ✅ `pkg/controllers/v1alpha1/datamigrate/status_handler.go:L72` - OnceStatusHandler
5. ✅ `pkg/controllers/v1alpha1/datamigrate/status_handler.go:L130` - CronStatusHandler
6. ✅ `pkg/controllers/v1alpha1/dataprocess/status_handler.go:L54` - OnceStatusHandler

所有这6处都需要按照方案中的方式进行替换。
