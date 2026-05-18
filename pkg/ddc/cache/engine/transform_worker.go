/*
  Copyright 2026 The Fluid Authors.

  Licensed under the Apache License, Version 2.0 (the "License");
  you may not use this file except in compliance with the License.
  You may obtain a copy of the License at

      http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific language governing permissions and
  limitations under the License.
*/

package engine

import (
	"fmt"

	datav1alpha1 "github.com/fluid-cloudnative/fluid/api/v1alpha1"
	"github.com/fluid-cloudnative/fluid/pkg/common"
	"github.com/fluid-cloudnative/fluid/pkg/ddc/base"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (e *CacheEngine) transformWorker(dataset *datav1alpha1.Dataset, runtime *datav1alpha1.CacheRuntime, runtimeClass *datav1alpha1.CacheRuntimeClass,
	commonConfig *CacheRuntimeComponentCommonConfig, value *common.CacheRuntimeValue) error {
	if runtimeClass.Topology == nil || runtimeClass.Topology.Worker == nil || runtime.Spec.Worker.Disabled {
		value.Worker = &common.CacheRuntimeComponentValue{Enabled: false}
		return nil
	}

	runtimeWorker := runtime.Spec.Worker
	componentDefinition := runtimeClass.Topology.Worker

	// Initialize component value with common fields
	var err error
	value.Worker, err = e.initComponentValue(common.ComponentTypeWorker, componentDefinition, commonConfig.Owner, runtimeWorker.Replicas)
	if err != nil {
		return err
	}

	// transform container related config, currently only modify the first container
	e.transformComponentPodTemplate(runtimeWorker.RuntimeComponentCommonSpec, dataset, value.Worker)

	// transform tiered store configuration
	err = e.transformWorkerTieredStore(&runtimeWorker.TieredStore, &value.Worker.PodTemplateSpec.Spec)
	if err != nil {
		return err
	}

	// make sure affinity not nil
	if value.Worker.PodTemplateSpec.Spec.Affinity == nil {
		value.Worker.PodTemplateSpec.Spec.Affinity = &corev1.Affinity{}
	}

	runtimeInfo, err := e.getRuntimeInfo()
	if err != nil {
		return err
	}

	// dataset.Spec.NodeAffinity only affects worker (cache) pods
	e.buildWorkerAffinity(value.Worker.PodTemplateSpec.Spec.Affinity, dataset, runtimeInfo)

	// inject stateful set pod match labels for workers
	value.Worker.MatchLabels = map[string]string{
		common.LabelAnnotationDataset:          runtimeInfo.GetOwnerDatasetUID(),
		common.LabelAnnotationDatasetPlacement: (string)(runtimeInfo.GetPlacementModeWithDefault(datav1alpha1.ExclusiveMode)),
	}

	// transform all volume-related configurations
	err = e.transformVolumes(runtime.Spec.Volumes, runtime.Spec.Worker.VolumeMounts, dataset, componentDefinition, commonConfig, true, &value.Worker.PodTemplateSpec.Spec)
	if err != nil {
		return err
	}

	return nil
}

// buildWorkerAffinity builds affinity for worker pods, refer to Helper.BuildWorkerAffinity
func (e *CacheEngine) buildWorkerAffinity(affinity *corev1.Affinity, dataset *datav1alpha1.Dataset, runtimeInfo base.RuntimeInfoInterface) {
	// 1. Set pod anti affinity(required) for same dataset (Current using port conflict for scheduling, no need to do)

	// 2. Set pod anti affinity for the different dataset
	if affinity.PodAntiAffinity == nil {
		// Ensure PodAntiAffinity exists
		affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
	}

	if dataset.IsExclusiveMode() {
		affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      common.LabelAnnotationDataset,
							Operator: metav1.LabelSelectorOpExists,
						},
					},
				},
				TopologyKey: common.K8sNodeNameLabelKey,
			},
		)
	} else {
		// Append to PreferredDuringSchedulingIgnoredDuringExecution
		affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
			affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
			corev1.WeightedPodAffinityTerm{
				// The default weight is 50
				Weight: 50,
				PodAffinityTerm: corev1.PodAffinityTerm{
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      common.LabelAnnotationDataset,
								Operator: metav1.LabelSelectorOpExists,
							},
						},
					},
					TopologyKey: common.K8sNodeNameLabelKey,
				},
			},
		)
		// Append to RequiredDuringSchedulingIgnoredDuringExecution
		affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      common.LabelAnnotationDatasetPlacement,
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{string(datav1alpha1.ExclusiveMode)},
						},
					},
				},
				TopologyKey: common.K8sNodeNameLabelKey,
			},
		)
	}

	// 3. Prefer to locate on the node which already has fuse on it
	if affinity.NodeAffinity == nil {
		affinity.NodeAffinity = &corev1.NodeAffinity{}
	}

	affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(
		affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution,
		corev1.PreferredSchedulingTerm{
			Weight: 100,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      runtimeInfo.GetFuseLabelName(),
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"true"},
					},
				},
			},
		})

	// append dataset node affinity
	datasetNodeAffinity := dataset.Spec.NodeAffinity
	if datasetNodeAffinity == nil || datasetNodeAffinity.Required == nil || len(datasetNodeAffinity.Required.NodeSelectorTerms) == 0 {
		return
	}

	// Ensure NodeAffinity exists in result
	if affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		affinity.NodeAffinity = &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: datasetNodeAffinity.Required,
		}
		return
	}

	// Merge node selector terms from both
	affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(
		affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms,
		datasetNodeAffinity.Required.NodeSelectorTerms...)
}

// transformWorkerTieredStore transforms the tiered store configuration to worker pod spec
func (e *CacheEngine) transformWorkerTieredStore(tieredStore *datav1alpha1.RuntimeTieredStore, podSpec *corev1.PodSpec) error {
	if len(tieredStore.Levels) == 0 {
		return nil
	}

	if len(podSpec.Containers) == 0 {
		return fmt.Errorf("no containers found in worker pod spec")
	}

	container := &podSpec.Containers[0]

	// Process each tier level
	for i, level := range tieredStore.Levels {
		// Handle medium source first
		if level.Medium.ProcessMemory != nil {
			// Process memory: add resource requests and limits
			err := e.addProcessMemoryResources(container, level, i)
			if err != nil {
				return err
			}
		} else if level.Medium.Volume != nil {
			// Volume-based storage: create volumes and volume mounts
			err := e.addVolumeStorage(podSpec, container, level, i)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// addProcessMemoryResources adds memory resources to container for process memory medium
func (e *CacheEngine) addProcessMemoryResources(container *corev1.Container, level datav1alpha1.RuntimeTieredStoreLevel, levelIndex int) error {
	if len(level.Path) == 0 || len(level.Quota) == 0 {
		return fmt.Errorf("path and quota must be specified for process memory medium at level %d", levelIndex)
	}

	// Calculate total memory quota across all paths
	var totalQuota resource.Quantity
	for _, quota := range level.Quota {
		totalQuota.Add(quota)
	}

	// Initialize resource requirements if not present
	if container.Resources.Requests == nil {
		container.Resources.Requests = corev1.ResourceList{}
	}
	if container.Resources.Limits == nil {
		container.Resources.Limits = corev1.ResourceList{}
	}

	// Add memory requests and limits
	currentRequest := container.Resources.Requests[corev1.ResourceMemory]
	currentLimit := container.Resources.Limits[corev1.ResourceMemory]

	newRequest := currentRequest
	newRequest.Add(totalQuota)
	newLimit := currentLimit
	newLimit.Add(totalQuota)

	container.Resources.Requests[corev1.ResourceMemory] = newRequest
	container.Resources.Limits[corev1.ResourceMemory] = newLimit

	return nil
}

// addVolumeStorage adds volume and volume mount for volume-based medium
func (e *CacheEngine) addVolumeStorage(podSpec *corev1.PodSpec, container *corev1.Container, level datav1alpha1.RuntimeTieredStoreLevel, levelIndex int) error {
	volumeSource := level.Medium.Volume
	if volumeSource == nil {
		// not handle
		return nil
	}

	// Process each path and corresponding quota
	for i, path := range level.Path {
		volumeName := fmt.Sprintf("tieredstore-level%d-path%d", levelIndex, i)
		mountPath := path

		// Create volume based on volume source type
		var volume corev1.Volume
		volume.Name = volumeName

		if volumeSource.HostPath != nil {
			volume.VolumeSource.HostPath = volumeSource.HostPath.DeepCopy()
		} else if volumeSource.EmptyDir != nil {
			volume.VolumeSource.EmptyDir = volumeSource.EmptyDir.DeepCopy()
			// Set size limit if quota is specified
			if i < len(level.Quota) {
				quota := level.Quota[i]
				// TODO memory 性质的如何处理？
				if volume.VolumeSource.EmptyDir.SizeLimit == nil {
					volume.VolumeSource.EmptyDir.SizeLimit = &quota
				}
			}
		} else if volumeSource.Ephemeral != nil {
			volume.VolumeSource.Ephemeral = volumeSource.Ephemeral.DeepCopy()
			// Set storage quota in volumeClaimTemplate if specified
			if i < len(level.Quota) {
				quota := level.Quota[i]
				if volume.VolumeSource.Ephemeral.VolumeClaimTemplate != nil {
					if volume.VolumeSource.Ephemeral.VolumeClaimTemplate.Spec.Resources.Requests == nil {
						volume.VolumeSource.Ephemeral.VolumeClaimTemplate.Spec.Resources.Requests = corev1.ResourceList{}
					}
					// Only set if not already specified in the template
					if _, exists := volume.VolumeSource.Ephemeral.VolumeClaimTemplate.Spec.Resources.Requests[corev1.ResourceStorage]; !exists {
						volume.VolumeSource.Ephemeral.VolumeClaimTemplate.Spec.Resources.Requests[corev1.ResourceStorage] = quota
					}
				}
			}
		} else {
			return fmt.Errorf("no storage medium for volume source at level %d, path index %d", levelIndex, i)
		}

		// Add volume to pod spec
		podSpec.Volumes = append(podSpec.Volumes, volume)

		// Add volume mount to container
		volumeMount := corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		}
		container.VolumeMounts = append(container.VolumeMounts, volumeMount)
	}

	return nil
}
