/*
Copyright 2026.

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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"

	"github.com/tamcore/imagepullsecret-patcher/internal/utils"
)

// emitEvent records an Event, tolerating a nil recorder (unit tests may omit
// one) and a nil object.
func emitEvent(rec record.EventRecorder, obj runtime.Object, eventType, reason, messageFmt string, args ...any) {
	if rec == nil || obj == nil {
		return
	}
	rec.Eventf(obj, eventType, reason, messageFmt, args...)
}

// recordSecretAction emits the Event describing what ReconcileImagePullSecret
// did to the managed secret. It is shared by both reconcilers because either
// can be the one that creates or updates the secret. SecretUnchanged emits
// nothing, so periodic resync stays quiet.
func recordSecretAction(rec record.EventRecorder, secretName string, sec *corev1.Secret, action utils.SecretAction) {
	switch action {
	case utils.SecretCreated:
		emitEvent(rec, sec, corev1.EventTypeNormal, "Created", "Created managed imagePullSecret %q", secretName)
	case utils.SecretUpdated:
		emitEvent(rec, sec, corev1.EventTypeNormal, "Updated", "Updated managed imagePullSecret %q", secretName)
	case utils.SecretAdopted:
		emitEvent(rec, sec, corev1.EventTypeNormal, "Adopted", "Adopted pre-existing imagePullSecret %q", secretName)
	case utils.SecretRecreated:
		emitEvent(rec, sec, corev1.EventTypeWarning, "Recreated",
			"Recreated imagePullSecret %q: pre-existing secret had an incompatible type", secretName)
	}
}
