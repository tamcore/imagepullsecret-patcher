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
	"fmt"
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
	for _, ex := range strings.Split(list, ",") {
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
	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to fetch pods: %w", err)
	}

	for _, pod := range podList.Items {
		ns, err := FetchNamespace(ctx, k8sClient, namespace)
		if err != nil {
			return fmt.Errorf("failed to fetch namespace: %w", err)
		}
		sa, err := FetchServiceAccount(ctx, k8sClient, namespace, pod.Spec.ServiceAccountName)
		if err != nil {
			return fmt.Errorf("failed to fetch serviceAccount: %w", err)
		}
		if !IsServiceAccountManaged(c, ns, sa) {
			continue
		}

		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil {
				if containerStatus.State.Waiting.Reason == "ErrImagePull" || containerStatus.State.Waiting.Reason == "ImagePullBackOff" {
					log.FromContext(ctx).Info("Deleting Pod " + pod.Name + " in " + pod.Namespace + " due to status " + containerStatus.State.Waiting.Reason)
					if err := k8sClient.Delete(ctx, &pod); err != nil {
						return fmt.Errorf("failed to delete Pod "+pod.Name+"in "+pod.Namespace+": %w", err)
					}
				}
			}
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

		for _, containerStatus := range pod.Status.ContainerStatuses {
			if containerStatus.State.Waiting != nil {
				if containerStatus.State.Waiting.Reason == "ErrImagePull" || containerStatus.State.Waiting.Reason == "ImagePullBackOff" {
					log.FromContext(ctx).Info("Deleting Pod " + pod.Name + " in " + pod.Namespace + " due to status " + containerStatus.State.Waiting.Reason)
					if err := k8sClient.Delete(ctx, &pod); err != nil {
						return fmt.Errorf("failed to delete Pod "+pod.Name+"in "+pod.Namespace+": %w", err)
					}
				}
			}
		}
	}

	return nil
}

func ReconcileImagePullSecret(ctx context.Context, k8sClient client.Client, c *config.Config, secretName string, namespace string) (bool, error) {
	desiredSecret, err := ConstructImagePullSecret(c, namespace)
	if err != nil {
		return false, fmt.Errorf("Failed to construct imagePullSecret: %v", err)
	}

	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx,
		types.NamespacedName{
			Name:      secretName,
			Namespace: namespace,
		},
		secret,
	); err != nil {
		if apierrs.IsNotFound(err) {
			// If Secret does not exist create it right away and return
			if err := k8sClient.Create(ctx, desiredSecret); err != nil {
				return false, fmt.Errorf("Failed to create Secret: %v", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("while fetching Secret: %v", err)
	}

	inClusterSecret := secret.DeepCopy()
	patchFrom := client.MergeFrom(secret.DeepCopy())
	secret.Annotations = desiredSecret.Annotations
	secret.Data = desiredSecret.Data

	doPatch := false
	if !reflect.DeepEqual(inClusterSecret.Annotations, desiredSecret.Annotations) {
		doPatch = true
	}
	if !reflect.DeepEqual(inClusterSecret.Data, desiredSecret.Data) {
		doPatch = true
	}
	if doPatch {
		if err = k8sClient.Patch(ctx, secret, patchFrom); err != nil {
			return false, fmt.Errorf("error while patching Secret '"+desiredSecret.GetName()+"' in namespace '"+desiredSecret.GetNamespace()+"': %v", err)
		}
	}
	return doPatch, nil
}

func ConstructImagePullSecret(c *config.Config, namespace string) (*corev1.Secret, error) {
	dockerConfigJSON, err := GetDockerConfigJSON(c)
	if err != nil {
		return nil, fmt.Errorf("Error while reading dockerConfigJSON from filesystem: %v", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.SecretName,
			Namespace: namespace,
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
		return "", fmt.Errorf("Neither `CONFIG_DOCKERCONFIGJSON or `CONFIG_DOCKERCONFIGJSONPATH defined.")
	}
	if c.DockerConfigJSON != "" && c.DockerConfigJSONPath != "" {
		return "", fmt.Errorf("Cannot specify both `CONFIG_DOCKERCONFIGJSON` and `CONFIG_DOCKERCONFIGJSONPATH`")
	}
	if c.DockerConfigJSON != "" {
		return c.DockerConfigJSON, nil
	}
	b, ok := os.ReadFile(c.DockerConfigJSONPath)
	return string(b), ok
}

func WaitUntilFileChanges(filename string) {
	initialStat, _ := os.Stat(filename)
	for {
		time.Sleep(1 * time.Second)
		stat, err := os.Stat(filename)
		if err != nil {
			fmt.Println("Error:", err)
			continue
		}
		if stat.ModTime() != initialStat.ModTime() {
			return
		}
	}
}
