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

package seed

import (
	"context"
	"fmt"
	"net"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	gardencorev1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	"github.com/gardener/gardener/pkg/chartrenderer"
	"github.com/gardener/gardener/pkg/features"
	"github.com/gardener/gardener/pkg/gardenlet/apis/config"
	gardenletfeatures "github.com/gardener/gardener/pkg/gardenlet/features"
	"github.com/gardener/gardener/pkg/operation"
	"github.com/gardener/gardener/pkg/operation/botanist/component"
	"github.com/gardener/gardener/pkg/operation/botanist/component/dependencywatchdog"
	"github.com/gardener/gardener/pkg/operation/botanist/component/etcd"
	"github.com/gardener/gardener/pkg/operation/botanist/component/istio"
	"github.com/gardener/gardener/pkg/operation/botanist/component/kubeapiserver"
	"github.com/gardener/gardener/pkg/operation/botanist/component/kubestatemetrics"
	"github.com/gardener/gardener/pkg/operation/botanist/component/networkpolicies"
	"github.com/gardener/gardener/pkg/operation/botanist/component/nodelocaldns"
	"github.com/gardener/gardener/pkg/operation/botanist/component/seedsystem"
	"github.com/gardener/gardener/pkg/operation/botanist/component/vpnauthzserver"
	"github.com/gardener/gardener/pkg/operation/botanist/component/vpnseedserver"
	"github.com/gardener/gardener/pkg/operation/common"
	seedpkg "github.com/gardener/gardener/pkg/operation/seed"
	"github.com/gardener/gardener/pkg/utils"
	gutil "github.com/gardener/gardener/pkg/utils/gardener"
	"github.com/gardener/gardener/pkg/utils/images"
	"github.com/gardener/gardener/pkg/utils/imagevector"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"

	"github.com/Masterminds/semver"
	restarterapi "github.com/gardener/dependency-watchdog/pkg/restarter/api"
	scalerapi "github.com/gardener/dependency-watchdog/pkg/scaler/api"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func defaultKubeStateMetrics(
	c client.Client,
	imageVector imagevector.ImageVector,
	seedVersion *semver.Version,
	gardenNamespaceName string,
) (
	component.DeployWaiter,
	error,
) {
	image, err := imageVector.FindImage(images.ImageNameKubeStateMetrics, imagevector.TargetVersion(seedVersion.String()))
	if err != nil {
		return nil, err
	}

	return kubestatemetrics.New(c, gardenNamespaceName, nil, kubestatemetrics.Values{
		ClusterType: component.ClusterTypeSeed,
		Image:       image.String(),
		Replicas:    1,
	}), nil
}

func defaultIstio(
	seedClient client.Client,
	imageVector imagevector.ImageVector,
	chartRenderer chartrenderer.Interface,
	seed *seedpkg.Seed,
	conf *config.GardenletConfiguration,
	sniEnabledOrInUse bool,
) (
	component.DeployWaiter,
	error,
) {
	istiodImage, err := imageVector.FindImage(images.ImageNameIstioIstiod)
	if err != nil {
		return nil, err
	}

	igwImage, err := imageVector.FindImage(images.ImageNameIstioProxy)
	if err != nil {
		return nil, err
	}

	gardenSeed := seed.GetInfo()

	var minReplicas *int
	var maxReplicas *int
	if len(gardenSeed.Spec.Provider.Zones) > 1 {
		// Each availability zone should have at least 2 replicas as on some infrastructures each
		// zonal load balancer is exposed individually via its own IP address. Therefore, having
		// just one replica may negatively affect availability.
		minReplicas = pointer.Int(len(gardenSeed.Spec.Provider.Zones) * 2)
		// The default configuration without availability zones has 5 as the maximum amount of
		// replicas, which apparently works in all known Gardener scenarios. Reducing it to less
		// per zone gives some room for autoscaling while it is assumed to never reach the maximum.
		maxReplicas = pointer.Int(len(gardenSeed.Spec.Provider.Zones) * 4)
	}

	defaultIngressGatewayConfig := istio.IngressValues{
		TrustDomain:           gardencorev1beta1.DefaultDomain,
		Image:                 igwImage.String(),
		IstiodNamespace:       v1beta1constants.IstioSystemNamespace,
		Annotations:           seed.GetLoadBalancerServiceAnnotations(),
		ExternalTrafficPolicy: seed.GetLoadBalancerServiceExternalTrafficPolicy(),
		MinReplicas:           minReplicas,
		MaxReplicas:           maxReplicas,
		Ports:                 []corev1.ServicePort{},
		LoadBalancerIP:        conf.SNI.Ingress.ServiceExternalIP,
		Labels:                operation.GetIstioZoneLabels(conf.SNI.Ingress.Labels, nil),
	}

	// even if SNI is being disabled, the existing ports must stay the same
	// until all APIServer SNI resources are removed.
	if sniEnabledOrInUse {
		defaultIngressGatewayConfig.Ports = append(
			defaultIngressGatewayConfig.Ports,
			corev1.ServicePort{Name: "proxy", Port: 8443, TargetPort: intstr.FromInt(8443)},
			corev1.ServicePort{Name: "tcp", Port: 443, TargetPort: intstr.FromInt(9443)},
			corev1.ServicePort{Name: "tls-tunnel", Port: vpnseedserver.GatewayPort, TargetPort: intstr.FromInt(vpnseedserver.GatewayPort)},
		)
	}

	istioIngressGateway := []istio.IngressGateway{{
		Values:    defaultIngressGatewayConfig,
		Namespace: *conf.SNI.Ingress.Namespace,
	}}

	istioProxyGateway := []istio.ProxyProtocol{{
		Values: istio.ProxyValues{
			Labels: operation.GetIstioZoneLabels(conf.SNI.Ingress.Labels, nil),
		},
		Namespace: *conf.SNI.Ingress.Namespace,
	}}

	// Automatically create ingress gateways for single-zone control planes on multi-zonal seeds
	if len(gardenSeed.Spec.Provider.Zones) > 1 {
		for _, zone := range gardenSeed.Spec.Provider.Zones {
			namespace := operation.GetIstioNamespaceForZone(*conf.SNI.Ingress.Namespace, zone)

			istioIngressGateway = append(istioIngressGateway, istio.IngressGateway{
				Values: istio.IngressValues{
					TrustDomain:           gardencorev1beta1.DefaultDomain,
					Image:                 igwImage.String(),
					IstiodNamespace:       v1beta1constants.IstioSystemNamespace,
					Annotations:           seed.GetZonalLoadBalancerServiceAnnotations(zone),
					ExternalTrafficPolicy: seed.GetZonalLoadBalancerServiceExternalTrafficPolicy(zone),
					Ports:                 defaultIngressGatewayConfig.Ports,
					// LoadBalancerIP can currently not be provided for automatic ingress gateways
					Labels: operation.GetIstioZoneLabels(defaultIngressGatewayConfig.Labels, &zone),
					Zones:  []string{zone},
				},
				Namespace: namespace,
			})

			istioProxyGateway = append(istioProxyGateway, istio.ProxyProtocol{
				Values: istio.ProxyValues{
					Labels: operation.GetIstioZoneLabels(defaultIngressGatewayConfig.Labels, &zone),
				},
				Namespace: namespace,
			})
		}
	}

	// Add for each ExposureClass handler in the config an own Ingress Gateway and Proxy Gateway.
	for _, handler := range conf.ExposureClassHandlers {
		istioIngressGateway = append(istioIngressGateway, istio.IngressGateway{
			Values: istio.IngressValues{
				TrustDomain:           gardencorev1beta1.DefaultDomain,
				Image:                 igwImage.String(),
				IstiodNamespace:       v1beta1constants.IstioSystemNamespace,
				Annotations:           utils.MergeStringMaps(seed.GetLoadBalancerServiceAnnotations(), handler.LoadBalancerService.Annotations),
				ExternalTrafficPolicy: seed.GetLoadBalancerServiceExternalTrafficPolicy(),
				MinReplicas:           minReplicas,
				MaxReplicas:           maxReplicas,
				Ports:                 defaultIngressGatewayConfig.Ports,
				LoadBalancerIP:        handler.SNI.Ingress.ServiceExternalIP,
				Labels:                operation.GetIstioZoneLabels(gutil.GetMandatoryExposureClassHandlerSNILabels(handler.SNI.Ingress.Labels, handler.Name), nil),
			},
			Namespace: *handler.SNI.Ingress.Namespace,
		})

		istioProxyGateway = append(istioProxyGateway, istio.ProxyProtocol{
			Values: istio.ProxyValues{
				Labels: operation.GetIstioZoneLabels(gutil.GetMandatoryExposureClassHandlerSNILabels(handler.SNI.Ingress.Labels, handler.Name), nil),
			},
			Namespace: *handler.SNI.Ingress.Namespace,
		})

		// Automatically create ingress gateways for single-zone control planes on multi-zonal seeds
		if len(gardenSeed.Spec.Provider.Zones) > 1 {
			for _, zone := range gardenSeed.Spec.Provider.Zones {
				namespace := operation.GetIstioNamespaceForZone(*handler.SNI.Ingress.Namespace, zone)

				istioIngressGateway = append(istioIngressGateway, istio.IngressGateway{
					Values: istio.IngressValues{
						TrustDomain:           gardencorev1beta1.DefaultDomain,
						Image:                 igwImage.String(),
						IstiodNamespace:       v1beta1constants.IstioSystemNamespace,
						Annotations:           utils.MergeStringMaps(handler.LoadBalancerService.Annotations, seed.GetZonalLoadBalancerServiceAnnotations(zone)),
						ExternalTrafficPolicy: seed.GetZonalLoadBalancerServiceExternalTrafficPolicy(zone),
						Ports:                 defaultIngressGatewayConfig.Ports,
						// LoadBalancerIP can currently not be provided for automatic ingress gateways
						Labels: operation.GetIstioZoneLabels(gutil.GetMandatoryExposureClassHandlerSNILabels(handler.SNI.Ingress.Labels, handler.Name), &zone),
						Zones:  []string{zone},
					},
					Namespace: namespace,
				})

				istioProxyGateway = append(istioProxyGateway, istio.ProxyProtocol{
					Values: istio.ProxyValues{
						Labels: operation.GetIstioZoneLabels(gutil.GetMandatoryExposureClassHandlerSNILabels(handler.SNI.Ingress.Labels, handler.Name), &zone),
					},
					Namespace: namespace,
				})
			}
		}
	}

	if !gardenletfeatures.FeatureGate.Enabled(features.APIServerSNI) {
		istioProxyGateway = nil
	}

	_, seedServiceCIDR, err := net.ParseCIDR(gardenSeed.Spec.Networks.Services)
	if err != nil {
		return nil, err
	}

	seedDNSServerAddress, err := common.ComputeOffsetIP(seedServiceCIDR, 10)
	if err != nil {
		return nil, fmt.Errorf("cannot calculate CoreDNS ClusterIP: %w", err)
	}

	return istio.NewIstio(
		seedClient,
		chartRenderer,
		istio.IstiodValues{
			TrustDomain:          gardencorev1beta1.DefaultDomain,
			Image:                istiodImage.String(),
			DNSServerAddress:     pointer.String(seedDNSServerAddress.String()),
			NodeLocalIPVSAddress: pointer.String(nodelocaldns.IPVSAddress),
			Zones:                gardenSeed.Spec.Provider.Zones,
		},
		v1beta1constants.IstioSystemNamespace,
		istioIngressGateway,
		istioProxyGateway,
	), nil
}

func defaultNetworkPolicies(
	c client.Client,
	seed *gardencorev1beta1.Seed,
	sniEnabled bool,
	gardenNamespaceName string,
) (
	component.DeployWaiter,
	error,
) {
	networks := []string{seed.Spec.Networks.Pods, seed.Spec.Networks.Services}
	if v := seed.Spec.Networks.Nodes; v != nil {
		networks = append(networks, *v)
	}
	privateNetworkPeers, err := networkpolicies.ToNetworkPolicyPeersWithExceptions(networkpolicies.AllPrivateNetworkBlocks(), networks...)
	if err != nil {
		return nil, err
	}

	_, seedServiceCIDR, err := net.ParseCIDR(seed.Spec.Networks.Services)
	if err != nil {
		return nil, err
	}
	seedDNSServerAddress, err := common.ComputeOffsetIP(seedServiceCIDR, 10)
	if err != nil {
		return nil, fmt.Errorf("cannot calculate CoreDNS ClusterIP: %v", err)
	}

	return networkpolicies.NewBootstrapper(c, gardenNamespaceName, networkpolicies.GlobalValues{
		SNIEnabled:           sniEnabled,
		DenyAllTraffic:       false,
		PrivateNetworkPeers:  privateNetworkPeers,
		NodeLocalIPVSAddress: pointer.String(nodelocaldns.IPVSAddress),
		DNSServerAddress:     pointer.String(seedDNSServerAddress.String()),
	}), nil
}

func defaultDependencyWatchdogs(
	c client.Client,
	seedVersion *semver.Version,
	imageVector imagevector.ImageVector,
	seedSettings *gardencorev1beta1.SeedSettings,
	gardenNamespaceName string,
) (
	dwdEndpoint component.DeployWaiter,
	dwdProbe component.DeployWaiter,
	err error,
) {
	image, err := imageVector.FindImage(images.ImageNameDependencyWatchdog, imagevector.RuntimeVersion(seedVersion.String()), imagevector.TargetVersion(seedVersion.String()))
	if err != nil {
		return nil, nil, err
	}

	var (
		dwdEndpointValues = dependencywatchdog.BootstrapperValues{Role: dependencywatchdog.RoleEndpoint, Image: image.String(), KubernetesVersion: seedVersion}
		dwdProbeValues    = dependencywatchdog.BootstrapperValues{Role: dependencywatchdog.RoleProbe, Image: image.String(), KubernetesVersion: seedVersion}
	)

	dwdEndpoint = component.OpDestroyWithoutWait(dependencywatchdog.NewBootstrapper(c, gardenNamespaceName, dwdEndpointValues))
	dwdProbe = component.OpDestroyWithoutWait(dependencywatchdog.NewBootstrapper(c, gardenNamespaceName, dwdProbeValues))

	if gardencorev1beta1helper.SeedSettingDependencyWatchdogEndpointEnabled(seedSettings) {
		// Fetch component-specific dependency-watchdog configuration
		var (
			dependencyWatchdogEndpointConfigurationFuncs = []dependencywatchdog.EndpointConfigurationFunc{
				func() (map[string]restarterapi.Service, error) {
					return etcd.DependencyWatchdogEndpointConfiguration(v1beta1constants.ETCDRoleMain)
				},
				kubeapiserver.DependencyWatchdogEndpointConfiguration,
			}
			dependencyWatchdogEndpointConfigurations = restarterapi.ServiceDependants{
				Services: make(map[string]restarterapi.Service, len(dependencyWatchdogEndpointConfigurationFuncs)),
			}
		)

		for _, componentFn := range dependencyWatchdogEndpointConfigurationFuncs {
			dwdConfig, err := componentFn()
			if err != nil {
				return nil, nil, err
			}
			for k, v := range dwdConfig {
				dependencyWatchdogEndpointConfigurations.Services[k] = v
			}
		}

		dwdEndpointValues.ValuesEndpoint = dependencywatchdog.ValuesEndpoint{ServiceDependants: dependencyWatchdogEndpointConfigurations}
		dwdEndpoint = dependencywatchdog.NewBootstrapper(c, gardenNamespaceName, dwdEndpointValues)
	}

	if gardencorev1beta1helper.SeedSettingDependencyWatchdogProbeEnabled(seedSettings) {
		// Fetch component-specific dependency-watchdog configuration
		var (
			dependencyWatchdogProbeConfigurationFuncs = []dependencywatchdog.ProbeConfigurationFunc{
				kubeapiserver.DependencyWatchdogProbeConfiguration,
			}
			dependencyWatchdogProbeConfigurations = scalerapi.ProbeDependantsList{
				Probes: make([]scalerapi.ProbeDependants, 0, len(dependencyWatchdogProbeConfigurationFuncs)),
			}
		)

		for _, componentFn := range dependencyWatchdogProbeConfigurationFuncs {
			dwdConfig, err := componentFn()
			if err != nil {
				return nil, nil, err
			}
			dependencyWatchdogProbeConfigurations.Probes = append(dependencyWatchdogProbeConfigurations.Probes, dwdConfig...)
		}

		dwdProbeValues.ValuesProbe = dependencywatchdog.ValuesProbe{ProbeDependantsList: dependencyWatchdogProbeConfigurations}
		dwdProbe = dependencywatchdog.NewBootstrapper(c, gardenNamespaceName, dwdProbeValues)
	}

	return
}

func defaultVPNAuthzServer(
	ctx context.Context,
	c client.Client,
	seedVersion *semver.Version,
	imageVector imagevector.ImageVector,
	gardenNamespaceName string,
) (
	extAuthzServer component.DeployWaiter,
	err error,
) {
	image, err := imageVector.FindImage(images.ImageNameExtAuthzServer, imagevector.RuntimeVersion(seedVersion.String()), imagevector.TargetVersion(seedVersion.String()))
	if err != nil {
		return nil, err
	}

	vpnAuthzServer := vpnauthzserver.New(
		c,
		gardenNamespaceName,
		image.String(),
		seedVersion,
	)

	if gardenletfeatures.FeatureGate.Enabled(features.ManagedIstio) {
		return vpnAuthzServer, nil
	}

	hasVPNSeedDeployments, err := kutil.ResourcesExist(ctx, c, appsv1.SchemeGroupVersion.WithKind("DeploymentList"), client.MatchingLabels(map[string]string{v1beta1constants.LabelApp: v1beta1constants.DeploymentNameVPNSeedServer}))
	if err != nil {
		return nil, err
	}
	if hasVPNSeedDeployments {
		// Even though the ManagedIstio feature gate is turned off, there are still shoots which have not been reconciled yet.
		// Thus, we cannot destroy the ext-authz-server.
		return component.NoOp(), nil
	}

	return component.OpDestroyWithoutWait(vpnAuthzServer), nil
}

func defaultSystem(
	c client.Client,
	seed *seedpkg.Seed,
	imageVector imagevector.ImageVector,
	reserveExcessCapacity bool,
	gardenNamespaceName string,
) (
	component.DeployWaiter,
	error,
) {
	image, err := imageVector.FindImage(images.ImageNamePauseContainer)
	if err != nil {
		return nil, err
	}

	var replicasExcessCapacityReservation int32 = 2
	if numberOfZones := len(seed.GetInfo().Spec.Provider.Zones); numberOfZones > 1 {
		replicasExcessCapacityReservation = int32(numberOfZones)
	}

	return seedsystem.New(
		c,
		gardenNamespaceName,
		seedsystem.Values{
			ReserveExcessCapacity: seedsystem.ReserveExcessCapacityValues{
				Enabled:  reserveExcessCapacity,
				Image:    image.String(),
				Replicas: replicasExcessCapacityReservation,
			},
		},
	), nil
}
