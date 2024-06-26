/*
  Copyright 2023 The Fluid Authors.

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

package kubeclient

import (
	"github.com/fluid-cloudnative/fluid/pkg/utils/fake"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"testing"
)

func TestGetJob(t *testing.T) {
	mockJobName := "fluid-test-job"
	mockJobNamespace := "default"
	initJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mockJobName,
			Namespace: mockJobNamespace,
		},
	}

	fakeClient := fake.NewFakeClient(initJob)

	testCases := map[string]struct {
		name      string
		namespace string
		wantName  string
		notFound  bool
	}{
		"test get DataLoad Job case 1": {
			name:      mockJobName,
			namespace: mockJobNamespace,
			wantName:  mockJobName,
			notFound:  false,
		},
		"test get DataLoad Job case 2": {
			name:      mockJobName + "not-exist",
			namespace: mockJobNamespace,
			wantName:  "",
			notFound:  true,
		},
	}

	for k, item := range testCases {
		gotJob, err := GetJob(fakeClient, item.name, item.namespace)
		if item.notFound {
			if err == nil && gotJob != nil {
				t.Errorf("%s check failure, want get err, but get nil", k)
			}
		} else {
			if gotJob.Name != item.wantName {
				t.Errorf("%s check failure, want DataLoad Job name:%s, got DataLoad Job name:%s", k, item.wantName, gotJob.Name)
			}
		}
	}
}

func TestGetFinishedJobCondition(t *testing.T) {
	type args struct {
		job *batchv1.Job
	}
	tests := []struct {
		name string
		args args
		want *batchv1.JobCondition
	}{
		{
			name: "nil conditions",
			args: args{
				job: &batchv1.Job{
					Status: batchv1.JobStatus{
						Conditions: nil,
					},
				},
			},
			want: nil,
		},
		{
			name: "JobComplete",
			args: args{
				job: &batchv1.Job{
					Status: batchv1.JobStatus{
						Conditions: []batchv1.JobCondition{
							{
								Type: batchv1.JobSuspended,
							},

							{
								Type: batchv1.JobComplete,
							},
						},
					},
				},
			},
			want: &batchv1.JobCondition{
				Type: batchv1.JobComplete,
			},
		},

		{
			name: "JobFailed",
			args: args{
				job: &batchv1.Job{
					Status: batchv1.JobStatus{
						Conditions: []batchv1.JobCondition{
							{
								Type: batchv1.JobSuspended,
							},

							{
								Type: batchv1.JobFailed,
							},
						},
					},
				},
			},
			want: &batchv1.JobCondition{
				Type: batchv1.JobFailed,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetFinishedJobCondition(tt.args.job); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetFinishedJobCondition() = %v, want %v", got, tt.want)
			}
		})
	}
}
