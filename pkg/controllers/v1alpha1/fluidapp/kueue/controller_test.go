/*
Copyright 2024 The Fluid Authors.

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

package kueue

import (
	"context"
	datav1alpha1 "github.com/fluid-cloudnative/fluid/api/v1alpha1"
	"github.com/fluid-cloudnative/fluid/pkg/common"
	"github.com/fluid-cloudnative/fluid/pkg/utils/fake"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"sigs.k8s.io/kueue/pkg/workload"
	"testing"
)

func TestController_Reconcile(t *testing.T) {
	type args struct {
		ctx context.Context
		kueue.Workload
		kueue.AdmissionCheck
		req  reconcile.Request
		objs []runtime.Object
	}
	tests := []struct {
		name      string
		args      args
		wantErr   bool
		wantState *kueue.AdmissionCheckState
	}{
		{
			name: "completed dataload",
			args: args{
				req: reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: "test-workload",
					},
				},
				AdmissionCheck: kueue.AdmissionCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-check",
					},
					Spec: kueue.AdmissionCheckSpec{
						ControllerName: ControllerName,
					},
				},
				Workload: kueue.Workload{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-workload",
					},
					Spec: kueue.WorkloadSpec{
						PodSets: []kueue.PodSet{
							{
								Template: v1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: map[string]string{
											WaitDataOpType: "DataLoad",
											WaitDataOpName: "test-dataload",
										},
									},
								},
							},
						},
					},
					Status: kueue.WorkloadStatus{
						AdmissionChecks: []kueue.AdmissionCheckState{
							{
								Name:  "test-check",
								State: kueue.CheckStatePending,
							},
						},
					},
				},
				objs: []runtime.Object{
					&datav1alpha1.DataLoad{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-dataload",
						},
						Status: datav1alpha1.OperationStatus{
							Phase: common.PhaseComplete,
						},
					},
				},
			},
			wantErr: false,
			wantState: &kueue.AdmissionCheckState{
				Name:    "test-check",
				State:   kueue.CheckStateReady,
				Message: "waited data operation is completed",
			},
		},
		{
			name: "failed dataprocess",
			args: args{
				req: reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: "test-workload",
					},
				},
				AdmissionCheck: kueue.AdmissionCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-check",
					},
					Spec: kueue.AdmissionCheckSpec{
						ControllerName: ControllerName,
					},
				},
				Workload: kueue.Workload{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-workload",
					},
					Spec: kueue.WorkloadSpec{
						PodSets: []kueue.PodSet{
							{
								Template: v1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: map[string]string{
											WaitDataOpType: "DataProcess",
											WaitDataOpName: "test-dataprocess",
										},
									},
								},
							},
						},
					},
					Status: kueue.WorkloadStatus{
						AdmissionChecks: []kueue.AdmissionCheckState{
							{
								Name:  "test-check",
								State: kueue.CheckStatePending,
							},
						},
					},
				},
				objs: []runtime.Object{
					&datav1alpha1.DataProcess{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-dataprocess",
						},
						Status: datav1alpha1.OperationStatus{
							Phase: common.PhaseFailed,
						},
					},
				},
			},
			wantErr: false,
			wantState: &kueue.AdmissionCheckState{
				Name:    "test-check",
				State:   kueue.CheckStateRejected,
				Message: "waited data operation is failed",
			},
		},
		{
			name: "running datamigrate",
			args: args{
				req: reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: "test-workload",
					},
				},
				AdmissionCheck: kueue.AdmissionCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-check",
					},
					Spec: kueue.AdmissionCheckSpec{
						ControllerName: ControllerName,
					},
				},
				Workload: kueue.Workload{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-workload",
					},
					Spec: kueue.WorkloadSpec{
						PodSets: []kueue.PodSet{
							{
								Template: v1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: map[string]string{
											WaitDataOpType: "DataMigrate",
											WaitDataOpName: "test-datamigrate",
										},
									},
								},
							},
						},
					},
					Status: kueue.WorkloadStatus{
						AdmissionChecks: []kueue.AdmissionCheckState{
							{
								Name:  "test-check",
								State: kueue.CheckStatePending,
							},
						},
					},
				},
				objs: []runtime.Object{
					&datav1alpha1.DataMigrate{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-datamigrate",
						},
						Status: datav1alpha1.OperationStatus{
							Phase: common.PhaseExecuting,
						},
					},
				},
			},
			wantErr: false,
			wantState: &kueue.AdmissionCheckState{
				Name:  "test-check",
				State: kueue.CheckStatePending,
			},
		},
		{
			name: "no dataop",
			args: args{
				req: reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: "test-workload",
					},
				},
				AdmissionCheck: kueue.AdmissionCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-check",
					},
					Spec: kueue.AdmissionCheckSpec{
						ControllerName: ControllerName,
					},
				},
				Workload: kueue.Workload{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-workload",
					},
					Spec: kueue.WorkloadSpec{
						PodSets: []kueue.PodSet{
							{
								Template: v1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: map[string]string{},
									},
								},
							},
						},
					},
					Status: kueue.WorkloadStatus{
						AdmissionChecks: []kueue.AdmissionCheckState{
							{
								Name:  "test-check",
								State: kueue.CheckStatePending,
							},
						},
					},
				},
				objs: []runtime.Object{
					&datav1alpha1.DataMigrate{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-datamigrate",
						},
						Status: datav1alpha1.OperationStatus{
							Phase: common.PhaseExecuting,
						},
					},
				},
			},
			wantErr: false,
			wantState: &kueue.AdmissionCheckState{
				Name:  "test-check",
				State: kueue.CheckStatePending,
			},
		},
	}
	schema := runtime.NewScheme()
	_ = datav1alpha1.AddToScheme(schema)
	_ = kueue.AddToScheme(schema)

	acCmpOptions := []cmp.Option{
		cmpopts.IgnoreFields(kueue.AdmissionCheckState{}, "LastTransitionTime"),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := append(tt.args.objs, &tt.args.Workload, &tt.args.AdmissionCheck)
			client := fake.NewFakeClientWithScheme(schema, objs...)
			controller := NewController(client, fake.NullLogger(), record.NewFakeRecorder(1))
			_, err := controller.Reconcile(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			wl := &kueue.Workload{}
			err = client.Get(context.TODO(), types.NamespacedName{Name: tt.args.Workload.Name, Namespace: tt.args.Workload.Namespace}, wl)
			if err != nil {
				t.Errorf("Get admissioncheck error")
				return
			}

			admissionCheck := workload.FindAdmissionCheck(wl.Status.AdmissionChecks, "test-check")
			if diff := cmp.Diff(tt.wantState, admissionCheck, acCmpOptions...); diff != "" {
				t.Errorf("unexpected check (-want/+got):\n%s", diff)
			}
		})
	}
}
