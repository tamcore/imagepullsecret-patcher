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
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/tamcore/imagepullsecret-patcher/internal/config"
)

const (
	watcherTestNamespace  = "watched-ns"
	watcherTestSecretNs   = "kube-system"
	watcherTestSecretName = "global-imagepullsecret"
	watcherTestTimeout    = 10 * time.Second
	watcherTestPoll       = 200 * time.Millisecond
)

var _ = Describe("DockerConfigWatcher", func() {
	It("requires leader election so only the leader polls the file", func() {
		watcher := &DockerConfigWatcher{}
		Expect(watcher.NeedLeaderElection()).To(BeTrue())
	})

	It("emits events for managed secrets when the watched file changes and stops cleanly on context cancellation", func() {
		By("building a fake client with a namespace and a managed secret")
		scheme := kruntime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: watcherTestNamespace,
			},
		}
		managedSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      watcherTestSecretName,
				Namespace: watcherTestNamespace,
				Labels: map[string]string{
					config.LabelManagedBy: config.AnnotationAppName,
				},
				Annotations: map[string]string{
					config.AnnotationManagedBy: config.AnnotationAppName,
				},
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(namespace, managedSecret).
			Build()

		By("writing a valid dockerconfigjson file to watch")
		tmpfile := filepath.Join(GinkgoT().TempDir(), "dockerconfig.json")
		Expect(os.WriteFile(tmpfile, []byte(`{"auths":{}}`), 0o600)).To(Succeed())

		cfg, err := config.NewConfig(
			config.WithDockerConfigJSONPath(tmpfile),
			config.WithSecretNamespace(watcherTestSecretNs),
		)
		Expect(err).NotTo(HaveOccurred())

		events := make(chan event.GenericEvent, watcherEventBuffer)
		watcher := &DockerConfigWatcher{
			Client: fakeClient,
			Config: cfg,
			Events: events,
		}

		By("starting the watcher with a cancellable context")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		startResult := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			startResult <- watcher.Start(ctx)
		}()

		By("touching the watched file until an event for the managed secret arrives")
		var received event.GenericEvent
		Eventually(func() bool {
			now := time.Now()
			Expect(os.Chtimes(tmpfile, now, now)).To(Succeed())
			select {
			case received = <-events:
				return true
			default:
				return false
			}
		}, watcherTestTimeout, watcherTestPoll).Should(BeTrue())

		Expect(received.Object.GetName()).To(Equal(watcherTestSecretName))
		Expect(received.Object.GetNamespace()).To(Equal(watcherTestNamespace))

		By("cancelling the context and expecting Start to return nil")
		cancel()
		Eventually(startResult, watcherTestTimeout).Should(Receive(BeNil()))
	})
})
