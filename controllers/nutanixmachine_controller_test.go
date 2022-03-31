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

func TestNutanixMachineReconciler(t *testing.T) {
	g := NewWithT(t)

	var _ = Describe("NutanixMachineReconciler", func() {

		Context("Reconcile an NutanixMachine", func() {
			It("should not error or requeue the request", func() {
				By("Calling reconcile")
				reconciler := &NutanixMachineReconciler{
					Client: k8sClient,
					Scheme: runtime.NewScheme(),
				}

				ctx := context.Background()
				ntnxMachine := &infrav1.NutanixMachine{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}
				result, err := reconciler.Reconcile(ctx, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Namespace: ntnxMachine.Namespace,
						Name:      ntnxMachine.Name,
					},
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.RequeueAfter).To(BeZero())
				g.Expect(result.Requeue).To(BeFalse())
			})
		})
	})
}
