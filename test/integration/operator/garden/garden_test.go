// Copyright (c) 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package garden_test

import (
	"time"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	operatorv1alpha1 "github.com/gardener/gardener/pkg/apis/operator/v1alpha1"
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/controllerutils"
	"github.com/gardener/gardener/pkg/features"
	"github.com/gardener/gardener/pkg/operation/botanist/component/etcd"
	operatorfeatures "github.com/gardener/gardener/pkg/operator/features"
	secretutils "github.com/gardener/gardener/pkg/utils/secrets"
	"github.com/gardener/gardener/pkg/utils/test"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"
)

var _ = Describe("Garden controller tests", func() {
	var (
		loadBalancerServiceAnnotations = map[string]string{"foo": "bar"}
		garden                         *operatorv1alpha1.Garden
	)

	BeforeEach(func() {
		DeferCleanup(test.WithVar(&secretutils.GenerateKey, secretutils.FakeGenerateKey))
		DeferCleanup(test.WithFeatureGate(operatorfeatures.FeatureGate, features.HVPA, true))
		DeferCleanup(test.WithVars(
			&etcd.DefaultInterval, 100*time.Millisecond,
			&etcd.DefaultTimeout, 500*time.Millisecond,
		))

		garden = &operatorv1alpha1.Garden{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "garden-" + testRunID,
				Labels: map[string]string{testID: testRunID},
			},
			Spec: operatorv1alpha1.GardenSpec{
				RuntimeCluster: operatorv1alpha1.RuntimeCluster{
					Provider: operatorv1alpha1.Provider{
						Zones: []string{"a", "b", "c"},
					},
					Settings: &operatorv1alpha1.Settings{
						LoadBalancerServices: &operatorv1alpha1.SettingLoadBalancerServices{
							Annotations: loadBalancerServiceAnnotations,
						},
						VerticalPodAutoscaler: &operatorv1alpha1.SettingVerticalPodAutoscaler{
							Enabled: pointer.Bool(true),
						},
					},
				},
				VirtualCluster: operatorv1alpha1.VirtualCluster{
					Maintenance: operatorv1alpha1.Maintenance{
						TimeWindow: gardencorev1beta1.MaintenanceTimeWindow{
							Begin: "220000+0100",
							End:   "230000+0100",
						},
					},
				},
			},
		}

		By("Create Garden")
		Expect(testClient.Create(ctx, garden)).To(Succeed())
		log.Info("Created Garden for test", "garden", garden.Name)

		DeferCleanup(func() {
			By("Delete Garden")
			Expect(client.IgnoreNotFound(testClient.Delete(ctx, garden))).To(Succeed())

			By("Forcefully remove finalizers")
			Expect(client.IgnoreNotFound(controllerutils.RemoveAllFinalizers(ctx, testClient, garden))).To(Succeed())

			By("Ensure Garden is gone")
			Eventually(func() error {
				return mgrClient.Get(ctx, client.ObjectKeyFromObject(garden), garden)
			}).Should(BeNotFoundError())
		})
	})

	It("should properly maintain the Reconciled condition", func() {
		By("Wait for Garden to have finalizer")
		Eventually(func(g Gomega) []string {
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(garden), garden)).To(Succeed())
			return garden.Finalizers
		}).Should(ConsistOf("gardener.cloud/operator"))

		By("Wait for Reconciled condition to be set to Progressing")
		Eventually(func(g Gomega) []gardencorev1beta1.Condition {
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(garden), garden)).To(Succeed())
			return garden.Status.Conditions
		}).Should(ContainCondition(
			OfType(operatorv1alpha1.GardenReconciled),
			WithStatus(gardencorev1beta1.ConditionProgressing),
		))
		Expect(garden.Status.Gardener).NotTo(BeNil())

		By("Verify that the custom resource definitions have been created")
		// When the controller succeeds then it deletes the `ManagedResource` CRD, so we only need to ensure here that
		// the `ManagedResource` API is no longer available.
		Eventually(func(g Gomega) []apiextensionsv1.CustomResourceDefinition {
			crdList := &apiextensionsv1.CustomResourceDefinitionList{}
			g.Expect(testClient.List(ctx, crdList)).To(Succeed())
			return crdList.Items
		}).Should(ContainElements(
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("hvpas.autoscaling.k8s.io")})}),
		))

		By("Verify that garden runtime CA secret was generated")
		Eventually(func(g Gomega) []corev1.Secret {
			secretList := &corev1.SecretList{}
			g.Expect(testClient.List(ctx, secretList, client.InNamespace(testNamespace.Name), client.MatchingLabels{"name": "ca-garden-runtime", "managed-by": "secrets-manager", "manager-identity": "gardener-operator"})).To(Succeed())
			return secretList.Items
		}).Should(HaveLen(1))

		By("Verify that garden namespace was labeled and annotated appropriately")
		Eventually(func(g Gomega) {
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(testNamespace), testNamespace)).To(Succeed())
			g.Expect(testNamespace.Labels).To(HaveKeyWithValue("high-availability-config.resources.gardener.cloud/consider", "true"))
			g.Expect(testNamespace.Annotations).To(HaveKeyWithValue("high-availability-config.resources.gardener.cloud/zones", "a,b,c"))
		}).Should(Succeed())

		// The garden controller waits for the gardener-resource-manager Deployment to be healthy, so let's fake this here.
		By("Patch gardener-resource-manager deployment to report healthiness")
		Eventually(func(g Gomega) {
			deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "gardener-resource-manager", Namespace: testNamespace.Name}}
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())

			patch := client.MergeFrom(deployment.DeepCopy())
			deployment.Status.ObservedGeneration = deployment.Generation
			deployment.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue}}
			g.Expect(testClient.Status().Patch(ctx, deployment, patch)).To(Succeed())
		}).Should(Succeed())

		// The gardener-resource-manager is not really running in this test scenario, hence there is nothing to serve
		// the webhook endpoints. However, the envtest kube-apiserver would try to reach them, so let's better delete
		// them here for the sake of this test.
		By("Delete gardener-resource-manager webhooks")
		mutatingWebhookConfiguration := &admissionregistrationv1.MutatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "gardener-resource-manager"}}
		validatingWebhookConfiguration := &admissionregistrationv1.ValidatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "gardener-resource-manager"}}
		Eventually(func(g Gomega) {
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(mutatingWebhookConfiguration), mutatingWebhookConfiguration)).To(Succeed())
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(validatingWebhookConfiguration), validatingWebhookConfiguration)).To(Succeed())

			g.Expect(testClient.Delete(ctx, mutatingWebhookConfiguration)).To(Succeed())
			g.Expect(testClient.Delete(ctx, validatingWebhookConfiguration)).To(Succeed())
		}).Should(Succeed())

		By("Verify that the garden system components have been deployed")
		Eventually(func(g Gomega) []resourcesv1alpha1.ManagedResource {
			managedResourceList := &resourcesv1alpha1.ManagedResourceList{}
			g.Expect(testClient.List(ctx, managedResourceList, client.InNamespace(testNamespace.Name))).To(Succeed())
			return managedResourceList.Items
		}).Should(ConsistOf(
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("garden-system")})}),
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("vpa")})}),
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("hvpa")})}),
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("etcd-druid")})}),
		))

		By("Verify that the virtual garden control plane components have been deployed")
		Eventually(func(g Gomega) []druidv1alpha1.Etcd {
			etcdList := &druidv1alpha1.EtcdList{}
			g.Expect(testClient.List(ctx, etcdList, client.InNamespace(testNamespace.Name))).To(Succeed())
			return etcdList.Items
		}).Should(ConsistOf(
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("virtual-garden-etcd-main")})}),
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("virtual-garden-etcd-events")})}),
		))

		Eventually(func(g Gomega) map[string]string {
			service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "virtual-garden-kube-apiserver", Namespace: testNamespace.Name}}
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(service), service)).To(Succeed())
			return service.Annotations
		}).Should(Equal(loadBalancerServiceAnnotations))

		// The garden controller waits for the Etcd resources to be healthy, but etcd-druid is not really running in
		// this test, so let's fake this here.
		By("Patch Etcd resources to report healthiness")
		Eventually(func(g Gomega) {
			for _, suffix := range []string{"main", "events"} {
				etcd := &druidv1alpha1.Etcd{ObjectMeta: metav1.ObjectMeta{Name: "virtual-garden-etcd-" + suffix, Namespace: testNamespace.Name}}
				g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(etcd), etcd)).To(Succeed(), "for "+etcd.Name)

				patch := client.MergeFrom(etcd.DeepCopy())
				delete(etcd.Annotations, "gardener.cloud/operation")
				g.Expect(testClient.Patch(ctx, etcd, patch)).To(Succeed(), "for "+etcd.Name)

				patch = client.MergeFrom(etcd.DeepCopy())
				etcd.Status.ObservedGeneration = &etcd.Generation
				etcd.Status.Ready = pointer.Bool(true)
				g.Expect(testClient.Status().Patch(ctx, etcd, patch)).To(Succeed(), "for "+etcd.Name)
			}
		}).Should(Succeed())

		// The garden controller waits for the virtual-garden-kube-apiserver Service resource to be ready, but there is
		// no service controller running in this test which would make it ready, so let's fake this here.
		By("Patch virtual-garden-kube-apiserver Service resource to report readiness")
		Eventually(func(g Gomega) {
			service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "virtual-garden-kube-apiserver", Namespace: testNamespace.Name}}
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(service), service)).To(Succeed())

			patch := client.MergeFrom(service.DeepCopy())
			service.Status.LoadBalancer.Ingress = append(service.Status.LoadBalancer.Ingress, corev1.LoadBalancerIngress{Hostname: "localhost"})
			g.Expect(testClient.Status().Patch(ctx, service, patch)).To(Succeed())
		}).Should(Succeed())

		By("Wait for Reconciled condition to be set to True")
		Eventually(func(g Gomega) []gardencorev1beta1.Condition {
			g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(garden), garden)).To(Succeed())
			return garden.Status.Conditions
		}).Should(ContainCondition(OfType(operatorv1alpha1.GardenReconciled), WithStatus(gardencorev1beta1.ConditionTrue)))

		By("Delete Garden")
		Expect(testClient.Delete(ctx, garden)).To(Succeed())

		By("Verify that the virtual garden control plane components have been deleted")
		Eventually(func(g Gomega) []druidv1alpha1.Etcd {
			etcdList := &druidv1alpha1.EtcdList{}
			g.Expect(testClient.List(ctx, etcdList)).To(Succeed())
			return etcdList.Items
		}).ShouldNot(ContainElements(
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("virtual-garden-etcd-main")})}),
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("virtual-garden-etcd-events")})}),
		))

		By("Verify that the garden system components have been deleted")
		// When the controller succeeds then it deletes the `ManagedResource` CRD, so we only need to ensure here that
		// the `ManagedResource` API is no longer available.
		Eventually(func(g Gomega) error {
			return testClient.List(ctx, &resourcesv1alpha1.ManagedResourceList{}, client.InNamespace(testNamespace.Name))
		}).Should(BeNotFoundError())

		By("Verify that the custom resource definitions have been deleted")
		// When the controller succeeds then it deletes the `ManagedResource` CRD, so we only need to ensure here that
		// the `ManagedResource` API is no longer available.
		Eventually(func(g Gomega) []apiextensionsv1.CustomResourceDefinition {
			crdList := &apiextensionsv1.CustomResourceDefinitionList{}
			g.Expect(testClient.List(ctx, crdList)).To(Succeed())
			return crdList.Items
		}).ShouldNot(ContainElements(
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("hvpas.autoscaling.k8s.io")})}),
			MatchFields(IgnoreExtras, Fields{"ObjectMeta": MatchFields(IgnoreExtras, Fields{"Name": Equal("etcds.druid.gardener.cloud")})}),
		))

		By("Verify that gardener-resource-manager has been deleted")
		Eventually(func(g Gomega) error {
			deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "gardener-resource-manager", Namespace: testNamespace.Name}}
			return testClient.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)
		}).Should(BeNotFoundError())

		By("Verify that secrets have been deleted")
		Eventually(func(g Gomega) []corev1.Secret {
			secretList := &corev1.SecretList{}
			g.Expect(testClient.List(ctx, secretList, client.InNamespace(testNamespace.Name), client.MatchingLabels{"managed-by": "secrets-manager", "manager-identity": "gardener-operator"})).To(Succeed())
			return secretList.Items
		}).Should(BeEmpty())

		By("Ensure Garden is gone")
		Eventually(func() error {
			return testClient.Get(ctx, client.ObjectKeyFromObject(garden), garden)
		}).Should(BeNotFoundError())
	})
})
