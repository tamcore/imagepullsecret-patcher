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
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

func IsNamespaceExcluded(c *config.Config, obj client.Object) bool {
	if IsStringInList(obj.GetName(), c.ExcludedNamespaces) {
		return true
	}

	return HasAnnotation(obj, c.ExcludeAnnotation, "true")
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

func IsServiceAccountExcluded(c *config.Config, obj client.Object) bool {
	return HasAnnotation(obj, c.ExcludeAnnotation, "true")
}

func IsManagedSecret(c *config.Config, namespace client.Object, obj client.Object) bool {
	if IsNamespaceExcluded(c, namespace) {
		return false
	}

	// Check whether secret has set annotation of name "app.kubernetes.io/managed-by"
	// set to value equal to "imagepullsecret-patcher"
	if HasAnnotation(obj, config.AnnotationManagedBy, config.AnnotationAppName) {
		return true
	}

	return obj.GetName() == c.SecretName && obj.GetNamespace() != c.SecretNamespace
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

func FetchNamespace(client client.Client, namespaceName string) *corev1.Namespace {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
		},
	}
	_ = client.Get(context.TODO(),
		types.NamespacedName{
			Name:      namespaceName,
			Namespace: namespaceName,
		},
		namespace,
	)
	// error handling is overrated
	// if err != nil {
	//     return namespace, err
	// }
	return namespace //, nil
}

func ReconcileImagePullSecret(ctx context.Context, k8sClient client.Client, c *config.Config, namespace string) error {
	desiredSecret, err := ConstructImagePullSecret(c, namespace)
	if err != nil {
		return fmt.Errorf("Failed to construct imagePullSecret: %v", err)
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
				return fmt.Errorf("Failed to create Secret: %v", err)
			}
			return nil
		}
		return fmt.Errorf("while fetching Secret: %v", err)
	}

	patchFrom := client.MergeFrom(secret.DeepCopy())
	secret.Annotations = desiredSecret.Annotations
	secret.Data = desiredSecret.Data

	if err = k8sClient.Patch(ctx, secret, patchFrom); err != nil {
		return fmt.Errorf("error while patching Secret '"+desiredSecret.GetName()+"' in namespace '"+desiredSecret.GetNamespace()+"': %v", err)
	}
	return nil
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
