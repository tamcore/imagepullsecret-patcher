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

package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
)

func IsServiceAccountManaged(c *config.Config, namespace client.Object, serviceAccount client.Object) bool {
	if IsNamespaceExcluded(c, namespace) || IsServiceAccountExcluded(c, serviceAccount) {
		return false
	}
	if IsStringInList(serviceAccount.GetName(), c.ServiceAccounts) {
		return true
	}

	return false
}

func IsNamespaceExcluded(c *config.Config, namespace client.Object) bool {
	if IsStringInList(namespace.GetName(), c.ExcludedNamespaces) {
		return true
	}

	return HasAnnotation(namespace, c.ExcludeAnnotation, "true")
}

func IsStringInList(find string, list string) bool {
	for ex := range strings.SplitSeq(list, ",") {
		match, _ := filepath.Match(ex, find)
		if ex == find || match {
			return true
		}
	}
	return false
}

func IsServiceAccountExcluded(c *config.Config, serviceAccount client.Object) bool {
	return HasAnnotation(serviceAccount, c.ExcludeAnnotation, "true")
}

func IsManagedSecret(c *config.Config, namespace client.Object, secret client.Object) bool {
	if IsNamespaceExcluded(c, namespace) {
		return false
	}

	// Check whether secret has set annotation of name "app.kubernetes.io/managed-by"
	// set to value equal to "imagepullsecret-patcher"
	if HasAnnotation(secret, config.AnnotationManagedBy, config.AnnotationAppName) {
		return true
	}

	return secret.GetName() == c.SecretName && secret.GetNamespace() != c.SecretNamespace
}

func HasAnnotation(obj client.Object, annotationKey string, annotationValue string) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return false
	}
	excludeAnnotation, ok := annotations[annotationKey]
	if ok && excludeAnnotation == annotationValue {
		return true
	}
	return false
}

func FetchNamespace(ctx context.Context, client client.Client, namespaceName string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{}
	err := client.Get(ctx,
		types.NamespacedName{
			Name: namespaceName,
		},
		ns,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch namespace: %w", err)
	}
	return ns, nil
}

func FetchServiceAccount(ctx context.Context, client client.Client, namespace string, serviceAccount string) (*corev1.ServiceAccount, error) {
	sa := &corev1.ServiceAccount{}
	err := client.Get(ctx,
		types.NamespacedName{
			Name:      serviceAccount,
			Namespace: namespace,
		},
		sa,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch serviceAccount: %w", err)
	}
	return sa, nil
}

//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;delete

func CleanupPodsForNamespace(ctx context.Context, c *config.Config, k8sClient client.Client, namespace string) error {
	ns, err := FetchNamespace(ctx, k8sClient, namespace)
	if err != nil {
		return fmt.Errorf("failed to fetch namespace: %w", err)
	}
	if IsNamespaceExcluded(c, ns) {
		return nil
	}

	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to fetch pods: %w", err)
	}

	// Cache the managed-state per ServiceAccount so pods sharing one don't
	// trigger repeated lookups.
	saManaged := map[string]bool{}
	for _, pod := range podList.Items {
		managed, known := saManaged[pod.Spec.ServiceAccountName]
		if !known {
			sa, err := FetchServiceAccount(ctx, k8sClient, namespace, pod.Spec.ServiceAccountName)
			if err != nil {
				if apierrs.IsNotFound(err) {
					// The ServiceAccount is gone; its pods are not managed by us.
					saManaged[pod.Spec.ServiceAccountName] = false
					continue
				}
				return fmt.Errorf("failed to fetch serviceAccount: %w", err)
			}
			managed = IsServiceAccountManaged(c, ns, sa)
			saManaged[pod.Spec.ServiceAccountName] = managed
		}
		if !managed {
			continue
		}

		if err := deletePodIfImagePullFailed(ctx, k8sClient, &pod); err != nil {
			return err
		}
	}

	return nil
}

func CleanupPodsForSA(ctx context.Context, k8sClient client.Client, namespace string, serviceAccount string) error {
	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to fetch pods: %w", err)
	}

	for _, pod := range podList.Items {
		if pod.Spec.ServiceAccountName != serviceAccount {
			continue
		}

		if err := deletePodIfImagePullFailed(ctx, k8sClient, &pod); err != nil {
			return err
		}
	}

	return nil
}

// deletePodIfImagePullFailed deletes the pod once if any of its containers is
// stuck in an image pull failure. An already-deleted pod is not an error.
func deletePodIfImagePullFailed(ctx context.Context, k8sClient client.Client, pod *corev1.Pod) error {
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.State.Waiting == nil {
			continue
		}
		reason := containerStatus.State.Waiting.Reason
		if reason != "ErrImagePull" && reason != "ImagePullBackOff" {
			continue
		}

		log.FromContext(ctx).Info("Deleting Pod due to image pull failure",
			"pod", pod.Name, "namespace", pod.Namespace, "reason", reason)
		if err := k8sClient.Delete(ctx, pod); err != nil && !apierrs.IsNotFound(err) {
			return fmt.Errorf("failed to delete Pod %q in namespace %q: %w", pod.Name, pod.Namespace, err)
		}
		// The pod is deleted; checking the remaining containers would only
		// trigger redundant deletes.
		return nil
	}
	return nil
}

func ReconcileImagePullSecret(ctx context.Context, k8sClient client.Client, apiReader client.Reader, c *config.Config, namespace string) (bool, error) {
	if apiReader == nil {
		apiReader = k8sClient
	}

	desiredSecret, err := ConstructImagePullSecret(c, namespace)
	if err != nil {
		return false, fmt.Errorf("failed to construct imagePullSecret: %w", err)
	}

	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx,
		types.NamespacedName{
			Name:      c.SecretName,
			Namespace: namespace,
		},
		secret,
	); err != nil {
		if apierrs.IsNotFound(err) {
			// If Secret does not exist create it right away and return
			if err := k8sClient.Create(ctx, desiredSecret); err != nil {
				if apierrs.IsAlreadyExists(err) {
					// Secret exists but is not in the label-filtered cache (e.g., pre-existing
					// installation without the managed-by label). Adopt it.
					return adoptExistingSecret(ctx, k8sClient, apiReader, desiredSecret)
				}
				return false, fmt.Errorf("failed to create Secret: %w", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("failed to fetch Secret: %w", err)
	}

	patchFrom := client.MergeFrom(secret.DeepCopy())

	// Enforce managed annotations/labels key-wise, so that annotations or labels
	// added by users or third-party controllers are preserved. The managed-by
	// label is enforced here as well, to keep hand-edited secrets inside the
	// label-filtered cache.
	annotations, annotationsChanged := enforceMapEntries(secret.Annotations, desiredSecret.Annotations)
	labels, labelsChanged := enforceMapEntries(secret.Labels, desiredSecret.Labels)
	secret.Annotations = annotations
	secret.Labels = labels

	doPatch := annotationsChanged || labelsChanged
	if !reflect.DeepEqual(secret.Data, desiredSecret.Data) {
		secret.Data = desiredSecret.Data
		doPatch = true
	}

	if doPatch {
		if err = k8sClient.Patch(ctx, secret, patchFrom); err != nil {
			return false, fmt.Errorf("failed to patch Secret %q in namespace %q: %w", desiredSecret.GetName(), desiredSecret.GetNamespace(), err)
		}
	}
	return doPatch, nil
}

// enforceMapEntries returns a copy of current with every key/value pair from
// desired enforced. Keys present only in current (e.g. user or third-party
// annotations/labels) are preserved. The boolean reports whether any desired
// entry was missing or differing.
func enforceMapEntries(current map[string]string, desired map[string]string) (map[string]string, bool) {
	merged := make(map[string]string, len(current)+len(desired))
	maps.Copy(merged, current)

	changed := false
	for k, v := range desired {
		if existing, ok := merged[k]; !ok || existing != v {
			merged[k] = v
			changed = true
		}
	}
	return merged, changed
}

// adoptExistingSecret handles Create conflicts for secrets that exist in the
// cluster but are invisible to the label-filtered cache (e.g. pre-existing
// installations without the managed-by label). The live secret is fetched via
// the uncached apiReader. Secrets of the correct type are adopted in place via
// merge patch. Secret.type is immutable in Kubernetes, so a secret of any
// other type can never serve as imagePullSecret and is deleted and recreated.
func adoptExistingSecret(ctx context.Context, k8sClient client.Client, apiReader client.Reader, desiredSecret *corev1.Secret) (bool, error) {
	live := &corev1.Secret{}
	if err := apiReader.Get(ctx, client.ObjectKeyFromObject(desiredSecret), live); err != nil {
		return false, fmt.Errorf("failed to fetch pre-existing Secret: %v", err)
	}

	if live.Type == corev1.SecretTypeDockerConfigJson {
		return patchUnmanagedSecret(ctx, k8sClient, desiredSecret)
	}

	log.FromContext(ctx).Info("Recreating pre-existing Secret with incompatible type",
		"name", live.GetName(),
		"namespace", live.GetNamespace(),
		"type", string(live.Type),
	)
	if err := k8sClient.Delete(ctx, live, client.Preconditions{UID: &live.UID}); err != nil {
		return false, fmt.Errorf("failed to delete pre-existing Secret with incompatible type: %v", err)
	}
	if err := k8sClient.Create(ctx, desiredSecret.DeepCopy()); err != nil {
		return false, fmt.Errorf("failed to recreate Secret: %v", err)
	}
	return true, nil
}

// patchUnmanagedSecret patches an existing secret that is not in the label-filtered cache.
// This handles upgrades from older versions that created secrets without the managed-by label.
func patchUnmanagedSecret(ctx context.Context, k8sClient client.Client, desiredSecret *corev1.Secret) (bool, error) {
	patchBytes, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"labels":      desiredSecret.Labels,
			"annotations": desiredSecret.Annotations,
		},
		"data": desiredSecret.Data,
	})
	if err != nil {
		return false, fmt.Errorf("failed to marshal patch: %w", err)
	}
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desiredSecret.Name,
			Namespace: desiredSecret.Namespace,
		},
	}
	if err := k8sClient.Patch(ctx, target, client.RawPatch(types.MergePatchType, patchBytes)); err != nil {
		return false, fmt.Errorf("failed to patch existing Secret: %w", err)
	}
	return true, nil
}

func ConstructImagePullSecret(c *config.Config, namespace string) (*corev1.Secret, error) {
	dockerConfigJSON, err := GetDockerConfigJSON(c)
	if err != nil {
		return nil, fmt.Errorf("failed to read dockerConfigJSON: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.SecretName,
			Namespace: namespace,
			Labels: map[string]string{
				config.LabelManagedBy: config.AnnotationAppName,
			},
			Annotations: map[string]string{
				config.AnnotationManagedBy: config.AnnotationAppName,
			},
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(dockerConfigJSON),
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}

	return secret, nil
}

func GetDockerConfigJSON(c *config.Config) (string, error) {
	if c.DockerConfigJSON == "" && c.DockerConfigJSONPath == "" {
		return "", fmt.Errorf("neither CONFIG_DOCKERCONFIGJSON nor CONFIG_DOCKERCONFIGJSONPATH defined")
	}
	if c.DockerConfigJSON != "" && c.DockerConfigJSONPath != "" {
		return "", fmt.Errorf("cannot specify both CONFIG_DOCKERCONFIGJSON and CONFIG_DOCKERCONFIGJSONPATH")
	}
	if c.DockerConfigJSON != "" {
		return c.DockerConfigJSON, nil
	}
	b, err := os.ReadFile(c.DockerConfigJSONPath)
	if err != nil {
		return "", err
	}
	if !json.Valid(b) {
		return "", fmt.Errorf("file %q does not contain valid JSON", c.DockerConfigJSONPath)
	}
	return string(b), nil
}

// fileWatchPollInterval is how often the watched file is polled for changes.
const fileWatchPollInterval = 1 * time.Second

// WaitUntilFileChanges blocks until the modification time of filename changes.
// It returns nil once a change is detected, ctx.Err() when the context is
// cancelled, or a wrapped error if the file cannot be stat'ed initially.
func WaitUntilFileChanges(ctx context.Context, filename string) error {
	initialStat, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("failed to stat watched file %q: %w", filename, err)
	}

	ticker := time.NewTicker(fileWatchPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			stat, err := os.Stat(filename)
			if err != nil {
				log.FromContext(ctx).Error(err, "failed to stat watched file", "path", filename)
				continue
			}
			if !stat.ModTime().Equal(initialStat.ModTime()) {
				return nil
			}
		}
	}
}
