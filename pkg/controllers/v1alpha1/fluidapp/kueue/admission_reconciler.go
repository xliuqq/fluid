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
	"github.com/fluid-cloudnative/fluid/pkg/utils"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
)

// AdmissionCheck Reconciler
type acReconciler struct {
	client client.Client
}

var _ reconcile.Reconciler = (*acReconciler)(nil)

// Reconcile reconciles the AdmissionCheck
func (a *acReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	ac := &kueue.AdmissionCheck{}
	err := a.client.Get(ctx, req.NamespacedName, ac)
	if err != nil || ac.Spec.ControllerName != ControllerName {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// add the AdmissionCheck Active Condition
	currentCondition := ptr.Deref(apimeta.FindStatusCondition(ac.Status.Conditions, kueue.AdmissionCheckActive), metav1.Condition{})
	newCondition := metav1.Condition{
		Type:    kueue.AdmissionCheckActive,
		Status:  metav1.ConditionTrue,
		Reason:  "Active",
		Message: "The admission check is active",
	}

	// current not use the Parameters field, no need to check it.

	// update condition
	if currentCondition.Status != newCondition.Status {
		apimeta.SetStatusCondition(&ac.Status.Conditions, newCondition)
		err = a.client.Status().Update(ctx, ac)
		return utils.RequeueIfError(err)
	}
	return utils.NoRequeue()
}
