/*
Copyright 2026.

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

// Command before runs the reconstructed M3 controller against whatever cluster
// the current kubeconfig points at. Run it through the Taskfile, which pins
// KUBECONFIG to this scenario's own cluster. Metrics, health probes, leader
// election and the webhook are all left out: none of them take part in the
// failure this scenario shows.
package main

import (
	"flag"
	"os"

	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	platformv1alpha1 "github.com/ChenYuTingJerry/idp-platform-lab/scenarios/03-second-service/before/api/v1alpha1"
	"github.com/ChenYuTingJerry/idp-platform-lab/scenarios/03-second-service/before/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := platformv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
}

func main() {
	var workloadsRepoURL, workloadsTargetRevision string
	flag.StringVar(&workloadsRepoURL, "workloads-repo-url", "",
		"Git repo holding the workload manifests ArgoCD syncs. Required.")
	flag.StringVar(&workloadsTargetRevision, "workloads-target-revision", "HEAD",
		"Git revision the generated Application tracks.")
	flag.Parse()

	// Plain console lines with no stack traces: a stack trace on every retry
	// buries the one line a reader is here to see. UseDevMode(false) alone is
	// not enough, because it still adds a stack trace at error level, and
	// "Reconciler error" is logged at error level.
	ctrl.SetLogger(zap.New(
		zap.UseDevMode(false),
		zap.ConsoleEncoder(),
		zap.StacktraceLevel(zapcore.DPanicLevel),
	))

	if workloadsRepoURL == "" {
		setupLog.Error(nil, "--workloads-repo-url is required")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		setupLog.Error(err, "failed to start manager")
		os.Exit(1)
	}

	if err := (&controller.ServiceClaimReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		WorkloadsRepoURL:        workloadsRepoURL,
		WorkloadsTargetRevision: workloadsTargetRevision,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "failed to create controller")
		os.Exit(1)
	}

	setupLog.Info("starting reconstructed M3 controller")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager exited with error")
		os.Exit(1)
	}
}
