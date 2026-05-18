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
	"testing"

	datav1alpha1 "github.com/fluid-cloudnative/fluid/api/v1alpha1"
	"github.com/fluid-cloudnative/fluid/pkg/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTransformWorkerTieredStore(t *testing.T) {
	testCases := []struct {
		name          string
		runtime       *datav1alpha1.CacheRuntime
		expectError   bool
		verifyFunc    func(*common.CacheRuntimeComponentValue) error
	}{
		{
			name: "ProcessMemory medium with single path",
			runtime: &datav1alpha1.CacheRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-runtime",
					Namespace: "default",
				},
				Spec: datav1alpha1.CacheRuntimeSpec{
					Worker: datav1alpha1.CacheRuntimeWorkerSpec{
						TieredStore: datav1alpha1.RuntimeTieredStore{
							Levels: []datav1alpha1.RuntimeTieredStoreLevel{
								{
									Medium: datav1alpha1.MediumSource{
										ProcessMemory: &datav1alpha1.ProcessMemoryMediumSource{},
									},
									Path:  []string{"/dev/shm"},
									Quota: []resource.Quantity{resource.MustParse("4Gi")},
									High:  "0.95",
									Low:   "0.7",
								},
							},
						},
					},
				},
			},
			expectError: false,
			verifyFunc: func(worker *common.CacheRuntimeComponentValue) error {
				if len(worker.PodTemplateSpec.Spec.Containers) == 0 {
					return nil
				}
				container := worker.PodTemplateSpec.Spec.Containers[0]
				
				// Check memory request
				memRequest := container.Resources.Requests[corev1.ResourceMemory]
				expectedRequest := resource.MustParse("4Gi")
				if memRequest.Cmp(expectedRequest) != 0 {
					t.Errorf("Expected memory request %v, got %v", expectedRequest, memRequest)
				}
				
				// Check memory limit
				memLimit := container.Resources.Limits[corev1.ResourceMemory]
				expectedLimit := resource.MustParse("4Gi")
				if memLimit.Cmp(expectedLimit) != 0 {
					t.Errorf("Expected memory limit %v, got %v", expectedLimit, memLimit)
				}
				
				return nil
			},
		},
		{
			name: "ProcessMemory medium with multiple paths",
			runtime: &datav1alpha1.CacheRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-runtime",
					Namespace: "default",
				},
				Spec: datav1alpha1.CacheRuntimeSpec{
					Worker: datav1alpha1.CacheRuntimeWorkerSpec{
						TieredStore: datav1alpha1.RuntimeTieredStore{
							Levels: []datav1alpha1.RuntimeTieredStoreLevel{
								{
									Medium: datav1alpha1.MediumSource{
										ProcessMemory: &datav1alpha1.ProcessMemoryMediumSource{},
									},
									Path:  []string{"/dev/shm/cache1", "/dev/shm/cache2"},
									Quota: []resource.Quantity{resource.MustParse("2Gi"), resource.MustParse("3Gi")},
								},
							},
						},
					},
				},
			},
			expectError: false,
			verifyFunc: func(worker *common.CacheRuntimeComponentValue) error {
				if len(worker.PodTemplateSpec.Spec.Containers) == 0 {
					return nil
				}
				container := worker.PodTemplateSpec.Spec.Containers[0]
				
				// Total quota should be 2Gi + 3Gi = 5Gi
				memRequest := container.Resources.Requests[corev1.ResourceMemory]
				expectedRequest := resource.MustParse("5Gi")
				if memRequest.Cmp(expectedRequest) != 0 {
					t.Errorf("Expected memory request %v, got %v", expectedRequest, memRequest)
				}
				
				return nil
			},
		},
		{
			name: "EmptyDir volume medium",
			runtime: &datav1alpha1.CacheRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-runtime",
					Namespace: "default",
				},
				Spec: datav1alpha1.CacheRuntimeSpec{
					Worker: datav1alpha1.CacheRuntimeWorkerSpec{
						TieredStore: datav1alpha1.RuntimeTieredStore{
							Levels: []datav1alpha1.RuntimeTieredStoreLevel{
								{
									Medium: datav1alpha1.MediumSource{
										Volume: &datav1alpha1.VolumeMediumSource{
											EmptyDir: &corev1.EmptyDirVolumeSource{},
										},
									},
									Path:  []string{"/mnt/cache"},
									Quota: []resource.Quantity{resource.MustParse("10Gi")},
								},
							},
						},
					},
				},
			},
			expectError: false,
			verifyFunc: func(worker *common.CacheRuntimeComponentValue) error {
				podSpec := worker.PodTemplateSpec.Spec
				
				// Check volume is created
				if len(podSpec.Volumes) != 1 {
					t.Errorf("Expected 1 volume, got %d", len(podSpec.Volumes))
				} else {
					volume := podSpec.Volumes[0]
					if volume.Name != "tieredstore-level0-path0" {
						t.Errorf("Expected volume name 'tieredstore-level0-path0', got '%s'", volume.Name)
					}
					if volume.EmptyDir == nil {
						t.Error("Expected EmptyDir volume source")
					}
				}
				
				// Check volume mount is created
				if len(podSpec.Containers) > 0 {
					container := podSpec.Containers[0]
					if len(container.VolumeMounts) != 1 {
						t.Errorf("Expected 1 volume mount, got %d", len(container.VolumeMounts))
					} else {
						mount := container.VolumeMounts[0]
						if mount.Name != "tieredstore-level0-path0" {
							t.Errorf("Expected volume mount name 'tieredstore-level0-path0', got '%s'", mount.Name)
						}
						if mount.MountPath != "/mnt/cache" {
							t.Errorf("Expected mount path '/mnt/cache', got '%s'", mount.MountPath)
						}
					}
				}
				
				return nil
			},
		},
		{
			name: "HostPath volume medium",
			runtime: &datav1alpha1.CacheRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-runtime",
					Namespace: "default",
				},
				Spec: datav1alpha1.CacheRuntimeSpec{
					Worker: datav1alpha1.CacheRuntimeWorkerSpec{
						TieredStore: datav1alpha1.RuntimeTieredStore{
							Levels: []datav1alpha1.RuntimeTieredStoreLevel{
								{
									Medium: datav1alpha1.MediumSource{
										Volume: &datav1alpha1.VolumeMediumSource{
											HostPath: &corev1.HostPathVolumeSource{
												Path: "/host/path/cache",
												Type: func() *corev1.HostPathType {
													t := corev1.HostPathDirectoryOrCreate
													return &t
												}(),
											},
										},
									},
									Path: []string{"/mnt/cache"},
								},
							},
						},
					},
				},
			},
			expectError: false,
			verifyFunc: func(worker *common.CacheRuntimeComponentValue) error {
				podSpec := worker.PodTemplateSpec.Spec
				
				// Check volume is created with HostPath
				if len(podSpec.Volumes) != 1 {
					t.Errorf("Expected 1 volume, got %d", len(podSpec.Volumes))
				} else {
					volume := podSpec.Volumes[0]
					if volume.HostPath == nil {
						t.Error("Expected HostPath volume source")
					} else if volume.HostPath.Path != "/host/path/cache" {
						t.Errorf("Expected host path '/host/path/cache', got '%s'", volume.HostPath.Path)
					}
				}
				
				return nil
			},
		},
		{
			name: "Multiple tier levels",
			runtime: &datav1alpha1.CacheRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-runtime",
					Namespace: "default",
				},
				Spec: datav1alpha1.CacheRuntimeSpec{
					Worker: datav1alpha1.CacheRuntimeWorkerSpec{
						TieredStore: datav1alpha1.RuntimeTieredStore{
							Levels: []datav1alpha1.RuntimeTieredStoreLevel{
								{
									Medium: datav1alpha1.MediumSource{
										ProcessMemory: &datav1alpha1.ProcessMemoryMediumSource{},
									},
									Path:  []string{"/dev/shm"},
									Quota: []resource.Quantity{resource.MustParse("2Gi")},
								},
								{
									Medium: datav1alpha1.MediumSource{
										Volume: &datav1alpha1.VolumeMediumSource{
											EmptyDir: &corev1.EmptyDirVolumeSource{},
										},
									},
									Path:  []string{"/mnt/ssd"},
									Quota: []resource.Quantity{resource.MustParse("100Gi")},
								},
							},
						},
					},
				},
			},
			expectError: false,
			verifyFunc: func(worker *common.CacheRuntimeComponentValue) error {
				podSpec := worker.PodTemplateSpec.Spec
				
				// Should have 1 volume (from second level)
				if len(podSpec.Volumes) != 1 {
					t.Errorf("Expected 1 volume, got %d", len(podSpec.Volumes))
				}
				
				// Memory should be set from first level
				if len(podSpec.Containers) > 0 {
					container := podSpec.Containers[0]
					memRequest := container.Resources.Requests[corev1.ResourceMemory]
					expectedRequest := resource.MustParse("2Gi")
					if memRequest.Cmp(expectedRequest) != 0 {
						t.Errorf("Expected memory request %v, got %v", expectedRequest, memRequest)
					}
				}
				
				return nil
			},
		},
		{
			name: "No tiered store configuration",
			runtime: &datav1alpha1.CacheRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-runtime",
					Namespace: "default",
				},
				Spec: datav1alpha1.CacheRuntimeSpec{
					Worker: datav1alpha1.CacheRuntimeWorkerSpec{},
				},
			},
			expectError: false,
			verifyFunc: func(worker *common.CacheRuntimeComponentValue) error {
				// Should not modify anything
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			engine := &CacheEngine{}
			
			// Create a worker component value with a container
			worker := &common.CacheRuntimeComponentValue{
				PodTemplateSpec: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "worker",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{},
									Limits:   corev1.ResourceList{},
								},
							},
						},
					},
				},
			}
			
			err := engine.transformWorkerTieredStore(&tc.runtime.Spec.Worker.TieredStore, &worker.PodTemplateSpec.Spec)
			
			if tc.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			
			if !tc.expectError && tc.verifyFunc != nil {
				tc.verifyFunc(worker)
			}
		})
	}
}

func TestAddProcessMemoryResources(t *testing.T) {
	engine := &CacheEngine{}
	
	container := &corev1.Container{
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{},
			Limits:   corev1.ResourceList{},
		},
	}
	
	level := datav1alpha1.RuntimeTieredStoreLevel{
		Path:  []string{"/dev/shm"},
		Quota: []resource.Quantity{resource.MustParse("8Gi")},
	}
	
	err := engine.addProcessMemoryResources(container, level, 0)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	
	// Verify memory request
	memRequest := container.Resources.Requests[corev1.ResourceMemory]
	expected := resource.MustParse("8Gi")
	if memRequest.Cmp(expected) != 0 {
		t.Errorf("Expected memory request %v, got %v", expected, memRequest)
	}
	
	// Verify memory limit
	memLimit := container.Resources.Limits[corev1.ResourceMemory]
	if memLimit.Cmp(expected) != 0 {
		t.Errorf("Expected memory limit %v, got %v", expected, memLimit)
	}
}

func TestAddProcessMemoryResourcesWithExistingResources(t *testing.T) {
	engine := &CacheEngine{}
	
	existingCPU := resource.MustParse("1")
	container := &corev1.Container{
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU: existingCPU,
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU: existingCPU,
			},
		},
	}
	
	level := datav1alpha1.RuntimeTieredStoreLevel{
		Path:  []string{"/dev/shm"},
		Quota: []resource.Quantity{resource.MustParse("4Gi")},
	}
	
	err := engine.addProcessMemoryResources(container, level, 0)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	
	// Verify CPU is preserved
	cpuRequest := container.Resources.Requests[corev1.ResourceCPU]
	if cpuRequest.Cmp(existingCPU) != 0 {
		t.Errorf("Expected CPU request %v, got %v", existingCPU, cpuRequest)
	}
	
	// Verify memory is added
	memRequest := container.Resources.Requests[corev1.ResourceMemory]
	expectedMem := resource.MustParse("4Gi")
	if memRequest.Cmp(expectedMem) != 0 {
		t.Errorf("Expected memory request %v, got %v", expectedMem, memRequest)
	}
}
