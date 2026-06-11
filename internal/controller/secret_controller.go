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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
	"github.com/tamcore/imagepullsecret-patcher/internal/utils"
)

// SecretReconciler reconciles a Secret object
type SecretReconciler struct {
	client.Client
	// APIReader reads directly from the API server, bypassing the
	// label-filtered cache (used to inspect pre-existing secrets).
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Config    *config.Config
}

//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Only the managed imagePullSecret itself is reconciled here. Requests for
	// other secrets (e.g. annotated ones, or channel-sourced events) must not be
	// conflated with the secret named in the configuration.
	if req.Name != r.Config.SecretName {
		return ctrl.Result{}, nil
	}

	// Re-check the namespace here because the predicates fail open on
	// namespace lookup errors. Without this, a cache hiccup could lead to
	// patching secrets in excluded or terminating namespaces.
	ns, err := utils.FetchNamespace(ctx, r.Client, req.Namespace)
	if err != nil {
		if apierrs.IsNotFound(err) {
			// Namespace is gone (e.g. event queued during namespace deletion).
			// The SA controller ensures secrets in new namespaces, so there is
			// nothing left to do here.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to fetch namespace: %w", err)
	}
	if utils.IsNamespaceExcluded(r.Config, ns) || !ns.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling imagePullSecret", "namespace", req.Namespace)
	doPatch := false
	if didPatch, err := utils.ReconcileImagePullSecret(ctx, r.Client, r.APIReader, r.Config, req.Namespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile imagePullSecret in namespace %q: %w", req.Namespace, err)
	} else {
		doPatch = didPatch
	}

	if doPatch && r.Config.FeatureDeletePods {
		if err := utils.CleanupPodsForNamespace(ctx, r.Config, r.Client, req.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to cleanup Pods in unauthorized state: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		Named("SecretController").
		For(&corev1.Secret{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Config.MaxConcurrentReconciles}).
		WithEventFilter(r.managedPredicate())

	// If DockerConfigJSONPath is defined, watch it for changes through a
	// manager runnable. The manager hands the watcher its own context (clean
	// shutdown) and gates it behind leader election (only the leader polls).
	if r.Config.DockerConfigJSONPath != "" && r.Config.FeatureWatchDockerConfigJSONPath {
		events := make(chan event.GenericEvent, watcherEventBuffer)
		if err := mgr.Add(&DockerConfigWatcher{Client: r.Client, Config: r.Config, Events: events}); err != nil {
			return err
		}

		// Attach channel event source to controller
		builder = builder.WatchesRawSource(source.Channel(events, &handler.EnqueueRequestForObject{}))
	}

	return builder.Complete(r)
}

// managedPredicate filters events down to secrets this controller manages.
// When the namespace lookup fails (e.g. a namespace not yet in the informer
// cache), it fails open so Reconcile can re-check with proper error handling
// instead of dropping the event permanently.
func (r *SecretReconciler) managedPredicate() predicate.Funcs {
	shouldProcess := func(obj client.Object) bool {
		ns, err := utils.FetchNamespace(context.Background(), r.Client, obj.GetNamespace())
		if err != nil {
			log.Log.V(1).Info("failed to fetch namespace in predicate, processing event anyway",
				"namespace", obj.GetNamespace(), "reason", err.Error())
			return true
		}
		return utils.IsManagedSecret(r.Config, ns, obj)
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return shouldProcess(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return shouldProcess(e.ObjectNew) },
		GenericFunc: func(e event.GenericEvent) bool { return shouldProcess(e.Object) },
		DeleteFunc: func(e event.DeleteEvent) bool {
			ns, err := utils.FetchNamespace(context.Background(), r.Client, e.Object.GetNamespace())
			if err != nil {
				// Fail open; Reconcile no-ops if the namespace is gone.
				return true
			}
			if !ns.DeletionTimestamp.IsZero() {
				// Do not recreate secrets in terminating namespaces.
				return false
			}
			return utils.IsManagedSecret(r.Config, ns, e.Object)
		},
	}
}
