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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
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
						Name: "default",
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: "default",
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
						Name: "default",
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: "default",
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
						Name: "default",
						Annotations: map[string]string{
							"pborn.eu/imagepullsecret-patcher-exclude": "true",
						},
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: "default",
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
						Name: "default",
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: "default",
						Annotations: map[string]string{
							"pborn.eu/imagepullsecret-patcher-exclude": "true",
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
			config := config.NewConfig(config.Config{DockerConfigJSON: "xx", SecretNamespace: "kube-system", ServiceAccounts: tt.configServiceAccounts})
			// config.ServiceAccounts = tt.configServiceAccounts

			if got := IsServiceAccountManaged(config, tt.args.namespace, tt.args.serviceAccount); got != tt.want {
				t.Errorf("IsServiceAccountManaged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_IsManagedSecret(t *testing.T) {
	config := config.NewConfig(config.Config{DockerConfigJSON: "xx", SecretNamespace: "kube-system"})
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
						Name: "default",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: "default",
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
						Name: "default",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
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
						Name: "default",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      config.SecretName,
						Namespace: config.SecretNamespace,
					},
				},
			},
			False,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsManagedSecret(config, tt.args.namespace, tt.args.secret); got != tt.want {
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
					Name: "default",
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
					Name: "default",
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
