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
	"fmt"

	"github.com/caitlinelfring/go-env-default"
	"github.com/tamcore/imagepullsecret-patcher/internal/namespace"
)

const (
	AnnotationManagedBy = "app.kubernetes.io/managed-by"
	AnnotationAppName   = "imagepullsecret-patcher"
)

type Config struct {
	DockerConfigJSON                 string
	DockerConfigJSONPath             string
	SecretName                       string
	SecretNamespace                  string
	ExcludedNamespaces               string
	ExcludeAnnotation                string
	ServiceAccounts                  string
	FeatureDeletePods                bool
	FeatureWatchDockerConfigJSONPath bool
	MaxConcurrentReconciles          int
	AnnotationManagedBy              string
	AnnotationAppName                string
}

func NewConfig(options ...Config) *Config {
	c := &Config{
		DockerConfigJSON:                 env.GetDefault("CONFIG_DOCKERCONFIGJSON", ""),
		DockerConfigJSONPath:             env.GetDefault("CONFIG_DOCKERCONFIGJSONPATH", ""),
		SecretName:                       env.GetDefault("CONFIG_SECRETNAME", "global-imagepullsecret"),
		SecretNamespace:                  env.GetDefault("CONFIG_SECRET_NAMESPACE", ""),
		ExcludedNamespaces:               env.GetDefault("CONFIG_EXCLUDED_NAMESPACES", "kube-*"),
		ExcludeAnnotation:                env.GetDefault("CONFIG_EXCLUDE_ANNOTATION", "pborn.eu/imagepullsecret-patcher-exclude"),
		ServiceAccounts:                  env.GetDefault("CONFIG_SERVICEACCOUNTS", "default"),
		AnnotationManagedBy:              AnnotationManagedBy,
		AnnotationAppName:                AnnotationAppName,
		FeatureDeletePods:                env.GetBoolDefault("CONFIG_DELETE_PODS", false),
		FeatureWatchDockerConfigJSONPath: env.GetBoolDefault("CONFIG_WATCH_DOCKERCONFIGJSONPATH", false),
		MaxConcurrentReconciles:          env.GetIntDefault("CONFIG_MAX_CONCURRENT_RECONCILES", 1),
	}

	for _, opt := range options {
		if opt.FeatureDeletePods {
			c.FeatureDeletePods = opt.FeatureDeletePods
		}
		if opt.FeatureWatchDockerConfigJSONPath {
			c.FeatureWatchDockerConfigJSONPath = opt.FeatureWatchDockerConfigJSONPath
		}
		if opt.DockerConfigJSON != "" {
			c.DockerConfigJSON = opt.DockerConfigJSON
		}
		if opt.DockerConfigJSONPath != "" {
			c.DockerConfigJSONPath = opt.DockerConfigJSONPath
		}
		if opt.SecretName != "" {
			c.SecretName = opt.SecretName
		}
		if opt.SecretNamespace != "" {
			c.SecretNamespace = opt.SecretNamespace
		}
		if opt.ExcludedNamespaces != "" {
			c.ExcludedNamespaces = opt.ExcludedNamespaces
		}
		if opt.ExcludeAnnotation != "" {
			c.ExcludeAnnotation = opt.ExcludeAnnotation
		}
		if opt.ServiceAccounts != "" {
			c.ServiceAccounts = opt.ServiceAccounts
		}
		if opt.MaxConcurrentReconciles != 0 {
			c.MaxConcurrentReconciles = opt.MaxConcurrentReconciles
		}
	}

	if c.SecretNamespace == "" {
		operatorNamespace, err := namespace.GetOperatorNamespace()
		if err != nil {
			panic(err)
		}
		c.SecretNamespace = operatorNamespace
	}

	if c.DockerConfigJSON == "" && c.DockerConfigJSONPath == "" {
		panic("Neither `CONFIG_DOCKERCONFIGJSON or `CONFIG_DOCKERCONFIGJSONPATH defined.")
	}
	if c.DockerConfigJSON != "" && c.DockerConfigJSONPath != "" {
		panic(fmt.Sprintf("Cannot specify both `CONFIG_DOCKERCONFIGJSON` (%s) and `CONFIG_DOCKERCONFIGJSONPATH` (%s)", c.DockerConfigJSON, c.DockerConfigJSONPath))
	}

	return c
}
