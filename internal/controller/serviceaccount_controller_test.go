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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// controller-runtime envtest doesn't support namespace deletion (https://github.com/kubernetes-sigs/controller-runtime/issues/880)
// To work around that, we just create a new namespace + sa for each test
func makeObjects(namespaceName string, serviceAccountName string, secretName string) (corev1.Namespace, corev1.ServiceAccount, types.NamespacedName, types.NamespacedName) {
	namespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
		},
	}
	serviceAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: namespace.GetName(),
		},
	}
	serviceAccountNN := types.NamespacedName{
		Name:      serviceAccount.GetName(),
		Namespace: serviceAccount.GetNamespace(),
	}
	secretNN := types.NamespacedName{
		Name:      secretName,
		Namespace: serviceAccount.GetNamespace(),
	}

	return namespace, serviceAccount, serviceAccountNN, secretNN
}

var _ = Describe("ServiceAccount Controller", func() {
	Context("When reconciling a ServiceAccount", func() {
		var err error
		ctx := context.Background()
		config := config.NewConfig(
			config.Config{
				DockerConfigJSON:  imagePullSecretData,
				SecretNamespace:   "kube-system",
				FeatureDeletePods: true,
			},
		)

		It("should successfully reconcile the resource", func() {
			namespace, serviceAccount, serviceAccountNN, secretNN := makeObjects("testns-1", "default", config.SecretName)

			By("Creating the Namespace to perform the tests")
			Expect(k8sClient.Create(ctx, namespace.DeepCopy())).Should(Succeed())

			By("Creating the ServiceAccount to reconcile")
			Expect(k8sClient.Create(ctx, serviceAccount.DeepCopy())).Should(Succeed())

			By("Creating a managed Pod with ErrImagePull to cleanup")
			managedPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-errimagepull",
					Namespace: serviceAccount.GetNamespace(),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccount.GetName(),
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "foo.bar",
						},
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason: "ErrImagePull",
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, managedPod)).Should(Succeed())

			By("Creating a unmanaged Pod with ErrImagePull to cleanup")
			unmanagedPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unmanaged-errimagepull",
					Namespace: serviceAccount.GetNamespace(),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "entirely-unrelated-serviceaccount",
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "foo.bar",
						},
					},
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason: "ErrImagePull",
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, unmanagedPod)).Should(Succeed())

			By("Reconciling the ServiceAccount")
			serviceAccountReconciler := &ServiceAccountReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Config: config,
			}
			_, err = serviceAccountReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: serviceAccountNN,
			})
			Expect(err).To(Not(HaveOccurred()))

			By("Checking if Secret was successfully created in the reconciliation")
			Eventually(func() error {
				found := &corev1.Secret{}
				return k8sClient.Get(ctx, secretNN, found)
			}, time.Minute, time.Second).Should(Succeed())
			Expect(err).To(Not(HaveOccurred()))

			By("Checking if created Secret contains expected data")
			foundSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, secretNN, foundSecret)
			if err == nil {
				secretData := string(foundSecret.Data[".dockerconfigjson"])
				if imagePullSecretData != secretData {
					err = fmt.Errorf("Expected %s, got %s", imagePullSecretData, secretData)
				}
			}
			Expect(err).To(Not(HaveOccurred()))

			By("Checking if managed Pod with ErrImagePull was cleaned up during the reconciliation")
			foundManagedPod := &corev1.Pod{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      managedPod.GetName(),
				Namespace: managedPod.GetNamespace(),
			}, foundManagedPod)
			Expect(err).To(HaveOccurred())

			By("Checking if unmanaged Pod with ErrImagePull was cleaned up during the reconciliation")
			foundUnmanagedPod := &corev1.Pod{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      unmanagedPod.GetName(),
				Namespace: unmanagedPod.GetNamespace(),
			}, foundUnmanagedPod)
			Expect(err).To(Not(HaveOccurred()))
		})

		It("should not reconcile the resource", func() {
			namespace, serviceAccount, serviceAccountNN, secretNN := makeObjects("testns-2", "default", config.SecretName)

			By("Creating the Namespace to perform the tests")
			Expect(k8sClient.Create(ctx, namespace.DeepCopy())).Should(Succeed())

			By("Creating the ServiceAccount to reconcile")
			serviceAccount.Annotations = map[string]string{
				config.ExcludeAnnotation: "true",
			}
			Expect(k8sClient.Create(ctx, serviceAccount.DeepCopy())).Should(Succeed())

			By("Reconciling the ServiceAccount")
			serviceAccountReconciler := &ServiceAccountReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Config: config,
			}
			_, err = serviceAccountReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: serviceAccountNN,
			})
			Expect(err).To(Not(HaveOccurred()))

			By("Checking if Secret was NOT created in the reconciliation")
			foundSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, secretNN, foundSecret)
			// This should error out, as the ServiceAccount has the excludeAnnotation
			// and therefore the Secret should not be created.
			Expect(err).To(HaveOccurred())
		})
	})
})
