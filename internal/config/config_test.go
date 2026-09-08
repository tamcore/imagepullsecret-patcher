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

package config

import (
	"strings"
	"testing"
)

// testSecretNamespace stands in for the namespace the operator runs in.
const testSecretNamespace = "imagepullsecret-patcher"

func Test_New_Validation(t *testing.T) {
	const credential = `{"auths":{"registry.example.com":{"auth":"c3VwZXJzZWNyZXQ="}}}`

	tests := []struct {
		name        string
		cfg         Config
		wantErr     bool
		forbidInErr []string
	}{
		{
			"valid inline dockerConfigJSON should not error",
			Config{
				SecretNamespace:  testSecretNamespace,
				DockerConfigJSON: credential,
			},
			false,
			nil,
		},
		{
			"neither dockerConfigJSON nor dockerConfigJSONPath should error",
			Config{
				SecretNamespace: testSecretNamespace,
			},
			true,
			nil,
		},
		{
			"both dockerConfigJSON and dockerConfigJSONPath should error without leaking values",
			Config{
				SecretNamespace:      testSecretNamespace,
				DockerConfigJSON:     credential,
				DockerConfigJSONPath: "/path/to/dockerconfig.json",
			},
			true,
			[]string{credential, "c3VwZXJzZWNyZXQ="},
		},
		{
			"invalid JSON in dockerConfigJSON should error without leaking the value",
			Config{
				SecretNamespace:  testSecretNamespace,
				DockerConfigJSON: `{"auths":{"registry.example.com":`,
			},
			true,
			[]string{`{"auths":{"registry.example.com":`},
		},
		{
			"whitespace-only dockerConfigJSON should error",
			Config{
				SecretNamespace:  testSecretNamespace,
				DockerConfigJSON: "   ",
			},
			true,
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			_, err := New(tt.cfg)

			// Assert
			if (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
			for _, forbidden := range tt.forbidInErr {
				if err != nil && strings.Contains(err.Error(), forbidden) {
					t.Errorf("New() error message leaks sensitive value %q: %v", forbidden, err)
				}
			}
		})
	}
}

func Test_New_AppliesDefaults(t *testing.T) {
	// Arrange
	cfg := Config{
		SecretNamespace:  testSecretNamespace,
		DockerConfigJSON: `{"auths":{}}`,
	}

	// Act
	got, err := New(cfg)

	// Assert
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got.SecretName != DefaultSecretName {
		t.Errorf("SecretName = %q, want %q", got.SecretName, DefaultSecretName)
	}
	if got.ExcludedNamespaces != DefaultExcludedNamespaces {
		t.Errorf("ExcludedNamespaces = %q, want %q", got.ExcludedNamespaces, DefaultExcludedNamespaces)
	}
	if got.ExcludeAnnotation != DefaultExcludeAnnotation {
		t.Errorf("ExcludeAnnotation = %q, want %q", got.ExcludeAnnotation, DefaultExcludeAnnotation)
	}
	if got.ServiceAccounts != DefaultServiceAccounts {
		t.Errorf("ServiceAccounts = %q, want %q", got.ServiceAccounts, DefaultServiceAccounts)
	}
	if got.MaxConcurrentReconciles != DefaultMaxConcurrentReconciles {
		t.Errorf("MaxConcurrentReconciles = %d, want %d", got.MaxConcurrentReconciles, DefaultMaxConcurrentReconciles)
	}
}

func Test_FromEnv(t *testing.T) {
	// Arrange
	t.Setenv("CONFIG_SECRETNAME", "custom-secret")
	t.Setenv("CONFIG_DELETE_PODS", "true")
	t.Setenv("CONFIG_MAX_CONCURRENT_RECONCILES", "4")

	// Act
	got := FromEnv()

	// Assert
	if got.SecretName != "custom-secret" {
		t.Errorf("SecretName = %q, want %q", got.SecretName, "custom-secret")
	}
	if !got.FeatureDeletePods {
		t.Error("FeatureDeletePods = false, want true")
	}
	if got.MaxConcurrentReconciles != 4 {
		t.Errorf("MaxConcurrentReconciles = %d, want 4", got.MaxConcurrentReconciles)
	}
	if got.ExcludedNamespaces != DefaultExcludedNamespaces {
		t.Errorf("ExcludedNamespaces = %q, want the default %q", got.ExcludedNamespaces, DefaultExcludedNamespaces)
	}
}
