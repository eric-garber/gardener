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

package conditions_test

import (
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Shoot Conditions controller tests", func() {
	var (
		shoot       *gardencorev1beta1.Shoot
		managedSeed *seedmanagementv1alpha1.ManagedSeed
		seed        *gardencorev1beta1.Seed
	)

	BeforeEach(func() {
		shoot = &gardencorev1beta1.Shoot{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-",
				Namespace:    testNamespace.Name,
				Labels:       map[string]string{testID: testRunID},
			},
			Spec: gardencorev1beta1.ShootSpec{
				SecretBindingName: "my-provider-account",
				CloudProfileName:  "cloudprofile1",
				Region:            "europe-central-1",
				Provider: gardencorev1beta1.Provider{
					Type: "foo-provider",
					Workers: []gardencorev1beta1.Worker{
						{
							Name:    "cpu-worker",
							Minimum: 3,
							Maximum: 3,
							Machine: gardencorev1beta1.Machine{
								Type: "large",
							},
						},
					},
				},
				DNS: &gardencorev1beta1.DNS{
					Domain: pointer.String("some-domain.example.com"),
				},
				Kubernetes: gardencorev1beta1.Kubernetes{
					Version: "1.20.1",
				},
				Networking: gardencorev1beta1.Networking{
					Type: "foo-networking",
				},
			},
		}

		By("Create Shoot")
		Expect(testClient.Create(ctx, shoot)).To(Succeed())
		log.Info("Created shoot for test", "shoot", client.ObjectKeyFromObject(shoot))

		DeferCleanup(func() {
			By("Delete Shoot")
			Expect(client.IgnoreNotFound(testClient.Delete(ctx, shoot))).To(Succeed())
		})

		managedSeed = &seedmanagementv1alpha1.ManagedSeed{
			ObjectMeta: metav1.ObjectMeta{
				Name:      shoot.Name,
				Namespace: testNamespace.Name,
				Labels:    map[string]string{testID: testRunID},
			},
			Spec: seedmanagementv1alpha1.ManagedSeedSpec{
				Shoot: &seedmanagementv1alpha1.Shoot{
					Name: shoot.Name,
				},
				Gardenlet: &seedmanagementv1alpha1.Gardenlet{},
			},
		}

		seed = &gardencorev1beta1.Seed{
			ObjectMeta: metav1.ObjectMeta{
				Name:   managedSeed.Name,
				Labels: map[string]string{testID: testRunID},
			},
			Spec: gardencorev1beta1.SeedSpec{
				Provider: gardencorev1beta1.SeedProvider{
					Region: "region",
					Type:   "providerType",
				},
				Networks: gardencorev1beta1.SeedNetworks{
					Pods:     "10.0.0.0/16",
					Services: "10.1.0.0/16",
					Nodes:    pointer.String("10.2.0.0/16"),
					ShootDefaults: &gardencorev1beta1.ShootNetworks{
						Pods:     pointer.String("100.128.0.0/11"),
						Services: pointer.String("100.72.0.0/13"),
					},
				},
				DNS: gardencorev1beta1.SeedDNS{
					IngressDomain: pointer.String("someingress.example.com"),
				},
			},
		}
	})

	Context("preconditions not fulfilled", func() {
		It("no ManagedSeed", func() {
			Consistently(func(g Gomega) []gardencorev1beta1.Condition {
				g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(shoot), shoot)).To(Succeed())
				return shoot.Status.Conditions
			}).Should(And(
				Not(ContainCondition(OfType(gardencorev1beta1.SeedBackupBucketsReady))),
				Not(ContainCondition(OfType(gardencorev1beta1.SeedBootstrapped))),
				Not(ContainCondition(OfType(gardencorev1beta1.SeedExtensionsReady))),
				Not(ContainCondition(OfType(gardencorev1beta1.SeedGardenletReady))),
				Not(ContainCondition(OfType(gardencorev1beta1.SeedSystemComponentsHealthy))),
			))
		})

		It("no Seed", func() {
			Expect(testClient.Create(ctx, managedSeed)).To(Succeed())
			log.Info("Created ManagedSeed for test", "managedSeed", client.ObjectKeyFromObject(managedSeed))

			DeferCleanup(func() {
				By("Delete ManagedSeed")
				Expect(client.IgnoreNotFound(testClient.Delete(ctx, managedSeed))).To(Succeed())
			})

			Consistently(func(g Gomega) []gardencorev1beta1.Condition {
				g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(shoot), shoot)).To(Succeed())
				return shoot.Status.Conditions
			}).Should(And(
				Not(ContainCondition(OfType(gardencorev1beta1.SeedBackupBucketsReady))),
				Not(ContainCondition(OfType(gardencorev1beta1.SeedBootstrapped))),
				Not(ContainCondition(OfType(gardencorev1beta1.SeedExtensionsReady))),
				Not(ContainCondition(OfType(gardencorev1beta1.SeedGardenletReady))),
				Not(ContainCondition(OfType(gardencorev1beta1.SeedSystemComponentsHealthy))),
			))
		})
	})

	Context("preconditions fulfilled", func() {
		BeforeEach(func() {
			Expect(testClient.Create(ctx, managedSeed)).To(Succeed())
			log.Info("Created ManagedSeed for test", "managedSeed", client.ObjectKeyFromObject(managedSeed))

			By("Wait until manager cache has observed ManagedSeed")
			Eventually(func() error {
				return mgrClient.Get(ctx, client.ObjectKeyFromObject(managedSeed), managedSeed)
			}).Should(Succeed())

			Expect(testClient.Create(ctx, seed)).To(Succeed())
			log.Info("Created Seed for test", "seed", client.ObjectKeyFromObject(seed))

			DeferCleanup(func() {
				By("Delete Seed")
				Expect(client.IgnoreNotFound(testClient.Delete(ctx, seed))).To(Succeed())

				By("Delete ManagedSeed")
				Expect(client.IgnoreNotFound(testClient.Delete(ctx, managedSeed))).To(Succeed())
			})
		})

		It("should copy the seed conditions to the shoot", func() {
			conditions := []gardencorev1beta1.Condition{
				{Type: gardencorev1beta1.SeedBackupBucketsReady, Status: gardencorev1beta1.ConditionProgressing},
				{Type: gardencorev1beta1.SeedBootstrapped, Status: gardencorev1beta1.ConditionProgressing},
				{Type: gardencorev1beta1.SeedExtensionsReady, Status: gardencorev1beta1.ConditionProgressing},
				{Type: gardencorev1beta1.SeedGardenletReady, Status: gardencorev1beta1.ConditionProgressing},
				{Type: gardencorev1beta1.SeedSystemComponentsHealthy, Status: gardencorev1beta1.ConditionProgressing},
			}

			patch := client.StrategicMergeFrom(seed.DeepCopy())
			seed.Status.Conditions = helper.MergeConditions(seed.Status.Conditions, conditions...)
			Expect(testClient.Status().Patch(ctx, seed, patch)).To(Succeed())

			By("Wait until manager cache has observed seed with updated conditions")
			Eventually(func(g Gomega) []gardencorev1beta1.Condition {
				updatedSeed := &gardencorev1beta1.Seed{}
				g.Expect(mgrClient.Get(ctx, client.ObjectKeyFromObject(seed), updatedSeed)).To(Succeed())
				return updatedSeed.Status.Conditions
			}).Should(And(
				ContainCondition(OfType(gardencorev1beta1.SeedBackupBucketsReady)),
				ContainCondition(OfType(gardencorev1beta1.SeedBootstrapped)),
				ContainCondition(OfType(gardencorev1beta1.SeedExtensionsReady)),
				ContainCondition(OfType(gardencorev1beta1.SeedGardenletReady)),
				ContainCondition(OfType(gardencorev1beta1.SeedSystemComponentsHealthy)),
			))

			By("Check shoot conditions")
			Eventually(func(g Gomega) []gardencorev1beta1.Condition {
				g.Expect(testClient.Get(ctx, client.ObjectKeyFromObject(shoot), shoot)).To(Succeed())
				return shoot.Status.Conditions
			}).Should(And(
				ContainCondition(OfType(gardencorev1beta1.SeedBackupBucketsReady)),
				ContainCondition(OfType(gardencorev1beta1.SeedBootstrapped)),
				ContainCondition(OfType(gardencorev1beta1.SeedExtensionsReady)),
				ContainCondition(OfType(gardencorev1beta1.SeedGardenletReady)),
				ContainCondition(OfType(gardencorev1beta1.SeedSystemComponentsHealthy)),
			))
		})
	})
})
