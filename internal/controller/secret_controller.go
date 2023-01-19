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
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

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
	if err := utils.ReconcileImagePullSecret(ctx, r.Client, r.Config, req.NamespacedName.Namespace); err != nil {
		return ctrl.Result{}, fmt.Errorf("Failed to reconcile imagePullSecret in Namespace '"+req.NamespacedName.Namespace+"': %v", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("SecretController").
		For(&corev1.Secret{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return utils.IsManagedSecret(r.Config, utils.FetchNamespace(r.Client, e.Object.GetNamespace()), e.Object)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return utils.IsManagedSecret(r.Config, utils.FetchNamespace(r.Client, e.ObjectNew.GetNamespace()), e.ObjectNew)
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return utils.IsManagedSecret(r.Config, utils.FetchNamespace(r.Client, e.Object.GetNamespace()), e.Object)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return utils.IsManagedSecret(r.Config, utils.FetchNamespace(r.Client, e.Object.GetNamespace()), e.Object)
			},
		}).
		Complete(r)
}
