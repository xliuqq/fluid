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
	errors2 "errors"
	"github.com/fluid-cloudnative/fluid/pkg/common"
	"github.com/fluid-cloudnative/fluid/pkg/utils"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"sigs.k8s.io/kueue/pkg/workload"

	"time"
)

type Controller struct {
	client client.Client
	log    logr.Logger
	record record.EventRecorder
}

var _ reconcile.Reconciler = (*Controller)(nil)

func NewController(client client.Client, log logr.Logger, record record.EventRecorder) *Controller {
	return &Controller{
		client: client,
		record: record,
		log:    log,
	}
}

func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	wl := &kueue.Workload{}
	err := c.client.Get(ctx, req.NamespacedName, wl)
	if err != nil {
		c.log.Error(err, "Failed to get wl", "NamespacedName", req.NamespacedName)
		return utils.RequeueIfError(err)
	}

	// already run, no requeue.
	if workload.IsAdmitted(wl) {
		return utils.NoRequeue()
	}

	podSets := wl.Spec.PodSets
	// currently only support batch job, which only has one pod set.
	if len(podSets) != 1 {
		return utils.NoRequeue()
	}
	podLabels := podSets[0].Template.Labels

	opType, ok := podLabels[WaitDataOpType]
	// no need to wait data operation
	if !ok {
		return utils.NoRequeue()
	}

	opNamespace := podLabels[WaitDataOpNameSpace]
	opName := podLabels[WaitDataOpName]
	opNamespacedName := types.NamespacedName{Name: opName, Namespace: opNamespace}

	opStatus, err := utils.GetDataOperationStatus(c.client, opType, opNamespacedName)
	if err != nil {
		c.log.Error(err, "Get data operation status failed", "data operation namespaced name", opNamespacedName)
		if errors.IsBadRequest(err) {
			c.record.Event(wl, v1.EventTypeWarning, common.DataOperationNotValid, err.Error())
			return utils.NoRequeue()
		}
		return utils.RequeueIfError(err)
	}

	var opStatusPhase = opStatus.Phase

	// operation failed, reject this workload.
	if opStatusPhase == common.PhaseFailed {
		c.record.Event(wl, v1.EventTypeWarning, "Waiting DataOperation failed", "reject workload")
		err = updateWorkload(c.client, wl, kueue.CheckStateRejected, "waited data operation is failed")
		if err != nil {
			c.log.Error(err, "update wl status failed")
			return utils.RequeueIfError(err)
		}
		return utils.NoRequeue()
	}

	// operation succeed, will execute this wl.
	if opStatusPhase == common.PhaseComplete {
		c.record.Event(wl, v1.EventTypeNormal, "Waiting DataOperation completed", "run workload")
		err = updateWorkload(c.client, wl, kueue.CheckStateReady, "waited data operation is completed")
		if err != nil {
			c.log.Error(err, "update wl status failed")
			return utils.RequeueIfError(err)
		}
		return utils.NoRequeue()
	}
	// other state, keep wait.
	return utils.RequeueAfterInterval(10 * time.Second)
}

func updateWorkload(c client.Client, wl *kueue.Workload, state kueue.CheckState, message string) error {
	checkState, err := getAdmissionCheckState(c, wl.Status.AdmissionChecks, ControllerName)
	if err != nil {
		return err
	}

	if checkState == nil {
		return errors2.New("can not find admission check state for " + ControllerName)
	}

	checkState.State = state
	checkState.Message = message

	workload.SetAdmissionCheckState(&wl.Status.AdmissionChecks, *checkState)

	err = c.Status().Update(context.TODO(), wl)

	return err
}

func getAdmissionCheckState(c client.Client, states []kueue.AdmissionCheckState, controllerName string) (*kueue.AdmissionCheckState, error) {

	for _, state := range states {
		ac := &kueue.AdmissionCheck{}

		if err := c.Get(context.TODO(), types.NamespacedName{Name: state.Name}, ac); err != nil {
			return nil, err
		}

		if ac.Spec.ControllerName == controllerName {
			return &state, nil
		}
	}
	return nil, nil
}

func (c *Controller) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
	// no need to watch the admission check changes.
	err := ctrl.NewControllerManagedBy(mgr).
		WithOptions(options).
		For(&kueue.Workload{}).
		Complete(c)
	if err != nil {
		return err
	}

	acReconciler := &acReconciler{
		client: c.client,
	}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(options).
		For(&kueue.AdmissionCheck{}).
		Complete(acReconciler)
}
