// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package app

import (
	"fmt"
	"os"

	extcontroller "github.com/gardener/gardener/extensions/pkg/controller"
	controllercmd "github.com/gardener/gardener/extensions/pkg/controller/cmd"
	"github.com/gardener/gardener/extensions/pkg/controller/heartbeat"
	heartbeatcmd "github.com/gardener/gardener/extensions/pkg/controller/heartbeat/cmd"
	"github.com/gardener/gardener/extensions/pkg/controller/operatingsystemconfig/oscommon"
	oscommoncmd "github.com/gardener/gardener/extensions/pkg/controller/operatingsystemconfig/oscommon/cmd"
	"github.com/gardener/gardener/extensions/pkg/controller/operatingsystemconfig/oscommon/generator"
	"github.com/gardener/gardener/extensions/pkg/util"

	"github.com/spf13/cobra"
	componentbaseconfig "k8s.io/component-base/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// NewControllerCommand creates a new command for running an OS controller.
func NewControllerCommand(ctrlName string, osTypes []string, generator generator.Generator) *cobra.Command {
	var (
		restOpts = &controllercmd.RESTOptions{}
		mgrOpts  = &controllercmd.ManagerOptions{
			LeaderElection:          true,
			LeaderElectionID:        controllercmd.LeaderElectionNameID(ctrlName),
			LeaderElectionNamespace: os.Getenv("LEADER_ELECTION_NAMESPACE"),
		}
		ctrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}

		heartbeatCtrlOpts = &heartbeatcmd.Options{
			ExtensionName:        ctrlName,
			RenewIntervalSeconds: 30,
			Namespace:            os.Getenv("LEADER_ELECTION_NAMESPACE"),
		}

		reconcileOpts = &controllercmd.ReconcilerOptions{}

		controllerSwitches = oscommoncmd.SwitchOptions(ctrlName, osTypes, generator)

		aggOption = controllercmd.NewOptionAggregator(
			restOpts,
			mgrOpts,
			ctrlOpts,
			controllercmd.PrefixOption("heartbeat-", heartbeatCtrlOpts),
			reconcileOpts,
			controllerSwitches,
		)
	)

	cmd := &cobra.Command{
		Use: "os-" + ctrlName + "-controller-manager",

		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if err := aggOption.Complete(); err != nil {
				return fmt.Errorf("error completing options: %w", err)
			}

			if err := heartbeatCtrlOpts.Validate(); err != nil {
				return err
			}

			// TODO: Make these flags configurable via command line parameters or component config file.
			util.ApplyClientConnectionConfigurationToRESTConfig(&componentbaseconfig.ClientConnectionConfiguration{
				QPS:   100.0,
				Burst: 130,
			}, restOpts.Completed().Config)

			mgr, err := manager.New(restOpts.Completed().Config, mgrOpts.Completed().Options())
			if err != nil {
				return fmt.Errorf("could not instantiate manager: %w", err)
			}

			if err := extcontroller.AddToScheme(mgr.GetScheme()); err != nil {
				return fmt.Errorf("could not update manager scheme: %w", err)
			}

			ctrlOpts.Completed().Apply(&oscommon.DefaultAddOptions.Controller)
			heartbeatCtrlOpts.Completed().Apply(&heartbeat.DefaultAddOptions)

			reconcileOpts.Completed().Apply(&oscommon.DefaultAddOptions.IgnoreOperationAnnotation)

			if err := controllerSwitches.Completed().AddToManager(mgr); err != nil {
				return fmt.Errorf("could not add controller to manager: %w", err)
			}

			if err := mgr.Start(ctx); err != nil {
				return fmt.Errorf("error running manager: %w", err)
			}

			return nil
		},
	}

	aggOption.AddFlags(cmd.Flags())

	return cmd
}
