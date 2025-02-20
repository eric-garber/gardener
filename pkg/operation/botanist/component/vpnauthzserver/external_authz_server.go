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

package vpnauthzserver

import (
	"context"
	"fmt"

	"github.com/Masterminds/semver"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	istionetworkingv1beta1 "istio.io/api/networking/v1beta1"
	networkingv1beta1 "istio.io/client-go/pkg/apis/networking/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/controllerutils"
	"github.com/gardener/gardener/pkg/operation/botanist/component"
	"github.com/gardener/gardener/pkg/utils"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/version"
)

const (
	// ServerPort is the port exposed by the reversed-vpn-auth-server.
	ServerPort = 9001
	// Name is the name of the reversed-vpn-auth-server.
	Name = "reversed-vpn-auth-server"
)

// New creates a new instance of DeployWaiter for the ReversedVPN authorization server.
func New(
	client client.Client,
	namespace string,
	imageExtAuthzServer string,
	kubernetesVersion *semver.Version,
) component.DeployWaiter {
	return &authzServer{
		client:              client,
		namespace:           namespace,
		imageExtAuthzServer: imageExtAuthzServer,
		kubernetesVersion:   kubernetesVersion,
	}
}

type authzServer struct {
	client              client.Client
	namespace           string
	imageExtAuthzServer string
	kubernetesVersion   *semver.Version
}

func (a *authzServer) Deploy(ctx context.Context) error {
	var (
		deployment      = a.emptyDeployment()
		destinationRule = a.emptyDestinationRule()
		service         = a.emptyService()
		virtualService  = a.emptyVirtualService()
		vpa             = a.emptyVPA()
		pdb             client.Object

		vpaUpdateMode = vpaautoscalingv1.UpdateModeAuto
	)

	pdb = a.emptyPDB()

	if _, err := controllerutils.GetAndCreateOrMergePatch(ctx, a.client, deployment, func() error {
		maxSurge := intstr.FromInt(100)
		maxUnavailable := intstr.FromInt(0)
		deployment.Labels = utils.MergeStringMaps(getLabels(), map[string]string{
			resourcesv1alpha1.HighAvailabilityConfigType: resourcesv1alpha1.HighAvailabilityConfigTypeServer,
		})
		deployment.Spec = appsv1.DeploymentSpec{
			Replicas:             pointer.Int32(1),
			RevisionHistoryLimit: pointer.Int32(2),
			Selector:             &metav1.LabelSelector{MatchLabels: getLabels()},
			Strategy: appsv1.DeploymentStrategy{
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: getLabels(),
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: pointer.Bool(false),
					PriorityClassName:            v1beta1constants.PriorityClassNameSeedSystem900,
					DNSPolicy:                    corev1.DNSDefault, // make sure to not use the coredns for DNS resolution.
					Containers: []corev1.Container{
						{
							Name:            Name,
							Image:           a.imageExtAuthzServer,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports: []corev1.ContainerPort{
								{
									Name:          "grpc-authz",
									ContainerPort: 9001,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("100Mi"),
								},
							},
						},
					},
				},
			},
		}
		return nil
	}); err != nil {
		return err
	}

	if _, err := controllerutils.GetAndCreateOrMergePatch(ctx, a.client, destinationRule, func() error {
		destinationRule.Spec = istionetworkingv1beta1.DestinationRule{
			ExportTo: []string{"*"},
			Host:     fmt.Sprintf("%s.%s.svc.%s", Name, a.namespace, gardencorev1beta1.DefaultDomain),
			TrafficPolicy: &istionetworkingv1beta1.TrafficPolicy{
				ConnectionPool: &istionetworkingv1beta1.ConnectionPoolSettings{
					Tcp: &istionetworkingv1beta1.ConnectionPoolSettings_TCPSettings{
						MaxConnections: 5000,
						TcpKeepalive: &istionetworkingv1beta1.ConnectionPoolSettings_TCPSettings_TcpKeepalive{
							Interval: &durationpb.Duration{
								Seconds: 75,
							},
							Time: &durationpb.Duration{
								Seconds: 7200,
							},
						},
					},
				},
				LoadBalancer: &istionetworkingv1beta1.LoadBalancerSettings{
					LocalityLbSetting: &istionetworkingv1beta1.LocalityLoadBalancerSetting{
						Enabled:          &wrapperspb.BoolValue{Value: true},
						FailoverPriority: []string{corev1.LabelTopologyZone},
					},
				},
				// OutlierDetection is required for locality settings to take effect
				OutlierDetection: &istionetworkingv1beta1.OutlierDetection{
					MinHealthPercent: 0,
				},
				Tls: &istionetworkingv1beta1.ClientTLSSettings{
					Mode: istionetworkingv1beta1.ClientTLSSettings_DISABLE,
				},
			},
		}
		return nil
	}); err != nil {
		return err
	}

	if _, err := controllerutils.GetAndCreateOrMergePatch(ctx, a.client, service, func() error {
		service.Annotations = map[string]string{
			"networking.istio.io/exportTo": "*",
		}
		service.Spec.Type = corev1.ServiceTypeClusterIP
		service.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "grpc-authz",
				Port:       ServerPort,
				TargetPort: intstr.FromInt(ServerPort),
				Protocol:   corev1.ProtocolTCP,
			},
		}
		service.Spec.Selector = getLabels()
		return nil
	}); err != nil {
		return err
	}

	if _, err := controllerutils.GetAndCreateOrMergePatch(ctx, a.client, virtualService, func() error {
		virtualService.Spec = istionetworkingv1beta1.VirtualService{
			ExportTo: []string{"*"},
			Hosts:    []string{fmt.Sprintf("%s.%s.svc.%s", Name, a.namespace, gardencorev1beta1.DefaultDomain)},
			Http: []*istionetworkingv1beta1.HTTPRoute{{
				Route: []*istionetworkingv1beta1.HTTPRouteDestination{{
					Destination: &istionetworkingv1beta1.Destination{
						Host: Name,
						Port: &istionetworkingv1beta1.PortSelector{Number: ServerPort},
					},
				}},
			}},
		}
		return nil
	}); err != nil {
		return err
	}

	if _, err := controllerutils.GetAndCreateOrMergePatch(ctx, a.client, vpa, func() error {
		vpa.Spec.TargetRef = &autoscalingv1.CrossVersionObjectReference{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
			Name:       Name,
		}
		vpa.Spec.UpdatePolicy = &vpaautoscalingv1.PodUpdatePolicy{
			UpdateMode: &vpaUpdateMode,
		}
		vpa.Spec.ResourcePolicy = &vpaautoscalingv1.PodResourcePolicy{
			ContainerPolicies: []vpaautoscalingv1.ContainerResourcePolicy{
				{
					ContainerName: Name,
					MinAllowed: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					},
				},
			},
		}
		return nil
	}); err != nil {
		return err
	}

	if err := a.reconcilePodDisruptionBudget(ctx, pdb); err != nil {
		return err
	}

	return nil
}

func (a *authzServer) Destroy(ctx context.Context) error {
	return kutil.DeleteObjects(
		ctx,
		a.client,
		a.emptyDeployment(),
		a.emptyDestinationRule(),
		a.emptyService(),
		a.emptyVirtualService(),
		a.emptyVPA(),
		a.emptyPDB(),
	)
}

func (a *authzServer) Wait(_ context.Context) error        { return nil }
func (a *authzServer) WaitCleanup(_ context.Context) error { return nil }

func (a *authzServer) emptyDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: Name, Namespace: a.namespace}}
}

func (a *authzServer) emptyDestinationRule() *networkingv1beta1.DestinationRule {
	return &networkingv1beta1.DestinationRule{ObjectMeta: metav1.ObjectMeta{Name: Name, Namespace: a.namespace}}
}

func (a *authzServer) emptyService() *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: Name, Namespace: a.namespace}}
}

func (a *authzServer) emptyVirtualService() *networkingv1beta1.VirtualService {
	return &networkingv1beta1.VirtualService{ObjectMeta: metav1.ObjectMeta{Name: Name, Namespace: a.namespace}}
}

func (a *authzServer) emptyVPA() *vpaautoscalingv1.VerticalPodAutoscaler {
	return &vpaautoscalingv1.VerticalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: Name + "-vpa", Namespace: a.namespace}}
}

func (a *authzServer) emptyPDB() client.Object {
	pdbObjectMeta := metav1.ObjectMeta{
		Name:      Name + "-pdb",
		Namespace: a.namespace,
	}

	if version.ConstraintK8sGreaterEqual121.Check(a.kubernetesVersion) {
		return &policyv1.PodDisruptionBudget{
			ObjectMeta: pdbObjectMeta,
		}
	}
	return &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: pdbObjectMeta,
	}

}

func (a *authzServer) reconcilePodDisruptionBudget(ctx context.Context, obj client.Object) error {
	var (
		maxUnavailable = intstr.FromInt(1)
		pdbSelector    = &metav1.LabelSelector{
			MatchLabels: getLabels(),
		}
	)

	_, err := controllerutils.GetAndCreateOrMergePatch(ctx, a.client, obj, func() error {
		switch pdb := obj.(type) {
		case *policyv1.PodDisruptionBudget:
			pdb.Labels = getLabels()
			pdb.Spec = policyv1.PodDisruptionBudgetSpec{
				MaxUnavailable: &maxUnavailable,
				Selector:       pdbSelector,
			}
		case *policyv1beta1.PodDisruptionBudget:
			pdb.Labels = getLabels()
			pdb.Spec = policyv1beta1.PodDisruptionBudgetSpec{
				MaxUnavailable: &maxUnavailable,
				Selector:       pdbSelector,
			}
		}
		return nil
	})

	return err
}

func getLabels() map[string]string {
	return map[string]string{
		v1beta1constants.LabelApp: Name,
	}
}
