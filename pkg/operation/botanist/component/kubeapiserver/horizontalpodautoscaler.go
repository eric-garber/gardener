// Copyright (c) 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package kubeapiserver

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	autoscalingv2beta1 "k8s.io/api/autoscaling/v2beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	"github.com/gardener/gardener/pkg/controllerutils"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	versionutils "github.com/gardener/gardener/pkg/utils/version"
)

const (
	hpaTargetAverageUtilizationCPU    int32 = 80
	hpaTargetAverageUtilizationMemory int32 = 80
)

func (k *kubeAPIServer) emptyHorizontalPodAutoscaler() client.Object {
	hpaObjectMeta := metav1.ObjectMeta{
		Name:      v1beta1constants.DeploymentNameKubeAPIServer,
		Namespace: k.namespace,
	}

	if versionutils.ConstraintK8sGreaterEqual123.Check(k.values.RuntimeVersion) {
		return &autoscalingv2.HorizontalPodAutoscaler{
			ObjectMeta: hpaObjectMeta,
		}
	}
	return &autoscalingv2beta1.HorizontalPodAutoscaler{
		ObjectMeta: hpaObjectMeta,
	}
}

func (k *kubeAPIServer) reconcileHorizontalPodAutoscaler(ctx context.Context, obj client.Object, deployment *appsv1.Deployment) error {
	if k.values.Autoscaling.HVPAEnabled ||
		k.values.Autoscaling.Replicas == nil ||
		*k.values.Autoscaling.Replicas == 0 {

		return kutil.DeleteObject(ctx, k.client.Client(), obj)
	}

	_, err := controllerutils.GetAndCreateOrMergePatch(ctx, k.client.Client(), obj, func() error {
		switch hpa := obj.(type) {
		case *autoscalingv2.HorizontalPodAutoscaler:
			hpa.Spec = autoscalingv2.HorizontalPodAutoscalerSpec{
				MinReplicas: &k.values.Autoscaling.MinReplicas,
				MaxReplicas: k.values.Autoscaling.MaxReplicas,
				ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
					APIVersion: appsv1.SchemeGroupVersion.String(),
					Kind:       "Deployment",
					Name:       deployment.Name,
				},
				Metrics: []autoscalingv2.MetricSpec{
					{
						Type: autoscalingv2.ResourceMetricSourceType,
						Resource: &autoscalingv2.ResourceMetricSource{
							Name: corev1.ResourceCPU,
							Target: autoscalingv2.MetricTarget{
								Type:               autoscalingv2.UtilizationMetricType,
								AverageUtilization: pointer.Int32(hpaTargetAverageUtilizationCPU),
							},
						},
					},
					{
						Type: autoscalingv2.ResourceMetricSourceType,
						Resource: &autoscalingv2.ResourceMetricSource{
							Name: corev1.ResourceMemory,
							Target: autoscalingv2.MetricTarget{
								Type:               autoscalingv2.UtilizationMetricType,
								AverageUtilization: pointer.Int32(hpaTargetAverageUtilizationMemory),
							},
						},
					},
				},
			}
		case *autoscalingv2beta1.HorizontalPodAutoscaler:
			hpa.Spec = autoscalingv2beta1.HorizontalPodAutoscalerSpec{
				MinReplicas: &k.values.Autoscaling.MinReplicas,
				MaxReplicas: k.values.Autoscaling.MaxReplicas,
				ScaleTargetRef: autoscalingv2beta1.CrossVersionObjectReference{
					APIVersion: appsv1.SchemeGroupVersion.String(),
					Kind:       "Deployment",
					Name:       deployment.Name,
				},
				Metrics: []autoscalingv2beta1.MetricSpec{
					{
						Type: autoscalingv2beta1.ResourceMetricSourceType,
						Resource: &autoscalingv2beta1.ResourceMetricSource{
							Name:                     corev1.ResourceCPU,
							TargetAverageUtilization: pointer.Int32(hpaTargetAverageUtilizationCPU),
						},
					},
					{
						Type: autoscalingv2beta1.ResourceMetricSourceType,
						Resource: &autoscalingv2beta1.ResourceMetricSource{
							Name:                     corev1.ResourceMemory,
							TargetAverageUtilization: pointer.Int32(hpaTargetAverageUtilizationMemory),
						},
					},
				},
			}
		}

		return nil
	})

	return err
}
