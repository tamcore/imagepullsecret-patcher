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

func Test_NewConfig_Validation(t *testing.T) {
	const credential = `{"auths":{"registry.example.com":{"auth":"c3VwZXJzZWNyZXQ="}}}`

	tests := []struct {
		name        string
		opts        []ConfigOption
		wantErr     bool
		forbidInErr []string
	}{
		{
			"valid inline dockerConfigJSON should not error",
			[]ConfigOption{
				WithSecretNamespace("imagepullsecret-patcher"),
				WithDockerConfigJSON(credential),
			},
			false,
			nil,
		},
		{
			"neither dockerConfigJSON nor dockerConfigJSONPath should error",
			[]ConfigOption{
				WithSecretNamespace("imagepullsecret-patcher"),
			},
			true,
			nil,
		},
		{
			"both dockerConfigJSON and dockerConfigJSONPath should error without leaking values",
			[]ConfigOption{
				WithSecretNamespace("imagepullsecret-patcher"),
				WithDockerConfigJSON(credential),
				WithDockerConfigJSONPath("/path/to/dockerconfig.json"),
			},
			true,
			[]string{credential, "c3VwZXJzZWNyZXQ="},
		},
		{
			"invalid JSON in dockerConfigJSON should error without leaking the value",
			[]ConfigOption{
				WithSecretNamespace("imagepullsecret-patcher"),
				WithDockerConfigJSON(`{"auths":{"registry.example.com":`),
			},
			true,
			[]string{`{"auths":{"registry.example.com":`},
		},
		{
			"whitespace-only dockerConfigJSON should error",
			[]ConfigOption{
				WithSecretNamespace("imagepullsecret-patcher"),
				WithDockerConfigJSON("   "),
			},
			true,
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			_, err := NewConfig(tt.opts...)

			// Assert
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			for _, forbidden := range tt.forbidInErr {
				if err != nil && strings.Contains(err.Error(), forbidden) {
					t.Errorf("NewConfig() error message leaks sensitive value %q: %v", forbidden, err)
				}
			}
		})
	}
}
