/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"os"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"go.uber.org/automaxprocs/maxprocs"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
	"github.com/tamcore/imagepullsecret-patcher/internal/controller"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var noAutoMaxProcs bool
	var noAutoMemlimit bool
	var autoMemlimitRatio float64

	// -serviceaccounts
	var serviceAccounts string
	// -dockerconfigjson
	var dockerConfigJSON string
	// -dockerconfigjsonpath
	var dockerConfigJSONPath string
	// -secretname
	var secretName string
	// -secretnamespace
	var secretNamespace string
	// -excluded-namespaces
	var excludedNamespaces string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080",
		"The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&noAutoMaxProcs, "no-auto-maxprocs", false,
		"Do not automatically set GOMAXPROCS to match container or system cpu quota.")
	flag.BoolVar(&noAutoMemlimit, "no-auto-memlimit", false,
		"Do not automatically set GOMEMLIMIT to match container or system memory limit.")
	flag.Float64Var(&autoMemlimitRatio, "auto-memlimit-ratio", float64(0.9),
		"The ratio of reserved GOMEMLIMIT memory to the detected maximum container or system memory.")
	flag.StringVar(&serviceAccounts, "serviceaccounts", "",
		"comma-separated list of serviceaccounts to patch")
	flag.StringVar(&dockerConfigJSON, "dockerconfigjson", "",
		"json credential for authenticating container registry")
	flag.StringVar(&dockerConfigJSONPath, "dockerconfigjsonpath", "",
		"path for mounted json credentials")
	flag.StringVar(&secretName, "secretname", "",
		"name of to be managed secret")
	flag.StringVar(&secretNamespace, "secretnamespace", "",
		"namespace where original secret can be found")
	flag.StringVar(&excludedNamespaces, "excluded-namespaces", "",
		"comma-separated namespaces excluded from processing")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if !noAutoMaxProcs {
		if _, err := maxprocs.Set(maxprocs.Logger(setupLog.Info)); err != nil {
			setupLog.Error(err, "failed to set GOMAXPROCS")
		}
	}

	if !noAutoMemlimit {
		if _, err := memlimit.SetGoMemLimitWithOpts(
			memlimit.WithRatio(autoMemlimitRatio),
			memlimit.WithProvider(
				memlimit.ApplyFallback(
					memlimit.FromCgroup,
					memlimit.FromSystem,
				),
			),
		); err != nil {
			setupLog.Error(err, "failed to set GOMEMLIMIT")
		}
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
		},
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                enableLeaderElection,
		LeaderElectionID:              "tamcore.github.com-imagepullsecret-patcher",
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	configOptions := config.ConfigOptions{}
	if dockerConfigJSON != "" {
		configOptions.DockerConfigJSON = dockerConfigJSON
	}
	if dockerConfigJSONPath != "" {
		configOptions.DockerConfigJSONPath = dockerConfigJSONPath
	}
	if secretName != "" {
		configOptions.SecretName = secretName
	}
	if secretNamespace != "" {
		configOptions.SecretNamespace = secretNamespace
	}
	if excludedNamespaces != "" {
		configOptions.ExcludedNamespaces = excludedNamespaces
	}
	if serviceAccounts != "" {
		configOptions.ServiceAccounts = serviceAccounts
	}
	controllerConfig := config.NewConfig(configOptions)

	if err = (&controller.ServiceAccountReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Config: controllerConfig,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ServiceAccount")
		os.Exit(1)
	}
	if err = (&controller.SecretReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Config: controllerConfig,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Secret")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
