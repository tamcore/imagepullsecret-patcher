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
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
	"github.com/tamcore/imagepullsecret-patcher/internal/utils"
)

// ServiceAccountReconciler reconciles a ServiceAccount object
type ServiceAccountReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config *config.Config
}

//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *ServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Get(ctx, req.NamespacedName, serviceAccount)
	if err != nil {
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get ServiceAccount")
		return ctrl.Result{}, err
	}

	// Not a managed SA
	ns, err := utils.FetchNamespace(ctx, r.Client, serviceAccount.GetNamespace())
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to fetch namespace: %w", err)
	}
	if !utils.IsServiceAccountManaged(r.Config, ns, serviceAccount) {
		return ctrl.Result{}, nil
	}

	// Ensure imagePullSecret exists before we attach it to the ServiceAccount
	if _, err = utils.ReconcileImagePullSecret(ctx, r.Client, r.Config, r.Config.SecretName, serviceAccount.GetNamespace()); err != nil {
		return ctrl.Result{}, fmt.Errorf("Failed to reconcile imagePullSecret in Namespace '"+serviceAccount.GetNamespace()+"': %w", err)
	}

	patchFrom := client.MergeFrom(serviceAccount.DeepCopy())
	patchedServiceAccount := r.getPatchedServiceAccount(serviceAccount.DeepCopy(), r.Config.SecretName)

	if !reflect.DeepEqual(serviceAccount.ImagePullSecrets, patchedServiceAccount.ImagePullSecrets) {
		err = r.Patch(ctx, patchedServiceAccount, patchFrom)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("[%s] Failed to patch ImagePullSecret to ServiceAccount '"+serviceAccount.GetName()+"' in namespace '"+serviceAccount.GetNamespace()+"': %w", err)
		}
		log.Info("Attached ImagePullSecret to ServiceAccount '" + serviceAccount.GetName() + "' in namespace '" + serviceAccount.GetNamespace() + "'")

		if r.Config.FeatureDeletePods {
			// Run Pod cleanup only if we're freshly attaching the imagePullSecret to the ServiceAccount
			if err = utils.CleanupPodsForSA(ctx, r.Client, serviceAccount.GetNamespace(), serviceAccount.GetName()); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to cleanup Pods in unauthorized state: %w", err)
			}
			log.Info("Cleaned up Pods belonging to ServiceAccount " + serviceAccount.GetName())
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.TODO()
	return ctrl.NewControllerManagedBy(mgr).
		Named("ServiceAccountController").
		For(&corev1.ServiceAccount{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Config.MaxConcurrentReconciles}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				ns, err := utils.FetchNamespace(ctx, r.Client, e.Object.GetNamespace())
				if err != nil {
					return false
				}
				return utils.IsServiceAccountManaged(r.Config, ns, e.Object)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				ns, err := utils.FetchNamespace(ctx, r.Client, e.ObjectNew.GetNamespace())
				if err != nil {
					return false
				}
				return utils.IsServiceAccountManaged(r.Config, ns, e.ObjectNew)
			},
			GenericFunc: func(e event.GenericEvent) bool {
				ns, err := utils.FetchNamespace(ctx, r.Client, e.Object.GetNamespace())
				if err != nil {
					return false
				}
				return utils.IsServiceAccountManaged(r.Config, ns, e.Object)
			},
			// Ignore Deletion events
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
		}).
		Complete(r)
}

// Check if service account contains imagePullSecret with name equal to secretName
func (r *ServiceAccountReconciler) includeImagePullSecret(sa *corev1.ServiceAccount, secretName string) bool {
	for _, imagePullSecret := range sa.ImagePullSecrets {
		if imagePullSecret.Name == secretName {
			return true
		}
	}
	return false
}

// Append to existing list of imagePullSecret names a new item with name of secretName
func (r *ServiceAccountReconciler) getPatchedServiceAccount(sa *corev1.ServiceAccount, secretName string) *corev1.ServiceAccount {
	if !r.includeImagePullSecret(sa, secretName) {
		sa.ImagePullSecrets = append(sa.ImagePullSecrets, corev1.LocalObjectReference{Name: secretName})
	}
	return sa
}
