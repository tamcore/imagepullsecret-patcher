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
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
)

const (
	nsDefault      = "default"
	annotationTrue = "true"
)

var (
	True  = true
	False = false
)

func Test_IsServiceAccountManaged(t *testing.T) {
	type args struct {
		namespace      client.Object
		serviceAccount client.Object
	}
	tests := []struct {
		name                  string
		args                  args
		configServiceAccounts string
		want                  bool
	}{
		{
			"Namespace not excluded. ServiceAccount not excluded. Should be managed = true.",
			args{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: nsDefault,
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nsDefault,
						Namespace: nsDefault,
					},
				},
			},
			"*",
			True,
		},
		{
			"Namespace not excluded. ServiceAccount not excluded, but not configured. Should be unmanaged = false.",
			args{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: nsDefault,
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nsDefault,
						Namespace: nsDefault,
					},
				},
			},
			"global-imagepull-serviceaccount",
			False,
		},
		{
			"Namespace excluded. ServiceAccount not excluded. Should be unmanaged = false.",
			args{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: nsDefault,
						Annotations: map[string]string{
							"pborn.eu/imagepullsecret-patcher-exclude": annotationTrue,
						},
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nsDefault,
						Namespace: nsDefault,
					},
				},
			},
			"*",
			False,
		},
		{
			"Namespace not excluded. ServiceAccount excluded. Should be unmanaged = false.",
			args{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: nsDefault,
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nsDefault,
						Namespace: nsDefault,
						Annotations: map[string]string{
							"pborn.eu/imagepullsecret-patcher-exclude": annotationTrue,
						},
					},
				},
			},
			"*",
			False,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.NewConfig(
				config.WithDockerConfigJSON(`{"auths":{}}`),
				config.WithSecretNamespace("kube-system"),
				config.WithServiceAccounts(tt.configServiceAccounts),
			)
			if err != nil {
				t.Fatalf("failed to create config: %v", err)
			}
			// config.ServiceAccounts = tt.configServiceAccounts

			if got := IsServiceAccountManaged(cfg, tt.args.namespace, tt.args.serviceAccount); got != tt.want {
				t.Errorf("IsServiceAccountManaged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_IsManagedSecret(t *testing.T) {
	cfg, err := config.NewConfig(
		config.WithDockerConfigJSON(`{"auths":{}}`),
		config.WithSecretNamespace("kube-system"),
	)
	if err != nil {
		t.Fatalf("failed to create config: %v", err)
	}
	type args struct {
		namespace client.Object
		secret    client.Object
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"Namespace not excluded. Secret has required annotations. Should be managed = true.",
			args{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: nsDefault,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nsDefault,
						Namespace: nsDefault,
						Annotations: map[string]string{
							config.AnnotationManagedBy: config.AnnotationAppName,
						},
					},
				},
			},
			True,
		},
		{
			"Namespace not excluded. Secret does not have required annotations. Should be unmanaged = false.",
			args{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: nsDefault,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: nsDefault,
					},
				},
			},
			False,
		},
		{
			"Namespace not excluded. Secret is our source of truth. Should be unmanaged = false.",
			args{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: nsDefault,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      cfg.SecretName,
						Namespace: cfg.SecretNamespace,
					},
				},
			},
			False,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsManagedSecret(cfg, tt.args.namespace, tt.args.secret); got != tt.want {
				t.Errorf("IsManagedSecret() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_HasAnnotation(t *testing.T) {
	tests := []struct {
		name            string
		object          client.Object
		annotationKey   string
		annotationValue string
		want            bool
	}{
		{
			"No annotations present. Should be false.",
			&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsDefault,
				},
			},
			"foo",
			"bar",
			False,
		},
		{
			"Desired annotation present. Should be true.",
			&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsDefault,
					Annotations: map[string]string{
						config.AnnotationManagedBy: config.AnnotationAppName,
					},
				},
			},
			config.AnnotationManagedBy,
			config.AnnotationAppName,
			True,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAnnotation(tt.object, tt.annotationKey, tt.annotationValue); got != tt.want {
				t.Errorf("HasAnnotation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func newCleanupTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.NewConfig(
		config.WithDockerConfigJSON(`{"auths":{}}`),
		config.WithSecretNamespace("kube-system"),
	)
	if err != nil {
		t.Fatalf("failed to create config: %v", err)
	}
	return cfg
}

func newFailingPod(name string, namespace string, serviceAccount string, reasons ...string) *corev1.Pod {
	statuses := make([]corev1.ContainerStatus, 0, len(reasons))
	for _, reason := range reasons {
		statuses = append(statuses, corev1.ContainerStatus{
			State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{Reason: reason},
			},
		})
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.PodSpec{ServiceAccountName: serviceAccount},
		Status:     corev1.PodStatus{ContainerStatuses: statuses},
	}
}

func newCountingClient(deletes *int, saGets *int, deleteNotFound bool, objs ...client.Object) client.Client {
	scheme := kruntime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				*deletes++
				if deleteNotFound {
					return apierrs.NewNotFound(schema.GroupResource{Resource: "pods"}, obj.GetName())
				}
				return c.Delete(ctx, obj, opts...)
			},
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.ServiceAccount); ok {
					*saGets++
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()
}

func Test_CleanupPodsForNamespace(t *testing.T) {
	managedNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsDefault}}
	managedSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: nsDefault, Namespace: nsDefault}}
	unmanagedSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: nsDefault}}

	tests := []struct {
		name           string
		objects        []client.Object
		deleteNotFound bool
		wantDeletes    int
		maxSAGets      int
		wantErr        bool
	}{
		{
			"pod with two failing containers is deleted exactly once",
			[]client.Object{
				managedNamespace.DeepCopy(), managedSA.DeepCopy(),
				newFailingPod("pod-a", nsDefault, nsDefault, "ErrImagePull", "ImagePullBackOff"),
			},
			false, 1, 1, false,
		},
		{
			"pods sharing a serviceaccount fetch it only once",
			[]client.Object{
				managedNamespace.DeepCopy(), managedSA.DeepCopy(),
				newFailingPod("pod-a", nsDefault, nsDefault, "ErrImagePull"),
				newFailingPod("pod-b", nsDefault, nsDefault, "ImagePullBackOff"),
			},
			false, 2, 1, false,
		},
		{
			"pod of unmanaged serviceaccount is kept",
			[]client.Object{
				managedNamespace.DeepCopy(), managedSA.DeepCopy(), unmanagedSA.DeepCopy(),
				newFailingPod("pod-a", nsDefault, "other", "ErrImagePull"),
			},
			false, 0, 1, false,
		},
		{
			"already-deleted pod is tolerated",
			[]client.Object{
				managedNamespace.DeepCopy(), managedSA.DeepCopy(),
				newFailingPod("pod-a", nsDefault, nsDefault, "ErrImagePull"),
			},
			true, 1, 1, false,
		},
		{
			"healthy pods are kept",
			[]client.Object{
				managedNamespace.DeepCopy(), managedSA.DeepCopy(),
				newFailingPod("pod-a", nsDefault, nsDefault),
			},
			false, 0, 1, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			cfg := newCleanupTestConfig(t)
			deletes, saGets := 0, 0
			c := newCountingClient(&deletes, &saGets, tt.deleteNotFound, tt.objects...)

			// Act
			err := CleanupPodsForNamespace(context.Background(), cfg, c, nsDefault)

			// Assert
			if (err != nil) != tt.wantErr {
				t.Fatalf("CleanupPodsForNamespace() error = %v, wantErr %v", err, tt.wantErr)
			}
			if deletes != tt.wantDeletes {
				t.Errorf("CleanupPodsForNamespace() deletes = %d, want %d", deletes, tt.wantDeletes)
			}
			if saGets > tt.maxSAGets {
				t.Errorf("CleanupPodsForNamespace() serviceaccount gets = %d, want at most %d", saGets, tt.maxSAGets)
			}
		})
	}

	t.Run("excluded namespace deletes nothing", func(t *testing.T) {
		// Arrange
		cfg := newCleanupTestConfig(t)
		excludedNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:        "kube-excluded",
			Annotations: map[string]string{cfg.ExcludeAnnotation: annotationTrue},
		}}
		deletes, saGets := 0, 0
		c := newCountingClient(&deletes, &saGets, false,
			excludedNamespace,
			&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: nsDefault, Namespace: "kube-excluded"}},
			newFailingPod("pod-a", "kube-excluded", nsDefault, "ErrImagePull"),
		)

		// Act
		err := CleanupPodsForNamespace(context.Background(), cfg, c, "kube-excluded")

		// Assert
		if err != nil {
			t.Fatalf("CleanupPodsForNamespace() unexpected error: %v", err)
		}
		if deletes != 0 {
			t.Errorf("CleanupPodsForNamespace() deletes = %d, want 0", deletes)
		}
	})
}

func Test_CleanupPodsForSA(t *testing.T) {
	tests := []struct {
		name           string
		objects        []client.Object
		deleteNotFound bool
		wantDeletes    int
	}{
		{
			"pod with two failing containers is deleted exactly once",
			[]client.Object{newFailingPod("pod-a", nsDefault, nsDefault, "ErrImagePull", "ImagePullBackOff")},
			false, 1,
		},
		{
			"pod of other serviceaccount is kept",
			[]client.Object{newFailingPod("pod-a", nsDefault, "other", "ErrImagePull")},
			false, 0,
		},
		{
			"already-deleted pod is tolerated",
			[]client.Object{newFailingPod("pod-a", nsDefault, nsDefault, "ErrImagePull")},
			true, 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			deletes, saGets := 0, 0
			c := newCountingClient(&deletes, &saGets, tt.deleteNotFound, tt.objects...)

			// Act
			err := CleanupPodsForSA(context.Background(), c, nsDefault, nsDefault)

			// Assert
			if err != nil {
				t.Fatalf("CleanupPodsForSA() unexpected error: %v", err)
			}
			if deletes != tt.wantDeletes {
				t.Errorf("CleanupPodsForSA() deletes = %d, want %d", deletes, tt.wantDeletes)
			}
		})
	}
}

func Test_GetDockerConfigJSON(t *testing.T) {
	const validJSON = `{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`
	const invalidJSON = `{"auths":{"registry.example.com":`

	writeTempFile := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "dockerconfig.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write temp file: %v", err)
		}
		return path
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T) []config.ConfigOption
		want    string
		wantErr bool
	}{
		{
			"valid inline JSON is returned as-is",
			func(t *testing.T) []config.ConfigOption {
				return []config.ConfigOption{config.WithDockerConfigJSON(validJSON)}
			},
			validJSON,
			false,
		},
		{
			"valid JSON from file is returned as-is",
			func(t *testing.T) []config.ConfigOption {
				return []config.ConfigOption{config.WithDockerConfigJSONPath(writeTempFile(t, validJSON))}
			},
			validJSON,
			false,
		},
		{
			"invalid JSON from file errors without leaking content",
			func(t *testing.T) []config.ConfigOption {
				return []config.ConfigOption{config.WithDockerConfigJSONPath(writeTempFile(t, invalidJSON))}
			},
			"",
			true,
		},
		{
			"empty file errors",
			func(t *testing.T) []config.ConfigOption {
				return []config.ConfigOption{config.WithDockerConfigJSONPath(writeTempFile(t, ""))}
			},
			"",
			true,
		},
		{
			"missing file errors",
			func(t *testing.T) []config.ConfigOption {
				return []config.ConfigOption{config.WithDockerConfigJSONPath(filepath.Join(t.TempDir(), "nonexistent.json"))}
			},
			"",
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			cfg := &config.Config{SecretNamespace: "kube-system"}
			for _, opt := range tt.setup(t) {
				opt(cfg)
			}

			// Act
			got, err := GetDockerConfigJSON(cfg)

			// Assert
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetDockerConfigJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), invalidJSON) {
				t.Errorf("GetDockerConfigJSON() error message leaks file content: %v", err)
			}
			if got != tt.want {
				t.Errorf("GetDockerConfigJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}
