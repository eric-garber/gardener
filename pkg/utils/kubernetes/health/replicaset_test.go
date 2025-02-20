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

package health_test

import (
	"k8s.io/utils/pointer"

	"github.com/gardener/gardener/pkg/utils/kubernetes/health"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ReplicaSet", func() {
	DescribeTable("CheckReplicaSet",
		func(rs *appsv1.ReplicaSet, matcher types.GomegaMatcher) {
			err := health.CheckReplicaSet(rs)
			Expect(err).To(matcher)
		},
		Entry("not observed at latest version", &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
		}, HaveOccurred()),
		Entry("not enough ready replicas", &appsv1.ReplicaSet{
			Spec:   appsv1.ReplicaSetSpec{Replicas: pointer.Int32(2)},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1},
		}, HaveOccurred()),
		Entry("healthy", &appsv1.ReplicaSet{
			Spec:   appsv1.ReplicaSetSpec{Replicas: pointer.Int32(2)},
			Status: appsv1.ReplicaSetStatus{ReadyReplicas: 2},
		}, BeNil()),
	)
})
