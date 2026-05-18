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

package component

import (
	"context"
	"fmt"

	workloadv1alpha1 "github.com/fluid-cloudnative/advanced-statefulset/api/workload/v1alpha1"
	datav1alpha1 "github.com/fluid-cloudnative/fluid/api/v1alpha1"
	"github.com/fluid-cloudnative/fluid/pkg/common"
	"github.com/fluid-cloudnative/fluid/pkg/utils"
	"github.com/fluid-cloudnative/fluid/pkg/utils/kubeclient"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type AdvancedStatefulSetManager struct {
	client client.Client
}

func newAdvancedStatefulSetManager(client client.Client) *AdvancedStatefulSetManager {
	return &AdvancedStatefulSetManager{client: client}
}

func (s *AdvancedStatefulSetManager) Reconciler(ctx context.Context, component *common.CacheRuntimeComponentValue) error {
	if err := s.reconcileStatefulSet(ctx, component); err != nil {
		return err
	}

	return reconcileService(ctx, s.client, component)
}

func (s *AdvancedStatefulSetManager) GetNodeAffinity(identity *common.ComponentIdentity) (*corev1.NodeAffinity, error) {
	asts := &workloadv1alpha1.AdvancedStatefulSet{}
	err := s.client.Get(context.TODO(), types.NamespacedName{Name: identity.Name, Namespace: identity.Namespace}, asts)
	if err != nil {
		return nil, err
	}

	affinity := kubeclient.MergeNodeSelectorAndNodeAffinity(asts.Spec.Template.Spec.NodeSelector, asts.Spec.Template.Spec.Affinity)
	return affinity, nil
}

func (s *AdvancedStatefulSetManager) reconcileStatefulSet(ctx context.Context, component *common.CacheRuntimeComponentValue) error {
	logger := log.FromContext(ctx)
	logger.Info("start to reconciling advanced statefulset workload")

	asts := &workloadv1alpha1.AdvancedStatefulSet{}
	err := s.client.Get(ctx, types.NamespacedName{Name: component.Name, Namespace: component.Namespace}, asts)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	// return if already created
	if err == nil {
		return nil
	}
	// create the advanced stateful set
	asts = s.constructAdvancedStatefulSet(component)
	err = s.client.Create(ctx, asts)
	if err != nil {
		return err
	}
	logger.Info("create advanced statefulset workload succeed")
	return nil
}
func (s *AdvancedStatefulSetManager) constructAdvancedStatefulSet(component *common.CacheRuntimeComponentValue) *workloadv1alpha1.AdvancedStatefulSet {
	matchLabels := getCommonLabelsFromComponent(component)

	podTemplateSpec := component.PodTemplateSpec
	podTemplateSpec.Labels = utils.UnionMapsWithOverride(podTemplateSpec.Labels, matchLabels)

	trueVar := true

	// Configure rolling update strategy with in-place update support
	rollingUpdateStrategy := &workloadv1alpha1.RollingUpdateStatefulSetStrategy{
		PodUpdatePolicy: workloadv1alpha1.InPlaceIfPossiblePodUpdateStrategyType,
	}

	asts := &workloadv1alpha1.AdvancedStatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      component.Name,
			Namespace: component.Namespace,
			Labels:    matchLabels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         component.Owner.APIVersion,
					Kind:               component.Owner.Kind,
					Name:               component.Owner.Name,
					UID:                types.UID(component.Owner.UID),
					BlockOwnerDeletion: &trueVar,
					Controller:         &trueVar,
				},
			},
		},
		Spec: workloadv1alpha1.AdvancedStatefulSetSpec{
			Replicas:            &component.Replicas,
			Template:            podTemplateSpec,
			PodManagementPolicy: appsv1.ParallelPodManagement,
			Selector: &metav1.LabelSelector{
				MatchLabels: matchLabels,
			},
			UpdateStrategy: workloadv1alpha1.StatefulSetUpdateStrategy{
				Type:          appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: rollingUpdateStrategy,
			},
		},
	}

	// Set ServiceName if service is configured
	if component.Service != nil {
		asts.Spec.ServiceName = component.Service.Name
	}

	return asts
}

func (s *AdvancedStatefulSetManager) ConstructComponentStatus(ctx context.Context, identity *common.ComponentIdentity) (datav1alpha1.RuntimeComponentStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("start to ConstructComponentStatus")

	asts := &workloadv1alpha1.AdvancedStatefulSet{}
	err := s.client.Get(ctx, types.NamespacedName{Name: identity.Name, Namespace: identity.Namespace}, asts)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to get component: %s/%s", identity.Namespace, identity.Name))
		return datav1alpha1.RuntimeComponentStatus{}, err
	}

	desiredReplicas := *asts.Spec.Replicas
	readyReplicas := asts.Status.ReadyReplicas

	runtimePhase := datav1alpha1.RuntimePhaseNotReady
	if desiredReplicas == readyReplicas {
		runtimePhase = datav1alpha1.RuntimePhaseReady
	}

	// AvailableReplicas can be greater than CurrentReplicas (Kubernetes API allows this)
	unavailableReplicas := asts.Status.CurrentReplicas - asts.Status.AvailableReplicas
	if unavailableReplicas < 0 {
		unavailableReplicas = 0
	}

	return datav1alpha1.RuntimeComponentStatus{
		Phase:               runtimePhase,
		DesiredReplicas:     desiredReplicas,
		CurrentReplicas:     asts.Status.CurrentReplicas,
		AvailableReplicas:   asts.Status.AvailableReplicas,
		UnavailableReplicas: unavailableReplicas,
		ReadyReplicas:       readyReplicas,
	}, nil
}
