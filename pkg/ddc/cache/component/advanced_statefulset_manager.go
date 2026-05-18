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
	"reflect"

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
	// if already created, update it
	if err == nil {
		return s.updateAdvancedStatefulSet(ctx, asts, component)
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

// updateAdvancedStatefulSet updates an existing AdvancedStatefulSet with new component configuration
func (s *AdvancedStatefulSetManager) updateAdvancedStatefulSet(ctx context.Context, existingAsts *workloadv1alpha1.AdvancedStatefulSet, component *common.CacheRuntimeComponentValue) error {
	logger := log.FromContext(ctx)
	logger.Info("start to updating advanced statefulset workload")

	// Create a copy of the existing ASTS for comparison
	astsToUpdate := existingAsts.DeepCopy()

	// Update replicas if changed
	if component.Replicas != *existingAsts.Spec.Replicas {
		logger.Info("replicas changed", "old", *existingAsts.Spec.Replicas, "new", component.Replicas)
		astsToUpdate.Spec.Replicas = &component.Replicas
	}

	// Update PodTemplateSpec - this is key for in-place updates
	// The AdvancedStatefulSet controller will detect changes and perform in-place updates when possible
	matchLabels := getCommonLabelsFromComponent(component)
	podTemplateSpec := component.PodTemplateSpec
	podTemplateSpec.Labels = utils.UnionMapsWithOverride(podTemplateSpec.Labels, matchLabels)

	// Check if pod template has changed
	if !reflect.DeepEqual(existingAsts.Spec.Template, podTemplateSpec) {
		logger.Info("pod template changed, will trigger update")
		astsToUpdate.Spec.Template = podTemplateSpec
	}

	// Only update if there are changes
	if reflect.DeepEqual(existingAsts, astsToUpdate) {
		logger.Info("no changes detected, skip update")
		return nil
	}

	// Update the AdvancedStatefulSet
	err := s.client.Update(ctx, astsToUpdate)
	if err != nil {
		logger.Error(err, "failed to update advanced statefulset")
		return err
	}

	logger.Info("update advanced statefulset workload succeed")
	return nil
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

// SyncComponentSpec synchronizes component specification changes to the AdvancedStatefulSet
// This supports in-place update for compatible fields (e.g., image, resources) without pod recreation
func (s *AdvancedStatefulSetManager) SyncComponentSpec(ctx context.Context, identity *common.ComponentIdentity, version datav1alpha1.VersionSpec) error {
	logger := log.FromContext(ctx)
	logger.Info("start syncing runtime version", "component", identity.Name)

	// Get current AdvancedStatefulSet
	asts := &workloadv1alpha1.AdvancedStatefulSet{}
	err := s.client.Get(ctx, types.NamespacedName{Name: identity.Name, Namespace: identity.Namespace}, asts)
	if err != nil {
		logger.Error(err, "failed to get advanced statefulset")
		return err
	}

	// Construct new image
	newImage := version.Image
	if version.ImageTag != "" {
		newImage = newImage + ":" + version.ImageTag
	}

	// Check if image needs update
	if len(asts.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("no containers found in advanced statefulset %s/%s", identity.Namespace, identity.Name)
	}

	// only update the first container
	currentImage := asts.Spec.Template.Spec.Containers[0].Image
	if currentImage == newImage {
		logger.Info("image already up to date, skip update", "image", newImage)
		return nil
	}

	logger.Info("image changed, will patch advanced statefulset", "old", currentImage, "new", newImage)

	// Create a copy for patching to avoid modifying the original object
	astsToUpdate := asts.DeepCopy()
	astsToUpdate.Spec.Template.Spec.Containers[0].Image = newImage

	// Set ImagePullPolicy if specified
	if version.ImagePullPolicy != "" {
		astsToUpdate.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullPolicy(version.ImagePullPolicy)
	}

	// Create patch using the original object as base
	patch := client.MergeFrom(asts)

	// Apply patch
	err = s.client.Patch(ctx, astsToUpdate, patch)
	if err != nil {
		logger.Error(err, "failed to patch advanced statefulset")
		return err
	}

	logger.Info("successfully patched advanced statefulset with new image")
	return nil
}
