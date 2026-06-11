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
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
	"github.com/tamcore/imagepullsecret-patcher/internal/utils"
)

const (
	// watcherEventBuffer sizes the event channel so reconcile bursts after a
	// file change do not block the watcher loop.
	watcherEventBuffer = 64
	// watcherRetryBackoff is how long to wait before retrying after a failed
	// initial stat of the watched file. Kubelet refreshes mounted files via
	// atomic symlink swaps, which can briefly race with the stat.
	watcherRetryBackoff = 5 * time.Second
)

// DockerConfigWatcher is a manager runnable that polls the configured
// dockerconfigjson file for changes and emits a GenericEvent for every
// managed Secret when it changes, triggering their reconciliation.
type DockerConfigWatcher struct {
	Client client.Client
	Config *config.Config
	Events chan event.GenericEvent
}

// NeedLeaderElection ensures the watcher only runs on the elected leader.
func (w *DockerConfigWatcher) NeedLeaderElection() bool { return true }

// Start implements manager.Runnable. It blocks until the context is
// cancelled, returning nil for a clean shutdown.
func (w *DockerConfigWatcher) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Info("starting dockerconfigjson file watcher", "path", w.Config.DockerConfigJSONPath)

	for {
		if err := utils.WaitUntilFileChanges(ctx, w.Config.DockerConfigJSONPath); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Error(err, "failed to watch dockerconfigjson file, retrying", "path", w.Config.DockerConfigJSONPath)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(watcherRetryBackoff):
			}
			continue
		}

		if stopped := w.notifyManagedSecrets(ctx); stopped {
			return nil
		}
	}
}

// notifyManagedSecrets sends a GenericEvent for every managed Secret. It
// returns true when the context was cancelled while sending events.
func (w *DockerConfigWatcher) notifyManagedSecrets(ctx context.Context) bool {
	logger := log.FromContext(ctx)

	// Fetch managed Secrets using the cached client (which respects the label
	// selector). This avoids listing ALL secrets cluster-wide.
	secretList := &corev1.SecretList{}
	if err := w.Client.List(ctx, secretList, client.MatchingLabels{
		config.LabelManagedBy: config.AnnotationAppName,
	}); err != nil {
		logger.Error(err, "error listing secrets")
		return false
	}

	for _, d := range secretList.Items {
		secret := d
		ns, err := utils.FetchNamespace(ctx, w.Client, secret.GetNamespace())
		if err != nil {
			logger.Error(err, "error fetching namespace", "namespace", secret.GetNamespace())
			continue
		}
		if !utils.IsManagedSecret(w.Config, ns, &secret) {
			continue
		}
		select {
		case w.Events <- event.GenericEvent{Object: &secret}:
		case <-ctx.Done():
			return true
		}
	}

	return false
}
