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

package shared

import (
	"github.com/Masterminds/semver"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener/pkg/gardenlet/apis/config"
	"github.com/gardener/gardener/pkg/operation/botanist/component"
	"github.com/gardener/gardener/pkg/operation/botanist/component/etcd"
	"github.com/gardener/gardener/pkg/utils/images"
	"github.com/gardener/gardener/pkg/utils/imagevector"
)

// NewEtcdDruid instantiates a new `etcd-druid` component.
func NewEtcdDruid(
	c client.Client,
	gardenNamespaceName string,
	runtimeVersion *semver.Version,
	imageVector imagevector.ImageVector,
	imageVectorOverwrites map[string]string,
	etcdConfig *config.ETCDConfig,
	priorityClassName string,
) (
	component.DeployWaiter,
	error,
) {
	image, err := imageVector.FindImage(images.ImageNameEtcdDruid, imagevector.RuntimeVersion(runtimeVersion.String()), imagevector.TargetVersion(runtimeVersion.String()))
	if err != nil {
		return nil, err
	}

	var imageVectorOverwrite *string
	if val, ok := imageVectorOverwrites[etcd.Druid]; ok {
		imageVectorOverwrite = &val
	}

	return etcd.NewBootstrapper(c, gardenNamespaceName, runtimeVersion, etcdConfig, image.String(), imageVectorOverwrite, priorityClassName), nil
}
