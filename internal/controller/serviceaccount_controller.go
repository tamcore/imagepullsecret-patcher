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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
	"github.com/tamcore/imagepullsecret-patcher/internal/utils"
)

// globMetacharacters are the filepath.Match metacharacters supported by
// utils.IsStringInList. Config entries containing any of them are globs.
const globMetacharacters = "*?["

// namespaceCacheRetryDelay is how long to wait before retrying when an
// object's namespace is not yet visible in the informer cache (e.g. a
// brand-new namespace whose informer event hasn't arrived yet).
const namespaceCacheRetryDelay = 10 * time.Second

// ServiceAccountReconciler reconciles a ServiceAccount object
type ServiceAccountReconciler struct {
	client.Client
	// APIReader reads directly from the API server, bypassing the
	// label-filtered cache (used to inspect pre-existing secrets).
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Config    *config.Config
}

//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *ServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Get(ctx, req.NamespacedName, serviceAccount)
	if err != nil {
		if apierrs.IsNotFound(err) {
			// ServiceAccount was deleted after the event was queued - nothing to do.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get ServiceAccount")
		return ctrl.Result{}, err
	}

	// Not a managed SA
	ns, err := utils.FetchNamespace(ctx, r.Client, serviceAccount.GetNamespace())
	if err != nil {
		if apierrs.IsNotFound(err) {
			// The ServiceAccount exists but its namespace is not yet in the
			// informer cache. Retry shortly; this resolves once the cache
			// catches up, or stops once the SA disappears with its namespace.
			return ctrl.Result{RequeueAfter: namespaceCacheRetryDelay}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to fetch namespace: %w", err)
	}
	if !utils.IsServiceAccountManaged(r.Config, ns, serviceAccount) {
		return ctrl.Result{}, nil
	}

	// Ensure imagePullSecret exists before we attach it to the ServiceAccount
	if _, err = utils.ReconcileImagePullSecret(ctx, r.Client, r.APIReader, r.Config, serviceAccount.GetNamespace()); err != nil {
		return ctrl.Result{}, fmt.Errorf("Failed to reconcile imagePullSecret in Namespace '"+serviceAccount.GetNamespace()+"': %w", err)
	}

	patchFrom := client.MergeFrom(serviceAccount.DeepCopy())
	patchedServiceAccount := r.getPatchedServiceAccount(serviceAccount.DeepCopy(), r.Config.SecretName)

	if !reflect.DeepEqual(serviceAccount.ImagePullSecrets, patchedServiceAccount.ImagePullSecrets) {
		err = r.Patch(ctx, patchedServiceAccount, patchFrom)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch ImagePullSecret to ServiceAccount %s in namespace %s: %w",
				serviceAccount.GetName(), serviceAccount.GetNamespace(), err)
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
//
// Predicates are attached per-watch instead of via WithEventFilter, because
// an event filter would apply to the Namespace watch as well: managedPredicate
// treats the event object as a ServiceAccount and would look up the namespace
// OF the namespace object (empty for cluster-scoped objects), breaking it.
func (r *ServiceAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("ServiceAccountController").
		For(&corev1.ServiceAccount{}, ctrlbuilder.WithPredicates(r.managedPredicate())).
		Watches(&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(r.namespaceToServiceAccounts),
			ctrlbuilder.WithPredicates(namespaceTransitionPredicate(r.Config))).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Config.MaxConcurrentReconciles}).
		Complete(r)
}

// namespaceToServiceAccounts maps a Namespace event to reconcile requests for
// every configured ServiceAccount in that namespace. Excluded namespaces map
// to nothing: only newly-included namespaces need reconciliation, as we never
// detach from newly-excluded ones (matching current behavior).
func (r *ServiceAccountReconciler) namespaceToServiceAccounts(ctx context.Context, obj client.Object) []reconcile.Request {
	if utils.IsNamespaceExcluded(r.Config, obj) {
		return nil
	}

	seen := map[types.NamespacedName]struct{}{}
	var requests []reconcile.Request
	enqueue := func(name string) {
		nn := types.NamespacedName{Namespace: obj.GetName(), Name: name}
		if _, isDuplicate := seen[nn]; isDuplicate {
			return
		}
		seen[nn] = struct{}{}
		requests = append(requests, reconcile.Request{NamespacedName: nn})
	}

	hasGlobEntry := false
	for _, entry := range strings.Split(r.Config.ServiceAccounts, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.ContainsAny(entry, globMetacharacters) {
			hasGlobEntry = true
			continue
		}
		enqueue(entry)
	}

	if !hasGlobEntry {
		return requests
	}

	// Glob entries can't be enqueued directly; list the ServiceAccounts in
	// the namespace and enqueue every one matching the configured list.
	saList := &corev1.ServiceAccountList{}
	if err := r.List(ctx, saList, client.InNamespace(obj.GetName())); err != nil {
		log.FromContext(ctx).Error(err, "failed to list ServiceAccounts while mapping namespace event",
			"namespace", obj.GetName())
		return requests
	}
	for _, sa := range saList.Items {
		if utils.IsStringInList(sa.GetName(), r.Config.ServiceAccounts) {
			enqueue(sa.GetName())
		}
	}

	return requests
}

// namespaceTransitionPredicate only lets Namespace updates through when the
// exclusion state changes, so periodic resync churn doesn't flood the
// workqueue. Create events are dropped because ServiceAccount create events
// already cover brand-new namespaces.
func namespaceTransitionPredicate(c *config.Config) predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return utils.IsNamespaceExcluded(c, e.ObjectOld) != utils.IsNamespaceExcluded(c, e.ObjectNew)
		},
		CreateFunc:  func(e event.CreateEvent) bool { return false },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

// managedPredicate filters events down to ServiceAccounts this controller
// manages. When the namespace lookup fails (e.g. a brand-new namespace not
// yet in the informer cache), it fails open so Reconcile can re-check with
// proper error handling instead of dropping the event permanently.
func (r *ServiceAccountReconciler) managedPredicate() predicate.Funcs {
	shouldProcess := func(obj client.Object) bool {
		ns, err := utils.FetchNamespace(context.Background(), r.Client, obj.GetNamespace())
		if err != nil {
			log.Log.V(1).Info("failed to fetch namespace in predicate, processing event anyway",
				"namespace", obj.GetNamespace(), "reason", err.Error())
			return true
		}
		return utils.IsServiceAccountManaged(r.Config, ns, obj)
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return shouldProcess(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return shouldProcess(e.ObjectNew) },
		GenericFunc: func(e event.GenericEvent) bool { return shouldProcess(e.Object) },
		// Ignore Deletion events
		DeleteFunc: func(e event.DeleteEvent) bool { return false },
	}
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
