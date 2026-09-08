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
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

const (
	AnnotationManagedBy = "app.kubernetes.io/managed-by"
	AnnotationAppName   = "imagepullsecret-patcher"
	// LabelManagedBy is used for cache selector filtering to reduce API server load
	LabelManagedBy = "app.kubernetes.io/managed-by"
)

// Defaults applied to fields the caller leaves empty.
const (
	DefaultSecretName              = "global-imagepullsecret"
	DefaultExcludedNamespaces      = "kube-*"
	DefaultExcludeAnnotation       = "pborn.eu/imagepullsecret-patcher-exclude"
	DefaultServiceAccounts         = "default"
	DefaultMaxConcurrentReconciles = 1
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
}

// FromEnv returns the configuration read from the CONFIG_* environment
// variables, falling back to the defaults above. Callers use it to seed
// command-line flag defaults, so a flag always overrides the environment.
func FromEnv() Config {
	maxConcurrentReconciles, err := strconv.Atoi(os.Getenv("CONFIG_MAX_CONCURRENT_RECONCILES"))
	if err != nil {
		maxConcurrentReconciles = DefaultMaxConcurrentReconciles
	}
	deletePods, _ := strconv.ParseBool(os.Getenv("CONFIG_DELETE_PODS"))
	watchDockerConfigJSONPath, _ := strconv.ParseBool(os.Getenv("CONFIG_WATCH_DOCKERCONFIGJSONPATH"))

	return Config{
		DockerConfigJSON:                 os.Getenv("CONFIG_DOCKERCONFIGJSON"),
		DockerConfigJSONPath:             os.Getenv("CONFIG_DOCKERCONFIGJSONPATH"),
		SecretName:                       cmp.Or(os.Getenv("CONFIG_SECRETNAME"), DefaultSecretName),
		SecretNamespace:                  os.Getenv("CONFIG_SECRET_NAMESPACE"),
		ExcludedNamespaces:               cmp.Or(os.Getenv("CONFIG_EXCLUDED_NAMESPACES"), DefaultExcludedNamespaces),
		ExcludeAnnotation:                cmp.Or(os.Getenv("CONFIG_EXCLUDE_ANNOTATION"), DefaultExcludeAnnotation),
		ServiceAccounts:                  cmp.Or(os.Getenv("CONFIG_SERVICEACCOUNTS"), DefaultServiceAccounts),
		FeatureDeletePods:                deletePods,
		FeatureWatchDockerConfigJSONPath: watchDockerConfigJSONPath,
		MaxConcurrentReconciles:          maxConcurrentReconciles,
	}
}

// New validates c, fills empty fields with their defaults and detects the
// operator namespace when SecretNamespace is unset. Error messages never
// include credential values.
func New(c Config) (*Config, error) {
	c.SecretName = cmp.Or(c.SecretName, DefaultSecretName)
	c.ExcludedNamespaces = cmp.Or(c.ExcludedNamespaces, DefaultExcludedNamespaces)
	c.ExcludeAnnotation = cmp.Or(c.ExcludeAnnotation, DefaultExcludeAnnotation)
	c.ServiceAccounts = cmp.Or(c.ServiceAccounts, DefaultServiceAccounts)
	c.MaxConcurrentReconciles = cmp.Or(c.MaxConcurrentReconciles, DefaultMaxConcurrentReconciles)

	if c.SecretNamespace == "" {
		operatorNamespace, err := operatorNamespace()
		if err != nil {
			return nil, fmt.Errorf("failed to detect operator namespace: %w", err)
		}
		c.SecretNamespace = operatorNamespace
	}

	switch {
	case c.DockerConfigJSON == "" && c.DockerConfigJSONPath == "":
		return nil, fmt.Errorf("neither CONFIG_DOCKERCONFIGJSON nor CONFIG_DOCKERCONFIGJSONPATH defined")
	case c.DockerConfigJSON != "" && c.DockerConfigJSONPath != "":
		return nil, fmt.Errorf("cannot specify both CONFIG_DOCKERCONFIGJSON and CONFIG_DOCKERCONFIGJSONPATH")
	case c.DockerConfigJSON != "" && !json.Valid([]byte(c.DockerConfigJSON)):
		return nil, fmt.Errorf("CONFIG_DOCKERCONFIGJSON does not contain valid JSON")
	}

	return &c, nil
}
