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
	"github.com/fluid-cloudnative/fluid/pkg/utils/fake"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"testing"
)

func Test_acReconciler_Reconcile(t *testing.T) {
	type args struct {
		ctx context.Context
		kueue.AdmissionCheck
		req reconcile.Request
	}
	tests := []struct {
		name            string
		args            args
		wantErr         bool
		wantACCondition *metav1.Condition
	}{
		{
			name: "normal case",
			args: args{
				req: reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: "test",
					},
				},
				AdmissionCheck: kueue.AdmissionCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: kueue.AdmissionCheckSpec{
						ControllerName: ControllerName,
					},
				},
			},
			wantErr: false,
			wantACCondition: &metav1.Condition{
				Type:    kueue.AdmissionCheckActive,
				Status:  metav1.ConditionTrue,
				Reason:  "Active",
				Message: "The admission check is active",
			},
		},
		{
			name: "normal case",
			args: args{
				req: reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name: "test-not-exist",
					},
				},
				AdmissionCheck: kueue.AdmissionCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
					},
					Spec: kueue.AdmissionCheckSpec{
						ControllerName: ControllerName,
					},
				},
			},
			wantErr: false,
		},
	}
	schema := runtime.NewScheme()
	_ = kueue.AddToScheme(schema)

	acCmpOptions := []cmp.Option{
		cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"),
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewFakeClientWithScheme(schema, &tt.args.AdmissionCheck)
			a := &acReconciler{
				client: client,
			}
			_, err := a.Reconcile(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			ac := &kueue.AdmissionCheck{}
			err = client.Get(context.TODO(), types.NamespacedName{Name: tt.args.AdmissionCheck.Name, Namespace: tt.args.AdmissionCheck.Namespace}, ac)
			if err != nil {
				t.Errorf("Get admissioncheck error")
				return
			}

			gotCondition := apimeta.FindStatusCondition(ac.Status.Conditions, kueue.AdmissionCheckActive)
			if diff := cmp.Diff(tt.wantACCondition, gotCondition, acCmpOptions...); diff != "" {
				t.Errorf("unexpected check (-want/+got):\n%s", diff)
			}
		})
	}
}
