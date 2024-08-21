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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	Scheme *runtime.Scheme
	Config *config.Config
}

//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info("Reconciling imagePullSecret in " + req.Namespace)
	doPatch := false
	if didPatch, err := utils.ReconcileImagePullSecret(ctx, r.Client, r.Config, req.NamespacedName.Name, req.NamespacedName.Namespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("Failed to reconcile imagePullSecret in Namespace '"+req.NamespacedName.Namespace+"': %v", err)
	} else {
		doPatch = didPatch
	}

	if doPatch {
		if err := utils.CleanupPodsForNamespace(ctx, r.Config, r.Client, req.NamespacedName.Namespace); err != nil {
			return ctrl.Result{}, fmt.Errorf("Failed to cleanup Pods in unauthorized state: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

func secretToObject(secret *corev1.Secret) client.Object {
	return secret
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.TODO()

	builder := ctrl.NewControllerManagedBy(mgr).
		Named("SecretController").
		For(&corev1.Secret{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return utils.IsManagedSecret(r.Config, utils.FetchNamespace(ctx, r.Client, e.Object.GetNamespace()), e.Object)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return utils.IsManagedSecret(r.Config, utils.FetchNamespace(ctx, r.Client, e.ObjectNew.GetNamespace()), e.ObjectNew)
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return utils.IsManagedSecret(r.Config, utils.FetchNamespace(ctx, r.Client, e.Object.GetNamespace()), e.Object)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return utils.IsManagedSecret(r.Config, utils.FetchNamespace(ctx, r.Client, e.Object.GetNamespace()), e.Object)
			},
		})

	// If DockerConfigJSONPath is defined
	if r.Config.DockerConfigJSONPath != "" {
		// Create a GenericEvent channel, to pass reconcile events to the controller
		secretRconciliationSourceChannel := make(chan event.GenericEvent)

		// Set up a goroutine, which does a basic polling watch on DockerConfigJSONPath
		go func() {
			ctx := context.TODO()
			log.FromContext(ctx).Info("setting up watcher")

			for {
				// Wait, until DockerConfigJSONPath has changed
				utils.WaitUntilFileChanges(r.Config.DockerConfigJSONPath)

				// Fetch all Secrets
				secretList := &corev1.SecretList{}
				if err := r.Client.List(ctx, secretList); err != nil {
					log.FromContext(ctx).Error(err, "error listing secrets")
				}

				for _, d := range secretList.Items {
					// Filter for Secrets that are actually managed
					if utils.IsManagedSecret(r.Config, utils.FetchNamespace(ctx, r.Client, d.GetNamespace()), secretToObject(&d)) {
						// Send reconcile event for fetched Secret
						secretRconciliationSourceChannel <- event.GenericEvent{Object: &d}
					}
				}
			}
		}()

		// Attach channel event source to controller
		builder = builder.WatchesRawSource(source.Channel(secretRconciliationSourceChannel, &handler.EnqueueRequestForObject{}))
	}

	return builder.Complete(r)
}
