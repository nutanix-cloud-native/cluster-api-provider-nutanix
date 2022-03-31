/*
Copyright 2021.

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

package controllers

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/nutanix-core/cluster-api-provider-nutanix/api/v1beta1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestNutanixClusterReconciler(t *testing.T) {
	g := NewWithT(t)

	var _ = Describe("NutanixClusterReconciler", func() {

		Context("Reconcile an NutanixCluster", func() {
			It("should not error and not requeue the request", func() {
				ctx := context.Background()
				reconciler := &NutanixClusterReconciler{
					Client: k8sClient,
					Scheme: runtime.NewScheme(),
				}

				ntnxCluster := &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}

				// Create the NutanixCluster object and expect the Reconcile to be created
				g.Expect(k8sClient.Create(ctx, ntnxCluster)).To(Succeed())
				defer func() {
					err := k8sClient.Delete(ctx, ntnxCluster)
					Expect(err).NotTo(HaveOccurred())
				}()

				result, err := reconciler.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Namespace: ntnxCluster.Namespace,
						Name:      ntnxCluster.Name,
					},
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.RequeueAfter).To(BeZero())
				g.Expect(result.Requeue).To(BeFalse())
			})
		})
	})
}
