// Copyright (c) 2018 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package care

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gardencorev1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/gardenlet/apis/config"
	"github.com/gardener/gardener/pkg/utils/kubernetes/health"
)

// Reconciler reconciles ControllerInstallations, checks their health status and reports it via conditions.
type Reconciler struct {
	GardenClient    client.Client
	SeedClient      client.Client
	Config          config.ControllerInstallationCareControllerConfiguration
	GardenNamespace string
}

// Reconcile reconciles ControllerInstallations, checks their health status and reports it via conditions.
func (r *Reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	controllerInstallation := &gardencorev1beta1.ControllerInstallation{}
	if err := r.GardenClient.Get(ctx, request.NamespacedName, controllerInstallation); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Object is gone, stop reconciling")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}

	if controllerInstallation.DeletionTimestamp != nil {
		return reconcile.Result{}, nil
	}

	var (
		conditionControllerInstallationInstalled   = gardencorev1beta1helper.GetOrInitCondition(controllerInstallation.Status.Conditions, gardencorev1beta1.ControllerInstallationInstalled)
		conditionControllerInstallationHealthy     = gardencorev1beta1helper.GetOrInitCondition(controllerInstallation.Status.Conditions, gardencorev1beta1.ControllerInstallationHealthy)
		conditionControllerInstallationProgressing = gardencorev1beta1helper.GetOrInitCondition(controllerInstallation.Status.Conditions, gardencorev1beta1.ControllerInstallationProgressing)
	)

	managedResource := &resourcesv1alpha1.ManagedResource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controllerInstallation.Name,
			Namespace: r.GardenNamespace,
		},
	}

	if err := r.SeedClient.Get(ctx, client.ObjectKeyFromObject(managedResource), managedResource); err != nil {
		msg := fmt.Sprintf("Failed to get ManagedResource %q: %s", client.ObjectKeyFromObject(managedResource).String(), err.Error())
		conditionControllerInstallationInstalled = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationInstalled, gardencorev1beta1.ConditionUnknown, "SeedReadError", msg)
		conditionControllerInstallationHealthy = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationHealthy, gardencorev1beta1.ConditionUnknown, "SeedReadError", msg)
		conditionControllerInstallationProgressing = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationProgressing, gardencorev1beta1.ConditionUnknown, "SeedReadError", msg)

		patch := client.StrategicMergeFrom(controllerInstallation.DeepCopy())
		controllerInstallation.Status.Conditions = gardencorev1beta1helper.MergeConditions(controllerInstallation.Status.Conditions, conditionControllerInstallationHealthy, conditionControllerInstallationInstalled, conditionControllerInstallationProgressing)
		if err := r.GardenClient.Status().Patch(ctx, controllerInstallation, patch); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to patch conditions: %w", err)
		}

		if apierrors.IsNotFound(err) {
			log.Info("ManagedResource was not found yet, requeuing", "managedResource", client.ObjectKeyFromObject(managedResource))
			return reconcile.Result{RequeueAfter: time.Second}, nil
		}

		return reconcile.Result{}, err
	}

	if err := health.CheckManagedResourceApplied(managedResource); err != nil {
		conditionControllerInstallationInstalled = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationInstalled, gardencorev1beta1.ConditionFalse, "InstallationPending", err.Error())
	} else {
		conditionControllerInstallationInstalled = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationInstalled, gardencorev1beta1.ConditionTrue, "InstallationSuccessful", "The controller was successfully installed in the seed cluster.")
	}

	if err := health.CheckManagedResourceHealthy(managedResource); err != nil {
		conditionControllerInstallationHealthy = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationHealthy, gardencorev1beta1.ConditionFalse, "ControllerNotHealthy", err.Error())
	} else {
		conditionControllerInstallationHealthy = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationHealthy, gardencorev1beta1.ConditionTrue, "ControllerHealthy", "The controller running in the seed cluster is healthy.")
	}

	if err := health.CheckManagedResourceProgressing(managedResource); err != nil {
		conditionControllerInstallationProgressing = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationProgressing, gardencorev1beta1.ConditionTrue, "ControllerNotRolledOut", err.Error())
	} else {
		conditionControllerInstallationProgressing = gardencorev1beta1helper.UpdatedCondition(conditionControllerInstallationProgressing, gardencorev1beta1.ConditionFalse, "ControllerRolledOut", "The controller has been rolled out successfully.")
	}

	patch := client.StrategicMergeFrom(controllerInstallation.DeepCopy())
	controllerInstallation.Status.Conditions = gardencorev1beta1helper.MergeConditions(controllerInstallation.Status.Conditions, conditionControllerInstallationHealthy, conditionControllerInstallationInstalled, conditionControllerInstallationProgressing)
	if err := r.GardenClient.Status().Patch(ctx, controllerInstallation, patch); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to patch conditions: %w", err)
	}

	return reconcile.Result{RequeueAfter: r.Config.SyncPeriod.Duration}, nil
}
