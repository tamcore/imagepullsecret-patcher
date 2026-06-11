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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	saDefault        = "default"
	annotationTrue   = "true"
	secretNsManaged  = "secretns-managed"
	secretNsExcluded = "secretns-excluded"
)

var _ = Describe("Secret Controller", func() {
	ctx := context.Background()
	cfg, err := config.NewConfig(
		config.WithDockerConfigJSON(imagePullSecretData),
		config.WithSecretNamespace("kube-system"),
	)
	if err != nil {
		panic(err)
	}

	newTestScheme := func() *kruntime.Scheme {
		scheme := kruntime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).NotTo(HaveOccurred())
		return scheme
	}

	Context("When reconciling a Secret", func() {
		It("should recreate the managed secret in a managed namespace", func() {
			scheme := newTestScheme()
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: secretNsManaged}},
			).Build()
			reconciler := &SecretReconciler{Client: c, Scheme: scheme, Config: cfg}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: cfg.SecretName, Namespace: secretNsManaged},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			foundSecret := &corev1.Secret{}
			Expect(c.Get(ctx, types.NamespacedName{Name: cfg.SecretName, Namespace: secretNsManaged}, foundSecret)).To(Succeed())
			Expect(string(foundSecret.Data[corev1.DockerConfigJsonKey])).To(Equal(imagePullSecretData))
		})

		It("should skip reconciliation for excluded namespaces", func() {
			scheme := newTestScheme()
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
					Name:        secretNsExcluded,
					Annotations: map[string]string{cfg.ExcludeAnnotation: annotationTrue},
				}},
			).Build()
			reconciler := &SecretReconciler{Client: c, Scheme: scheme, Config: cfg}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: cfg.SecretName, Namespace: secretNsExcluded},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
			foundSecret := &corev1.Secret{}
			Expect(c.Get(ctx, types.NamespacedName{Name: cfg.SecretName, Namespace: secretNsExcluded}, foundSecret)).NotTo(Succeed())
		})

		It("should return cleanly when the namespace no longer exists", func() {
			scheme := newTestScheme()
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler := &SecretReconciler{Client: c, Scheme: scheme, Config: cfg}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: cfg.SecretName, Namespace: "secretns-gone"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should ignore an annotated secret whose name differs from the managed secret", func() {
			scheme := newTestScheme()
			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "secretns-foreign"}}
			foreignSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "something-else",
					Namespace:   namespace.GetName(),
					Annotations: map[string]string{config.AnnotationManagedBy: config.AnnotationAppName},
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{"foo": []byte("bar")},
			}
			foreignSecretNN := types.NamespacedName{
				Name:      foreignSecret.GetName(),
				Namespace: foreignSecret.GetNamespace(),
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(namespace, foreignSecret).Build()
			reconciler := &SecretReconciler{Client: c, Scheme: scheme, Config: cfg}

			By("Snapshotting the foreign secret before reconciliation")
			before := &corev1.Secret{}
			Expect(c.Get(ctx, foreignSecretNN, before)).To(Succeed())

			By("Reconciling a request for the foreign secret")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: foreignSecretNN})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the managed secret was NOT created")
			managed := &corev1.Secret{}
			err = c.Get(ctx, types.NamespacedName{Name: cfg.SecretName, Namespace: namespace.GetName()}, managed)
			Expect(apierrs.IsNotFound(err)).To(BeTrue())

			By("Verifying the foreign secret is untouched")
			after := &corev1.Secret{}
			Expect(c.Get(ctx, foreignSecretNN, after)).To(Succeed())
			Expect(after.ResourceVersion).To(Equal(before.ResourceVersion))
			Expect(after.Data).To(Equal(map[string][]byte{"foo": []byte("bar")}))
			Expect(after.Type).To(Equal(corev1.SecretTypeOpaque))
		})
	})

	Context("When filtering events with managedPredicate", func() {
		managedSecret := func(namespace string) *corev1.Secret {
			return &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        cfg.SecretName,
					Namespace:   namespace,
					Annotations: map[string]string{config.AnnotationManagedBy: config.AnnotationAppName},
				},
			}
		}

		It("should process managed secrets in managed namespaces", func() {
			scheme := newTestScheme()
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "predns-managed"}},
			).Build()
			reconciler := &SecretReconciler{Client: c, Scheme: scheme, Config: cfg}

			pred := reconciler.managedPredicate()

			Expect(pred.Create(event.CreateEvent{Object: managedSecret("predns-managed")})).To(BeTrue())
			Expect(pred.Update(event.UpdateEvent{ObjectNew: managedSecret("predns-managed")})).To(BeTrue())
			Expect(pred.Delete(event.DeleteEvent{Object: managedSecret("predns-managed")})).To(BeTrue())
		})

		It("should drop events in excluded namespaces", func() {
			scheme := newTestScheme()
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
					Name:        "predns-excluded",
					Annotations: map[string]string{cfg.ExcludeAnnotation: annotationTrue},
				}},
			).Build()
			reconciler := &SecretReconciler{Client: c, Scheme: scheme, Config: cfg}

			pred := reconciler.managedPredicate()

			Expect(pred.Create(event.CreateEvent{Object: managedSecret("predns-excluded")})).To(BeFalse())
		})

		It("should fail open when the namespace lookup fails", func() {
			scheme := newTestScheme()
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			reconciler := &SecretReconciler{Client: c, Scheme: scheme, Config: cfg}

			pred := reconciler.managedPredicate()

			Expect(pred.Create(event.CreateEvent{Object: managedSecret("predns-missing")})).To(BeTrue())
			Expect(pred.Delete(event.DeleteEvent{Object: managedSecret("predns-missing")})).To(BeTrue())
		})

		It("should drop delete events for terminating namespaces", func() {
			scheme := newTestScheme()
			now := metav1.Now()
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
					Name:              "predns-terminating",
					DeletionTimestamp: &now,
					Finalizers:        []string{"kubernetes"},
				}},
			).Build()
			reconciler := &SecretReconciler{Client: c, Scheme: scheme, Config: cfg}

			pred := reconciler.managedPredicate()

			Expect(pred.Delete(event.DeleteEvent{Object: managedSecret("predns-terminating")})).To(BeFalse())
		})
	})
})
