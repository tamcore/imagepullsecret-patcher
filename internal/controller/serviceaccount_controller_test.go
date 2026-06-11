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
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
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

// unlabeledSecretsNotFound is a Get interceptor simulating the label-filtered
// cache used in production: secrets without the managed-by label are invisible
// to Get (NotFound) while still existing in the API server (Create returns
// AlreadyExists).
func unlabeledSecretsNotFound(
	ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption,
) error {
	if err := c.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if secret, ok := obj.(*corev1.Secret); ok {
		if secret.Labels[config.LabelManagedBy] != config.AnnotationAppName {
			return apierrs.NewNotFound(
				schema.GroupResource{Group: "", Resource: "secrets"},
				key.Name,
			)
		}
	}
	return nil
}

var _ = Describe("ServiceAccount Controller", func() {
	Context("When reconciling a ServiceAccount", func() {
		var err error
		ctx := context.Background()
		cfg, err := config.NewConfig(
			config.WithDockerConfigJSON(imagePullSecretData),
			config.WithSecretNamespace(kubeSystemNs),
			config.WithFeatureDeletePods(true),
		)
		if err != nil {
			panic(err)
		}

		It("should successfully reconcile the resource", func() {
			namespace, serviceAccount, serviceAccountNN, secretNN := makeObjects("testns-1", saDefault, cfg.SecretName)

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
				Config: cfg,
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

		It("should ignore a ServiceAccount that no longer exists", func() {
			_, _, serviceAccountNN, secretNN := makeObjects("testns-deleted", "ghost", cfg.SecretName)

			By("Reconciling a ServiceAccount that was never created")
			serviceAccountReconciler := &ServiceAccountReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Config: cfg,
			}
			result, err := serviceAccountReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: serviceAccountNN,
			})

			By("Expecting no error and no requeue, as NotFound must be ignored")
			Expect(err).To(Not(HaveOccurred()))
			Expect(result).To(Equal(reconcile.Result{}))

			By("Checking that no Secret was created")
			foundSecret := &corev1.Secret{}
			err = k8sClient.Get(ctx, secretNN, foundSecret)
			Expect(err).To(HaveOccurred())
		})

		It("should not reconcile the resource", func() {
			namespace, serviceAccount, serviceAccountNN, secretNN := makeObjects("testns-2", saDefault, cfg.SecretName)

			By("Creating the Namespace to perform the tests")
			Expect(k8sClient.Create(ctx, namespace.DeepCopy())).Should(Succeed())

			By("Creating the ServiceAccount to reconcile")
			serviceAccount.Annotations = map[string]string{
				cfg.ExcludeAnnotation: annotationTrue,
			}
			Expect(k8sClient.Create(ctx, serviceAccount.DeepCopy())).Should(Succeed())

			By("Reconciling the ServiceAccount")
			serviceAccountReconciler := &ServiceAccountReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Config: cfg,
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

	// Regression test for: secrets "paas-imagepullsecret" already exists
	// When upgrading from a version that didn't set the managed-by label,
	// the label-filtered cache returns NotFound for pre-existing secrets,
	// causing Create to fail with AlreadyExists.
	Context("When upgrading from a version without the managed-by label", func() {
		ctx := context.Background()
		cfg, err := config.NewConfig(
			config.WithDockerConfigJSON(imagePullSecretData),
			config.WithSecretNamespace(kubeSystemNs),
		)
		if err != nil {
			panic(err)
		}

		It("should adopt the pre-existing secret and add managed-by labels", func() {
			namespace, serviceAccount, serviceAccountNN, secretNN := makeObjects("testns-upgrade", saDefault, cfg.SecretName)

			oldSecretData := `{"auth":{"old.example.com":{}}}`
			preExistingSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cfg.SecretName,
					Namespace: namespace.GetName(),
				},
				Data: map[string][]byte{
					corev1.DockerConfigJsonKey: []byte(oldSecretData),
				},
				Type: corev1.SecretTypeDockerConfigJson,
			}

			// Build a client that simulates label-filtered cache behavior.
			// In production, the manager's cache only watches secrets with the
			// managed-by label. Pre-existing secrets without it are invisible
			// to Get (returns NotFound) while still existing in the API server
			// (Create returns AlreadyExists). The raw (un-intercepted) client
			// doubles as the uncached API reader.
			testScheme := kruntime.NewScheme()
			Expect(clientgoscheme.AddToScheme(testScheme)).NotTo(HaveOccurred())

			rawClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(
					namespace.DeepCopy(),
					serviceAccount.DeepCopy(),
					preExistingSecret,
				).
				Build()
			labelFilteredClient := interceptor.NewClient(rawClient, interceptor.Funcs{
				Get: unlabeledSecretsNotFound,
			})

			By("Reconciling the ServiceAccount with a pre-existing unlabeled secret")
			reconciler := &ServiceAccountReconciler{
				Client:    labelFilteredClient,
				APIReader: rawClient,
				Scheme:    testScheme,
				Config:    cfg,
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: serviceAccountNN,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the secret was adopted with managed-by label and annotation")
			foundSecret := &corev1.Secret{}
			Expect(labelFilteredClient.Get(ctx, secretNN, foundSecret)).To(Succeed())
			Expect(foundSecret.Labels).To(HaveKeyWithValue(config.LabelManagedBy, config.AnnotationAppName))
			Expect(foundSecret.Annotations).To(HaveKeyWithValue(config.AnnotationManagedBy, config.AnnotationAppName))

			By("Verifying the secret data was updated to current credentials")
			Expect(string(foundSecret.Data[corev1.DockerConfigJsonKey])).To(Equal(imagePullSecretData))

			By("Verifying the ServiceAccount has the imagePullSecret reference")
			foundSA := &corev1.ServiceAccount{}
			Expect(labelFilteredClient.Get(ctx, serviceAccountNN, foundSA)).To(Succeed())
			Expect(foundSA.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: cfg.SecretName}))

			By("Verifying idempotency - second reconciliation succeeds without errors")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: serviceAccountNN,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When the namespace is not yet visible in the cache", func() {
		ctx := context.Background()
		cfg, err := config.NewConfig(
			config.WithDockerConfigJSON(imagePullSecretData),
			config.WithSecretNamespace(kubeSystemNs),
		)
		if err != nil {
			panic(err)
		}

		newFakeClient := func(objs ...client.Object) client.Client {
			scheme := kruntime.NewScheme()
			Expect(clientgoscheme.AddToScheme(scheme)).NotTo(HaveOccurred())
			return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
		}

		It("should requeue instead of dropping the ServiceAccount", func() {
			serviceAccount := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: saDefault, Namespace: "ns-not-in-cache"},
			}
			c := newFakeClient(serviceAccount)
			reconciler := &ServiceAccountReconciler{Client: c, Scheme: c.Scheme(), Config: cfg}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: saDefault, Namespace: "ns-not-in-cache"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(namespaceCacheRetryDelay))
		})

		It("should fail open in predicates so Reconcile can decide", func() {
			c := newFakeClient(
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "sa-predns-managed"}},
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
					Name:        "sa-predns-excluded",
					Annotations: map[string]string{cfg.ExcludeAnnotation: annotationTrue},
				}},
			)
			reconciler := &ServiceAccountReconciler{Client: c, Scheme: c.Scheme(), Config: cfg}
			pred := reconciler.managedPredicate()

			saIn := func(namespace string) *corev1.ServiceAccount {
				return &corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{Name: saDefault, Namespace: namespace},
				}
			}

			By("processing ServiceAccounts in managed namespaces")
			Expect(pred.Create(event.CreateEvent{Object: saIn("sa-predns-managed")})).To(BeTrue())
			Expect(pred.Update(event.UpdateEvent{ObjectNew: saIn("sa-predns-managed")})).To(BeTrue())

			By("dropping ServiceAccounts in excluded namespaces")
			Expect(pred.Create(event.CreateEvent{Object: saIn("sa-predns-excluded")})).To(BeFalse())

			By("failing open when the namespace lookup fails")
			Expect(pred.Create(event.CreateEvent{Object: saIn("sa-predns-missing")})).To(BeTrue())

			By("still ignoring delete events")
			Expect(pred.Delete(event.DeleteEvent{Object: saIn("sa-predns-managed")})).To(BeFalse())
		})
	})

	// Secret.type is immutable in Kubernetes. A pre-existing secret with the
	// wrong type (e.g. Opaque) can never be merge-patched into a usable
	// imagePullSecret, so it has to be deleted and recreated.
	Context("When adopting a pre-existing secret with an incompatible type", func() {
		ctx := context.Background()
		cfg, err := config.NewConfig(
			config.WithDockerConfigJSON(imagePullSecretData),
			config.WithSecretNamespace(kubeSystemNs),
		)
		if err != nil {
			panic(err)
		}

		It("should delete and recreate the secret with the correct type", func() {
			namespace, serviceAccount, serviceAccountNN, secretNN := makeObjects("testns-wrong-type", "default", cfg.SecretName)

			preExistingSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cfg.SecretName,
					Namespace: namespace.GetName(),
				},
				Data: map[string][]byte{
					"some-key": []byte("some-value"),
				},
				Type: corev1.SecretTypeOpaque,
			}

			testScheme := kruntime.NewScheme()
			Expect(clientgoscheme.AddToScheme(testScheme)).NotTo(HaveOccurred())

			rawClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(
					namespace.DeepCopy(),
					serviceAccount.DeepCopy(),
					preExistingSecret,
				).
				Build()
			labelFilteredClient := interceptor.NewClient(rawClient, interceptor.Funcs{
				Get: unlabeledSecretsNotFound,
			})

			By("Reconciling the ServiceAccount with a pre-existing wrong-type secret")
			reconciler := &ServiceAccountReconciler{
				Client:    labelFilteredClient,
				APIReader: rawClient,
				Scheme:    testScheme,
				Config:    cfg,
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: serviceAccountNN,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the secret was recreated with the dockerconfigjson type")
			foundSecret := &corev1.Secret{}
			Expect(rawClient.Get(ctx, secretNN, foundSecret)).To(Succeed())
			Expect(foundSecret.Type).To(Equal(corev1.SecretTypeDockerConfigJson))

			By("Verifying the recreated secret carries the managed labels and annotations")
			Expect(foundSecret.Labels).To(HaveKeyWithValue(config.LabelManagedBy, config.AnnotationAppName))
			Expect(foundSecret.Annotations).To(HaveKeyWithValue(config.AnnotationManagedBy, config.AnnotationAppName))

			By("Verifying the recreated secret contains the current credentials")
			Expect(string(foundSecret.Data[corev1.DockerConfigJsonKey])).To(Equal(imagePullSecretData))

			By("Verifying idempotency - second reconciliation succeeds without errors")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: serviceAccountNN,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When a namespace exclusion changes", func() {
		ctx := context.Background()
		cfg, err := config.NewConfig(
			config.WithDockerConfigJSON(imagePullSecretData),
			config.WithSecretNamespace(kubeSystemNs),
		)
		if err != nil {
			panic(err)
		}

		newFakeClient := func(objs ...client.Object) client.Client {
			scheme := kruntime.NewScheme()
			Expect(clientgoscheme.AddToScheme(scheme)).NotTo(HaveOccurred())
			return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
		}

		namespaceNamed := func(name string, annotations map[string]string) *corev1.Namespace {
			return &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations},
			}
		}

		serviceAccountIn := func(namespace string, name string) *corev1.ServiceAccount {
			return &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			}
		}

		requestFor := func(namespace string, name string) reconcile.Request {
			return reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
			}
		}

		Describe("namespaceToServiceAccounts", func() {
			It("returns exactly one request for the default ServiceAccount in a managed namespace", func() {
				nsName := "mapns-managed"
				c := newFakeClient()
				reconciler := &ServiceAccountReconciler{Client: c, Scheme: c.Scheme(), Config: cfg}

				requests := reconciler.namespaceToServiceAccounts(ctx, namespaceNamed(nsName, nil))

				Expect(requests).To(ConsistOf(requestFor(nsName, saDefault)))
			})

			It("returns no requests for an excluded namespace", func() {
				c := newFakeClient()
				reconciler := &ServiceAccountReconciler{Client: c, Scheme: c.Scheme(), Config: cfg}
				excludedNs := namespaceNamed("mapns-excluded", map[string]string{
					cfg.ExcludeAnnotation: annotationTrue,
				})

				Expect(reconciler.namespaceToServiceAccounts(ctx, excludedNs)).To(BeEmpty())
			})

			It("expands glob entries to matching ServiceAccounts and de-duplicates requests", func() {
				nsName := "mapns-glob"
				globCfg, err := config.NewConfig(
					config.WithDockerConfigJSON(imagePullSecretData),
					config.WithSecretNamespace(kubeSystemNs),
					config.WithServiceAccounts("build*,default"),
				)
				Expect(err).NotTo(HaveOccurred())

				c := newFakeClient(
					serviceAccountIn(nsName, "build-a"),
					serviceAccountIn(nsName, "build-b"),
					serviceAccountIn(nsName, "other"),
				)
				reconciler := &ServiceAccountReconciler{Client: c, Scheme: c.Scheme(), Config: globCfg}

				requests := reconciler.namespaceToServiceAccounts(ctx, namespaceNamed(nsName, nil))

				Expect(requests).To(ConsistOf(
					requestFor(nsName, "build-a"),
					requestFor(nsName, "build-b"),
					requestFor(nsName, saDefault),
				))
			})
		})

		Describe("namespaceTransitionPredicate", func() {
			It("only passes updates that change the exclusion state", func() {
				pred := namespaceTransitionPredicate(cfg)
				included := namespaceNamed("transns", nil)
				excluded := namespaceNamed("transns", map[string]string{
					cfg.ExcludeAnnotation: annotationTrue,
				})
				relabeled := namespaceNamed("transns", nil)
				relabeled.Labels = map[string]string{"team": "platform"}

				By("passing updates that add the exclude annotation")
				Expect(pred.Update(event.UpdateEvent{ObjectOld: included, ObjectNew: excluded})).To(BeTrue())

				By("passing updates that remove the exclude annotation")
				Expect(pred.Update(event.UpdateEvent{ObjectOld: excluded, ObjectNew: included})).To(BeTrue())

				By("dropping unrelated label churn with unchanged exclusion state")
				Expect(pred.Update(event.UpdateEvent{ObjectOld: included, ObjectNew: relabeled})).To(BeFalse())

				By("dropping create events, as ServiceAccount creates already cover new namespaces")
				Expect(pred.Create(event.CreateEvent{Object: included})).To(BeFalse())
			})
		})

		It("reconciles the default ServiceAccount once the exclude annotation is removed", func() {
			nsName := "mapns-unexcluded"
			excludedNs := namespaceNamed(nsName, map[string]string{
				cfg.ExcludeAnnotation: annotationTrue,
			})
			c := newFakeClient(excludedNs, serviceAccountIn(nsName, saDefault))
			reconciler := &ServiceAccountReconciler{Client: c, Scheme: c.Scheme(), Config: cfg}

			By("producing no requests while the namespace is still excluded")
			Expect(reconciler.namespaceToServiceAccounts(ctx, excludedNs)).To(BeEmpty())

			By("removing the exclude annotation from the namespace")
			includedNs := excludedNs.DeepCopy()
			includedNs.Annotations = nil
			Expect(c.Update(ctx, includedNs)).To(Succeed())

			By("mapping the now-included namespace to exactly one request")
			requests := reconciler.namespaceToServiceAccounts(ctx, includedNs)
			Expect(requests).To(ConsistOf(requestFor(nsName, saDefault)))

			By("reconciling the mapped request")
			_, err := reconciler.Reconcile(ctx, requests[0])
			Expect(err).NotTo(HaveOccurred())

			By("verifying the imagePullSecret was created with the expected data")
			foundSecret := &corev1.Secret{}
			Expect(c.Get(ctx, types.NamespacedName{Name: cfg.SecretName, Namespace: nsName}, foundSecret)).To(Succeed())
			Expect(string(foundSecret.Data[corev1.DockerConfigJsonKey])).To(Equal(imagePullSecretData))

			By("verifying the ServiceAccount was patched with the imagePullSecret reference")
			foundSA := &corev1.ServiceAccount{}
			Expect(c.Get(ctx, types.NamespacedName{Name: saDefault, Namespace: nsName}, foundSA)).To(Succeed())
			Expect(foundSA.ImagePullSecrets).To(ContainElement(corev1.LocalObjectReference{Name: cfg.SecretName}))
		})
	})
})
