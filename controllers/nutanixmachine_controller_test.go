/*
Copyright 2022 Nutanix

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
	"errors"
	"testing"

	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"github.com/nutanix-cloud-native/prism-go-client/v3/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockctlclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/ctlclient"
	mockmeta "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sapimachinery"
	mocknutanixv3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/nutanix"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
)

func TestNutanixMachineReconciler(t *testing.T) {
	g := NewWithT(t)

	_ = Describe("NutanixMachineReconciler", func() {
		var (
			reconciler  *NutanixMachineReconciler
			ctx         context.Context
			ntnxMachine *infrav1.NutanixMachine
			machine     *capiv1.Machine
			ntnxCluster *infrav1.NutanixCluster
			r           string
		)

		BeforeEach(func() {
			ctx = context.Background()
			r = util.RandomString(10)
			reconciler = &NutanixMachineReconciler{
				Client: k8sClient,
				Scheme: runtime.NewScheme(),
			}

			ntnxMachine = &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: infrav1.NutanixMachineSpec{
					VCPUsPerSocket: int32(minVCPUsPerSocket),
					MemorySize:     minMachineMemorySize,
					SystemDiskSize: minMachineSystemDiskSize,
					VCPUSockets:    int32(minVCPUSockets),
				},
			}
			machine = &capiv1.Machine{ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			}}

			ntnxCluster = &infrav1.NutanixCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: infrav1.NutanixClusterSpec{
					PrismCentral: &credentialTypes.NutanixPrismEndpoint{
						// Adding port info to override default value (0)
						Port: 9440,
					},
				},
			}
		})

		Context("Reconcile an NutanixMachine", func() {
			It("should not error or requeue the request", func() {
				By("Calling reconcile")
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

		Context("Validates machine config", func() {
			It("should error if no failure domain is present on machine and no subnets are passed", func() {
				err := reconciler.validateMachineConfig(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
				})
				g.Expect(err).To(HaveOccurred())
			})
			It("should error if no failure domain is present on machine and no cluster name is passed", func() {
				ntnxMachine.Spec.Subnets = []infrav1.NutanixResourceIdentifier{
					{
						Type: infrav1.NutanixIdentifierName,
						Name: &r,
					},
				}
				err := reconciler.validateMachineConfig(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
				})
				g.Expect(err).To(HaveOccurred())
			})
			It("returns no error if valid machine config is passed without failure domain", func() {
				ntnxMachine.Spec.Subnets = []infrav1.NutanixResourceIdentifier{
					{
						Type: infrav1.NutanixIdentifierName,
						Name: &r,
					},
				}
				ntnxMachine.Spec.Cluster = infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &r,
				}
				err := reconciler.validateMachineConfig(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
				})
				g.Expect(err).ToNot(HaveOccurred())
			})
			It("returns no error if valid machine config is passed with failure domain", func() {
				machine.Spec.FailureDomain = &r
				err := reconciler.validateMachineConfig(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
				})
				g.Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("Gets the subnet and PE UUIDs", func() {
			It("should error if nil machine context is passed", func() {
				_, _, err := reconciler.GetSubnetAndPEUUIDs(nil)
				g.Expect(err).To(HaveOccurred())
			})

			It("should error if machine has no failure domain and Prism Element info is missing on nutanix machine", func() {
				_, _, err := reconciler.GetSubnetAndPEUUIDs(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				})
				g.Expect(err).To(HaveOccurred())
			})
			It("should error if machine has no failure domain and subnet info is missing on nutanix machine", func() {
				ntnxMachine.Spec.Cluster = infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &r,
				}
				_, _, err := reconciler.GetSubnetAndPEUUIDs(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				})
				g.Expect(err).To(HaveOccurred())
			})
			It("should error if machine has no failure domain and nutanixClient is nil", func() {
				ntnxMachine.Spec.Cluster = infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &r,
				}
				ntnxMachine.Spec.Subnets = []infrav1.NutanixResourceIdentifier{
					{
						Type: infrav1.NutanixIdentifierName,
						Name: &r,
					},
				}
				_, _, err := reconciler.GetSubnetAndPEUUIDs(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				})
				g.Expect(err).To(HaveOccurred())
			})
			It("should error if machine has failure domain and but it is missing on nutanixCluster object", func() {
				machine.Spec.FailureDomain = &r

				_, _, err := reconciler.GetSubnetAndPEUUIDs(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				})
				g.Expect(err).To(HaveOccurred())
			})
			It("should error if machine and nutanixCluster have failure domain and but nutanixClient is nil", func() {
				machine.Spec.FailureDomain = &r
				ntnxCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomain{
					{
						Name: r,
					},
				}
				_, _, err := reconciler.GetSubnetAndPEUUIDs(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				})
				g.Expect(err).To(HaveOccurred())
			})
		})

		Context("Detaches a volume group on deletion", func() {
			It("should error if get prism central returns error", func() {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().GetPrismCentral(gomock.Any()).Return(nil, errors.New("error"))

				v3Client := &prismclientv3.Client{
					V3: mockv3Service,
				}
				err := reconciler.detachVolumeGroups(&nctx.MachineContext{
					NutanixClient: v3Client,
				},
					"",
					ntnxMachine.Status.VmUUID,
					[]*prismclientv3.VMDisk{},
				)
				g.Expect(err).To(HaveOccurred())
			})

			It("should error if prism central version is empty", func() {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().GetPrismCentral(gomock.Any()).Return(&models.PrismCentral{Resources: &models.PrismCentralResources{
					Version: ptr.To(""),
				}}, nil)

				v3Client := &prismclientv3.Client{
					V3: mockv3Service,
				}
				err := reconciler.detachVolumeGroups(&nctx.MachineContext{
					NutanixClient: v3Client,
				}, "",
					ntnxMachine.Status.VmUUID,
					[]*prismclientv3.VMDisk{},
				)
				g.Expect(err).To(HaveOccurred())
			})

			It("should error if prism central version value is absent", func() {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().GetPrismCentral(gomock.Any()).Return(&models.PrismCentral{Resources: &models.PrismCentralResources{
					Version: ptr.To("pc."),
				}}, nil)

				v3Client := &prismclientv3.Client{
					V3: mockv3Service,
				}
				err := reconciler.detachVolumeGroups(&nctx.MachineContext{
					NutanixClient: v3Client,
				}, "",
					ntnxMachine.Status.VmUUID,
					[]*prismclientv3.VMDisk{},
				)
				g.Expect(err).To(HaveOccurred())
			})

			It("should error if prism central version is invalid", func() {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().GetPrismCentral(gomock.Any()).Return(&models.PrismCentral{Resources: &models.PrismCentralResources{
					Version: ptr.To("not.a.valid.version"),
				}}, nil)

				v3Client := &prismclientv3.Client{
					V3: mockv3Service,
				}
				err := reconciler.detachVolumeGroups(&nctx.MachineContext{
					NutanixClient: v3Client,
				}, "",
					ntnxMachine.Status.VmUUID,
					[]*prismclientv3.VMDisk{},
				)
				g.Expect(err).To(HaveOccurred())
			})

			It("should not error if prism central is not v4 compatible", func() {
				mockctrl := gomock.NewController(t)
				mockv3Service := mocknutanixv3.NewMockService(mockctrl)
				mockv3Service.EXPECT().GetPrismCentral(gomock.Any()).Return(&models.PrismCentral{Resources: &models.PrismCentralResources{
					Version: ptr.To("pc.2023.4.0.1"),
				}}, nil)

				v3Client := &prismclientv3.Client{
					V3: mockv3Service,
				}
				err := reconciler.detachVolumeGroups(&nctx.MachineContext{
					NutanixClient: v3Client,
				}, "",
					ntnxMachine.Status.VmUUID,
					[]*prismclientv3.VMDisk{},
				)
				g.Expect(err).To(Not(HaveOccurred()))
			})
		})
	})
}

func TestNutanixMachineReconciler_SetupWithManager(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()
	log := ctrl.Log.WithName("controller")
	scheme := runtime.NewScheme()
	err := infrav1.AddToScheme(scheme)
	require.NoError(t, err)
	err = capiv1.AddToScheme(scheme)
	require.NoError(t, err)

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(config.Controller{MaxConcurrentReconciles: 1}).AnyTimes()
	mgr.EXPECT().GetLogger().Return(log).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(nil).AnyTimes()

	restScope := mockmeta.NewMockRESTScope(mockCtrl)
	restScope.EXPECT().Name().Return(meta.RESTScopeNameNamespace).AnyTimes()

	restMapper := mockmeta.NewMockRESTMapper(mockCtrl)
	restMapper.EXPECT().RESTMapping(gomock.Any()).Return(&meta.RESTMapping{Scope: restScope}, nil).AnyTimes()

	mockClient := mockctlclient.NewMockClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockClient.EXPECT().RESTMapper().Return(restMapper).AnyTimes()

	reconciler := &NutanixMachineReconciler{
		Client: mockClient,
		Scheme: scheme,
		controllerConfig: &ControllerConfig{
			MaxConcurrentReconciles: 1,
		},
	}

	err = reconciler.SetupWithManager(ctx, mgr)
	assert.NoError(t, err)
}

func TestNutanixMachineReconciler_SetupWithManager_BuildError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()
	log := ctrl.Log.WithName("controller")
	scheme := runtime.NewScheme()
	err := infrav1.AddToScheme(scheme)
	require.NoError(t, err)
	err = capiv1.AddToScheme(scheme)
	require.NoError(t, err)

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(config.Controller{MaxConcurrentReconciles: 1}).AnyTimes()
	mgr.EXPECT().GetLogger().Return(log).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(errors.New("error")).AnyTimes()

	restScope := mockmeta.NewMockRESTScope(mockCtrl)
	restScope.EXPECT().Name().Return(meta.RESTScopeNameNamespace).AnyTimes()

	restMapper := mockmeta.NewMockRESTMapper(mockCtrl)
	restMapper.EXPECT().RESTMapping(gomock.Any()).Return(&meta.RESTMapping{Scope: restScope}, nil).AnyTimes()

	mockClient := mockctlclient.NewMockClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockClient.EXPECT().RESTMapper().Return(restMapper).AnyTimes()

	reconciler := &NutanixMachineReconciler{
		Client: mockClient,
		Scheme: scheme,
		controllerConfig: &ControllerConfig{
			MaxConcurrentReconciles: 1,
		},
	}

	err = reconciler.SetupWithManager(ctx, mgr)
	assert.Error(t, err)
}

func TestNutanixMachineReconciler_SetupWithManager_ClusterToTypedObjectsMapperError(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	ctx := context.Background()
	log := ctrl.Log.WithName("controller")
	scheme := runtime.NewScheme()
	err := infrav1.AddToScheme(scheme)
	require.NoError(t, err)
	err = capiv1.AddToScheme(scheme)
	require.NoError(t, err)

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetControllerOptions().Return(config.Controller{MaxConcurrentReconciles: 1}).AnyTimes()
	mgr.EXPECT().GetLogger().Return(log).AnyTimes()
	mgr.EXPECT().Add(gomock.Any()).Return(nil).AnyTimes()

	restScope := mockmeta.NewMockRESTScope(mockCtrl)
	restScope.EXPECT().Name().Return(meta.RESTScopeName("")).AnyTimes()

	restMapper := mockmeta.NewMockRESTMapper(mockCtrl)
	restMapper.EXPECT().RESTMapping(gomock.Any()).Return(&meta.RESTMapping{Scope: restScope}, nil).AnyTimes()

	mockClient := mockctlclient.NewMockClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockClient.EXPECT().RESTMapper().Return(restMapper).AnyTimes()

	reconciler := &NutanixMachineReconciler{
		Client: mockClient,
		Scheme: scheme,
		controllerConfig: &ControllerConfig{
			MaxConcurrentReconciles: 1,
		},
	}

	err = reconciler.SetupWithManager(ctx, mgr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create mapper for Cluster to NutanixMachine")
}
