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

// Package config provides configuration loading and helpers for the
// imagepullsecret-patcher operator.
package config

import (
	"fmt"

	env "github.com/caitlinelfring/go-env-default"
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

// ConfigOption is a functional option for Config.
type ConfigOption func(*Config)

func WithDockerConfigJSON(val string) ConfigOption {
	return func(c *Config) { c.DockerConfigJSON = val }
}
func WithDockerConfigJSONPath(val string) ConfigOption {
	return func(c *Config) { c.DockerConfigJSONPath = val }
}
func WithSecretName(val string) ConfigOption {
	return func(c *Config) { c.SecretName = val }
}
func WithSecretNamespace(val string) ConfigOption {
	return func(c *Config) { c.SecretNamespace = val }
}
func WithExcludedNamespaces(val string) ConfigOption {
	return func(c *Config) { c.ExcludedNamespaces = val }
}
func WithExcludeAnnotation(val string) ConfigOption {
	return func(c *Config) { c.ExcludeAnnotation = val }
}
func WithServiceAccounts(val string) ConfigOption {
	return func(c *Config) { c.ServiceAccounts = val }
}
func WithFeatureDeletePods(val bool) ConfigOption {
	return func(c *Config) { c.FeatureDeletePods = val }
}
func WithFeatureWatchDockerConfigJSONPath(val bool) ConfigOption {
	return func(c *Config) { c.FeatureWatchDockerConfigJSONPath = val }
}
func WithMaxConcurrentReconciles(val int) ConfigOption {
	return func(c *Config) { c.MaxConcurrentReconciles = val }
}

// NewConfig constructs a Config from environment defaults and functional options.
// It returns an error instead of panicking for easier testing and caller handling.
func NewConfig(opts ...ConfigOption) (*Config, error) {
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

	for _, opt := range opts {
		opt(c)
	}

	if c.SecretNamespace == "" {
		operatorNamespace, err := namespace.GetOperatorNamespace()
		if err != nil {
			return nil, fmt.Errorf("failed to detect operator namespace: %w", err)
		}
		c.SecretNamespace = operatorNamespace
	}

	if c.DockerConfigJSON == "" && c.DockerConfigJSONPath == "" {
		return nil, fmt.Errorf("neither CONFIG_DOCKERCONFIGJSON nor CONFIG_DOCKERCONFIGJSONPATH defined")
	}
	if c.DockerConfigJSON != "" && c.DockerConfigJSONPath != "" {
		return nil, fmt.Errorf("cannot specify both CONFIG_DOCKERCONFIGJSON (%s) and CONFIG_DOCKERCONFIGJSONPATH (%s)", c.DockerConfigJSON, c.DockerConfigJSONPath)
	}

	return c, nil
}
