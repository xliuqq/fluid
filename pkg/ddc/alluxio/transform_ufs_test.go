/*
Copyright 2020 The Fluid Authors.

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

package alluxio

import (
	"testing"

	datav1alpha1 "github.com/fluid-cloudnative/fluid/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

// TestTransformDatasetToVolume is a unit test function that verifies the transformation of a Dataset into a UFSPath
// in the Alluxio runtime. It ensures that the Dataset's mount points are correctly converted into corresponding
// container and host paths.
//
// Parameters:
//   - t: *testing.T, the Go testing framework's object used to manage the test execution and report failures.
//
// Return value:
//   - None (this is a test function, and it does not return any values).
func TestTransformDatasetToVolume(t *testing.T) {
	// Initialize UFSPath variables for expected results.
	var ufsPath = UFSPath{}
	ufsPath.Name = "test"
	ufsPath.HostPath = "/mnt/test"
	ufsPath.ContainerPath = "/underFSStorage/test"

	var ufsPath1 = UFSPath{}
	ufsPath1.Name = "test"
	ufsPath1.HostPath = "/mnt/test"
	ufsPath1.ContainerPath = "/underFSStorage"

	// Define test cases with different Dataset configurations
	var tests = []struct {
		runtime *datav1alpha1.AlluxioRuntime
		dataset *datav1alpha1.Dataset
		value   *Alluxio
		expect  UFSPath
	}{
		// Test case 1: Simple Mount configuration
		{&datav1alpha1.AlluxioRuntime{}, &datav1alpha1.Dataset{
			Spec: datav1alpha1.DatasetSpec{
				Mounts: []datav1alpha1.Mount{{
					MountPoint: "local:///mnt/test",
					Name:       "test",
				}},
			},
		}, &Alluxio{}, ufsPath},

		// Test case 2: Mount with a path specified
		{&datav1alpha1.AlluxioRuntime{}, &datav1alpha1.Dataset{
			Spec: datav1alpha1.DatasetSpec{
				Mounts: []datav1alpha1.Mount{{
					MountPoint: "local:///mnt/test",
					Name:       "test",
					Path:       "/",
				}},
			},
		}, &Alluxio{}, ufsPath1},
	}

	// Iterate through all test cases and run the test.
	for _, test := range tests {
		// Create an instance of AlluxioEngine to call the function under test.
		engine := &AlluxioEngine{}
		// Call the function that we want to test.
		engine.transformDatasetToVolume(test.runtime, test.dataset, test.value)
		// Compare the actual result with the expected result.
		if test.value.UFSPaths[0].HostPath != test.expect.HostPath ||
			test.value.UFSPaths[0].ContainerPath != test.expect.ContainerPath {
			// If the result doesn't match the expected values, report an error.
			t.Errorf("expected %v, got %v", test.expect, test.value.UFSPaths[0])
		}
	}
}

// TestTransformDatasetToPVC tests the transformation of Dataset specifications into Persistent Volume Claim (PVC)
// configurations in Alluxio volumes. It validates the correct conversion of Dataset mount configurations to
// corresponding UFSVolume structures with proper path mappings.
//
// Parameters:
//   - t *testing.T : Go testing framework context for test reporting and assertions
//
// Test Components:
//   - Defines 4 expected UFSVolume configurations covering different PVC mount scenarios
//   - Constructs test cases with varying Dataset.Spec.Mounts configurations
//   - Verifies transformed Alluxio configuration matches expected volume parameters
//
// Return Value:
//   - None (Standard Go test function, reports failures through t.Errorf)
func TestTransformDatasetToPVC(t *testing.T) {
	var ufsVolume = UFSVolume{}
	ufsVolume.Name = "test"
	ufsVolume.ContainerPath = "/underFSStorage/test"

	var ufsVolume1 = UFSVolume{}
	ufsVolume1.Name = "test1"
	ufsVolume1.ContainerPath = "/underFSStorage"

	var ufsVolume2 = UFSVolume{}
	ufsVolume2.Name = "test2"
	ufsVolume2.SubPath = "subpath"
	ufsVolume2.ContainerPath = "/underFSStorage/test2"

	var ufsVolume3 = UFSVolume{}
	ufsVolume3.Name = "test3"
	ufsVolume3.SubPath = "subpath"
	ufsVolume3.ContainerPath = "/underFSStorage"

	var tests = []struct {
		runtime *datav1alpha1.AlluxioRuntime
		dataset *datav1alpha1.Dataset
		value   *Alluxio
		expect  UFSVolume
	}{
		{&datav1alpha1.AlluxioRuntime{}, &datav1alpha1.Dataset{
			Spec: datav1alpha1.DatasetSpec{
				Mounts: []datav1alpha1.Mount{{
					MountPoint: "pvc://test",
					Name:       "test",
				}},
			},
		}, &Alluxio{}, ufsVolume},
		{&datav1alpha1.AlluxioRuntime{}, &datav1alpha1.Dataset{
			Spec: datav1alpha1.DatasetSpec{
				Mounts: []datav1alpha1.Mount{{
					MountPoint: "pvc://test1",
					Name:       "test1",
					Path:       "/",
				}},
			},
		}, &Alluxio{}, ufsVolume1},
		{&datav1alpha1.AlluxioRuntime{}, &datav1alpha1.Dataset{
			Spec: datav1alpha1.DatasetSpec{
				Mounts: []datav1alpha1.Mount{{
					MountPoint: "pvc://test2/subpath",
					Name:       "test2",
				}},
			},
		}, &Alluxio{}, ufsVolume2},
		{&datav1alpha1.AlluxioRuntime{}, &datav1alpha1.Dataset{
			Spec: datav1alpha1.DatasetSpec{
				Mounts: []datav1alpha1.Mount{{
					MountPoint: "pvc://test3/subpath",
					Name:       "test3",
					Path:       "/",
				}},
			},
		}, &Alluxio{}, ufsVolume3},
	}
	for _, test := range tests {
		engine := &AlluxioEngine{}
		engine.transformDatasetToVolume(test.runtime, test.dataset, test.value)
		if test.value.UFSVolumes[0].ContainerPath != test.expect.ContainerPath ||
			test.value.UFSVolumes[0].Name != test.expect.Name ||
			test.value.UFSVolumes[0].SubPath != test.expect.SubPath {
			t.Errorf("expected %v, got %v", test.expect, test.value)
		}
	}
}

// TestTransformDatasetWithAffinity is a unit test function that verifies the behavior of the
// transformDatasetToVolume method when a Dataset with NodeAffinity is provided.
// It ensures that the NodeAffinity configuration is correctly transferred to the Alluxio master's affinity settings.
//
// Parameters:
//   - t: *testing.T, the Go testing framework's object used to manage the test execution and report failures.
//
// Return value:
//   - None (this is a test function, and it does not return any values).
func TestTransformDatasetWithAffinity(t *testing.T) {
	var ufsPath = UFSPath{}
	ufsPath.Name = "test"
	ufsPath.HostPath = "/mnt/test"
	ufsPath.ContainerPath = "/opt/alluxio/underFSStorage/test"

	var tests = []struct {
		runtime *datav1alpha1.AlluxioRuntime
		dataset *datav1alpha1.Dataset
		value   *Alluxio
		expect  UFSPath
	}{
		{&datav1alpha1.AlluxioRuntime{}, &datav1alpha1.Dataset{
			Spec: datav1alpha1.DatasetSpec{
				Mounts: []datav1alpha1.Mount{{
					MountPoint: "local:///mnt/test",
					Name:       "test",
				}},
				NodeAffinity: &datav1alpha1.CacheableNodeAffinity{
					Required: &v1.NodeSelector{
						NodeSelectorTerms: []v1.NodeSelectorTerm{
							{
								MatchExpressions: []v1.NodeSelectorRequirement{
									{
										Operator: v1.NodeSelectorOpIn,
										Values:   []string{"test-label-value"},
									},
								},
							},
						},
					},
				},
			},
		}, &Alluxio{}, ufsPath},
	}
	for _, test := range tests {
		engine := &AlluxioEngine{}
		engine.transformDatasetToVolume(test.runtime, test.dataset, test.value)
		if test.value.Master.Affinity.NodeAffinity == nil {
			t.Error("The master affinity is nil")
		}
	}
}
