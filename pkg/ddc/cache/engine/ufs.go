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
	"encoding/json"
	"fmt"
	"time"

	datav1alpha1 "github.com/fluid-cloudnative/fluid/api/v1alpha1"
	"github.com/fluid-cloudnative/fluid/pkg/common"
	"github.com/fluid-cloudnative/fluid/pkg/utils"
	"github.com/fluid-cloudnative/fluid/pkg/utils/kubeclient"
	"github.com/pkg/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (e *CacheEngine) PrepareUFS(runtimeClass *datav1alpha1.CacheRuntimeClass) (string, error) {
	if runtimeClass.Topology == nil || runtimeClass.Topology.Master == nil ||
		runtimeClass.Topology.Master.ExecutionEntries == nil || runtimeClass.Topology.Master.ExecutionEntries.MountUFS == nil {
		e.Log.Info("No mount ufs command found in runtime class")
		return "", nil
	}
	mountUfs := runtimeClass.Topology.Master.ExecutionEntries.MountUFS

	// execute mount command in master pod
	podName, containerName, err := e.getMasterPodInfo(runtimeClass)
	if err != nil {
		return "", err
	}

	fileUtils := newCacheFileUtils(podName, containerName, e.namespace, e.Log)

	// at least 20 seconds
	timeoutSeconds := max(mountUfs.TimeoutSeconds, 20)

	stdout, err := fileUtils.Mount(mountUfs.Command, time.Duration(timeoutSeconds)*time.Second)
	if err != nil {
		return "", nil
	}

	return stdout, nil
}

// shouldUpdateUFS determines whether the UFS configuration needs to be updated.
// It retrieves the current dataset, analyzes the path differences to identify
// which UFS entries require updates.
func (e *CacheEngine) shouldUpdateUFS(dataset *datav1alpha1.Dataset, runtimeClass *datav1alpha1.CacheRuntimeClass) bool {
	// whether the ufs mount paths need to update
	ufsToUpdate := utils.NewUFSToUpdate(dataset)
	ufsToUpdate.AnalyzePathsDelta()
	if ufsToUpdate != nil && ufsToUpdate.ShouldUpdate() {
		e.Log.Info("Detected UFS changes, updating mount points", "toAdd", ufsToUpdate.ToAdd(), "toRemove", ufsToUpdate.ToRemove())
		return true
	}

	// check if master pod restart after the latest mount time
	restart := e.checkIfRemountRequired(runtimeClass)
	if restart {
		e.Log.Info("Master pod restart after the latest mount time, need to remount")
	}

	return restart
}

// UpdateOnUFSChange handles changes to the UFS configuration by updating mount points dynamically.
// When dataset mount information changes, this method ensures the changes take effect without requiring a restart.
func (e *CacheEngine) UpdateOnUFSChange() (err error) {
	runtime, err := e.getRuntime()
	if err != nil {
		return err
	}

	runtimeClass, err := e.getRuntimeClass(runtime.Spec.RuntimeClassName)
	if err != nil {
		return
	}

	// update only for master-worker architecture and has mount ufs command
	if runtimeClass.Topology == nil || runtimeClass.Topology.Master == nil || runtime.Spec.Master.Disabled ||
		runtimeClass.Topology.Master.ExecutionEntries == nil ||
		runtimeClass.Topology.Master.ExecutionEntries.MountUFS == nil {
		return
	}

	// 1. get the dataset
	dataset, err := utils.GetDataset(e.Client, e.name, e.namespace)
	if err != nil {
		e.Log.Error(err, "Failed to get the dataset")
		return
	}

	shouldUpdate := e.shouldUpdateUFS(dataset, runtimeClass)
	// 1. check if need to update ufs
	if !shouldUpdate {
		return
	}

	// 2. set update status to updating
	err = utils.UpdateMountStatus(e.Client, e.name, e.namespace, datav1alpha1.UpdatingDatasetPhase)
	if err != nil {
		e.Log.Error(err, "Failed to update dataset status to updating")
		return
	}

	// 3. use the same mount script to add or remove mount points
	// how to make sure the config map is mounted in pod !!!
	stdout, err := e.PrepareUFS(runtimeClass)
	if err != nil {
		e.Log.Error(err, "Failed to add or remove mount points")
		return
	}

	// 4. parse mount output, and sync dataset mounts
	mountOutput := &datav1alpha1.CacheRuntimeMountUfsOutput{}
	err = json.Unmarshal([]byte(stdout), mountOutput)
	if err != nil {
		return errors.Wrapf(err, "mount ufs output is not CacheRuntimeMountUfsOutput struct format, output is: %s", stdout)
	}

	err = e.SyncDatasetMounts(dataset, mountOutput)
	if err != nil {
		e.Log.Error(err, "Failed to sync dataset mounts")
		return err
	}

	// 5. update latest mount time
	e.updateMountTime()

	return
}

// SyncDatasetMounts synchronizes the dataset mount points with the current runtime state.
// This ensures that any changes to dataset.spec.mounts are reflected in the running system.
func (e *CacheEngine) SyncDatasetMounts(dataset *datav1alpha1.Dataset, mountOutput *datav1alpha1.CacheRuntimeMountUfsOutput) (err error) {

	// Update MountPoints based on current dataset mounts
	var mountedPaths = map[string]bool{}
	for _, path := range mountOutput.Mounted {
		mountedPaths[path] = true
	}

	// check mounted path is the same as dataset spec mounts
	for _, mount := range dataset.Spec.Mounts {
		if common.IsFluidNativeScheme(mount.MountPoint) {
			continue
		}
		datasetMountPath := utils.UFSPathBuilder{}.GenUFSPathInUnifiedNamespace(mount)
		if !mountedPaths[datasetMountPath] {
			e.Log.Info("Waiting for mount point to be mounted", "Mount point", datasetMountPath)
			return
		}
		delete(mountedPaths, datasetMountPath)
	}
	if len(mountedPaths) != 0 {
		e.Log.Info("Waiting for mount point to be unmounted", "Mount points", mountedPaths)
		return
	}

	// update dataset status mount and phase status
	err = utils.UpdateMountStatus(e.Client, e.name, e.namespace, datav1alpha1.BoundDatasetPhase)
	if err != nil {
		return err
	}

	return nil
}

func (e *CacheEngine) checkIfRemountRequired(runtimeClass *datav1alpha1.CacheRuntimeClass) bool {
	runtime, err := e.getRuntime()
	if err != nil {
		e.Log.Error(err, "can not get runtime", "method", "checkIfRemountRequired", "runtime", e.name)
		return false
	}

	masterPodName, masterContainerName, err := e.getMasterPodInfo(runtimeClass)
	if err != nil {
		e.Log.Error(err, "get runtime pod container name failed", "method", "checkIfRemountRequired", "runtimeClass name", e.name)
		return false
	}

	masterPod, err := kubeclient.GetPodByName(e.Client, masterPodName, e.namespace)
	if err != nil {
		e.Log.Error(err, "get runtime pod failed", "method", "checkIfRemountRequired", "pod name", masterPodName)
		return false
	}
	if masterPod == nil {
		e.Log.Error(fmt.Errorf("master pod is not found"), "checkIfRemountRequired", "pod name", masterPodName)
		return false
	}

	var startedAt *v1.Time
	for _, containerStatus := range masterPod.Status.ContainerStatuses {
		if containerStatus.Name == masterContainerName {
			if containerStatus.State.Running == nil {
				e.Log.Error(fmt.Errorf("container is not running"), "checkIfRemountRequired", "master pod", masterPodName)
				return false
			}

			startedAt = &containerStatus.State.Running.StartedAt
			break
		}
	}
	if startedAt == nil {
		e.Log.Error(fmt.Errorf("can not get container start time"), "checkIfRemountRequired", "pod name", masterPod, "container name", masterContainerName)
		return false
	}

	// If mount time is earlier than master container start time, remount is necessary
	if runtime.Status.MountTime == nil || runtime.Status.MountTime.Before(startedAt) {
		return true
	}

	return false
}
