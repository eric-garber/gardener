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

package shootvalidator_test

import (
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"

	"github.com/gardener/gardener/pkg/client/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ShootValidator tests", func() {
	var (
		shoot *gardencorev1beta1.Shoot

		userTestClient client.Client
		userName       string

		err error
	)

	BeforeEach(func() {
		shoot = &gardencorev1beta1.Shoot{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-",
				Namespace:    testNamespace.Name,
			},
			Spec: gardencorev1beta1.ShootSpec{
				CloudProfileName:  cloudProfile.Name,
				SecretBindingName: testSecretBinding.Name,
				Region:            "region",
				Provider: gardencorev1beta1.Provider{
					Type: "providerType",
					Workers: []gardencorev1beta1.Worker{
						{
							Name:    "cpu-worker",
							Minimum: 2,
							Maximum: 2,
							Machine: gardencorev1beta1.Machine{Type: "large"},
						},
					},
				},
				Kubernetes: gardencorev1beta1.Kubernetes{Version: "1.21.1"},
				Networking: gardencorev1beta1.Networking{Type: "foo-networking"},
			},
		}
	})

	Context("User without RBAC for shoots/binding", func() {
		BeforeEach(func() {
			userName = "member"

			// envtest.Environment.AddUser doesn't work when running against an existing cluster
			// use impersonation instead to simulate different user
			userConfig := rest.CopyConfig(restConfig)
			userConfig.Impersonate = rest.ImpersonationConfig{
				UserName: userName,
				Groups:   []string{"project:member"},
			}

			userTestClient, err = client.New(userConfig, client.Options{Scheme: kubernetes.GardenScheme})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should be able to create a shoot without .spec.seedName successfully", func() {
			By("creating Shoot")
			Eventually(func() error {
				return userTestClient.Create(ctx, shoot)
			}).Should(Succeed())
			log.Info("Created Shoot for test", "shoot", client.ObjectKeyFromObject(shoot))

			DeferCleanup(func() {
				By("Delete Shoot")
				Expect(userTestClient.Delete(ctx, shoot)).To(Or(Succeed(), BeNotFoundError()))
				Eventually(func() error {
					return testClient.Get(ctx, client.ObjectKeyFromObject(shoot), shoot)
				}).Should(BeNotFoundError())
			})
		})

		It("should not be able to create a shoot with .spec.seedName", func() {
			By("creating Shoot")
			shoot.Spec.SeedName = &seed.Name

			Consistently(func() error {
				return userTestClient.Create(ctx, shoot)
			}).Should(And(
				BeForbiddenError(),
				MatchError(ContainSubstring("user %q is not allowed to set .spec.seedName", userName)),
			))
		})
	})

	Context("User with RBAC for shoots/binding", func() {
		BeforeEach(func() {
			userName = "admin"

			// envtest.Environment.AddUser doesn't work when running against an existing cluster
			// use impersonation instead to simulate different user
			userConfig := rest.CopyConfig(restConfig)
			userConfig.Impersonate = rest.ImpersonationConfig{
				UserName: userName,
				Groups:   []string{"project:admin"},
			}

			userTestClient, err = client.New(userConfig, client.Options{Scheme: kubernetes.GardenScheme})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should be able to create a shoot with .spec.seedName successfully", func() {
			By("creating Shoot")
			shoot.Spec.SeedName = &seed.Name

			Eventually(func() error {
				return userTestClient.Create(ctx, shoot)
			}).Should(Succeed())
			log.Info("Created Shoot for test", "shoot", client.ObjectKeyFromObject(shoot))

			DeferCleanup(func() {
				By("Delete Shoot")
				Expect(userTestClient.Delete(ctx, shoot)).To(Or(Succeed(), BeNotFoundError()))
				Eventually(func() error {
					return testClient.Get(ctx, client.ObjectKeyFromObject(shoot), shoot)
				}).Should(BeNotFoundError())
			})
		})
	})
})
