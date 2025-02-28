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
	"fmt"
	"testing"

	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	"github.com/nutanix-cloud-native/prism-go-client/v3/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
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

func TestNutanixMachineValidateDataDisks(t *testing.T) {
	testCases := []struct {
		name        string
		dataDisks   func() []infrav1.NutanixMachineVMDisk
		description string
		stepDesc    string
		errCheck    func(*WithT, error)
	}{
		{
			name: "noErrors",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
				}
			},
			description: "Verify an correct NutanixMachine",
			stepDesc:    "should not error on validation",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).ToNot(HaveOccurred())
			},
		},
		{
			name: "sizeError",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("0Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk size error",
			stepDesc:    "should error on validation due to disk size",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("minimum data disk size"))
			},
		},
		{
			name: "dataSourceUUIDError",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
						DataSource: &infrav1.NutanixResourceIdentifier{
							Type: infrav1.NutanixIdentifierUUID,
							Name: ptr.To("data-source-name"),
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk data source UUID error",
			stepDesc:    "should error on validation due to data source UUID",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("UUID is required for data disk with UUID source"))
			},
		},
		{
			name: "dataSourceNameError",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
						DataSource: &infrav1.NutanixResourceIdentifier{
							Type: infrav1.NutanixIdentifierName,
							UUID: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk data source name error",
			stepDesc:    "should error on validation due to data source name",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("name is required for data disk with name source"))
			},
		},
		{
			name: "storageContainerIDErrorWrongUUID",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("not-an-uuid"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk storage container ID error",
			stepDesc:    "should error on validation due to storage container ID",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("invalid UUID for storage container in data disk"))
			},
		},
		{
			name: "storageContainerIDErrorEmptyID",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To(""),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk storage container ID error",
			stepDesc:    "should error on validation due to storage container ID",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("invalid UUID for storage container in data disk"))
			},
		},
		{
			name: "dataDiskErrorWrongDiskMode",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: "not-standard",
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk disk mode error",
			stepDesc:    "should error on validation due to disk mode",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("invalid disk mode not-standard for data disk"))
			},
		},
		{
			name: "dataDiskErrorWrongDeviceType",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  "not-disk",
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk device type error",
			stepDesc:    "should error on validation due to device type",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("invalid device type not-disk for data disk"))
			},
		},
		{
			name: "dataDiskErrorWrongAdapterType",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: "not-scsi",
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk adapter type error",
			stepDesc:    "should error on validation due to adapter type",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("invalid adapter type not-scsi for data disk"))
			},
		},
		{
			name: "dataDiskErrorWrongDeviceIndex",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
							DeviceIndex: -1,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
				}
			},
			description: "Verify NutanixMachine with data disk device index error",
			stepDesc:    "should error on validation due to device index",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("invalid device index -1 for data disk"))
			},
		},
	}

	for _, testCase := range testCases {
		g := NewWithT(t)

		Describe("NutanixMachineValidateDataDisks", func() {
			var (
				reconciler  *NutanixMachineReconciler
				ctx         context.Context
				ntnxMachine *infrav1.NutanixMachine
				machine     *capiv1.Machine
				ntnxCluster *infrav1.NutanixCluster
				dataDisks   func() []infrav1.NutanixMachineVMDisk
			)

			BeforeEach(func() {
				ctx = context.Background()

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
						Image: &infrav1.NutanixResourceIdentifier{
							Type: infrav1.NutanixIdentifierName,
							Name: ptr.To("image"),
						},
						VCPUsPerSocket: int32(minVCPUsPerSocket),
						MemorySize:     minMachineMemorySize,
						SystemDiskSize: minMachineSystemDiskSize,
						VCPUSockets:    int32(minVCPUSockets),
						DataDisks:      dataDisks(),
						Subnets: []infrav1.NutanixResourceIdentifier{
							{
								Type: infrav1.NutanixIdentifierName,
								Name: ptr.To("blabla"),
							},
						},
						Cluster: infrav1.NutanixResourceIdentifier{
							Type: infrav1.NutanixIdentifierName,
							Name: ptr.To("PE1"),
						},
					},
				}

				machine = &capiv1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test",
						},
					},
					Spec: capiv1.MachineSpec{
						ClusterName: "test",
					},
				}

				ntnxCluster = &infrav1.NutanixCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
					},
					Spec: infrav1.NutanixClusterSpec{
						PrismCentral: &credentialTypes.NutanixPrismEndpoint{
							Address: "prism.central.ntnx",
							Port:    9440,
							CredentialRef: &credentialTypes.NutanixCredentialReference{
								Kind:      credentialTypes.SecretKind,
								Name:      "test",
								Namespace: "default",
							},
						},
					},
				}
			})

			Context(testCase.description, func() {
				dataDisks = testCase.dataDisks

				It(testCase.stepDesc, func() {
					By("Validating machine config")
					err := reconciler.validateMachineConfig(&nctx.MachineContext{
						Context:        ctx,
						NutanixMachine: ntnxMachine,
						Machine:        machine,
						NutanixCluster: ntnxCluster,
					})
					testCase.errCheck(g, err)
				})
			})
		})
	}
}

func TestNutanixClusterReconcilerGetDiskList(t *testing.T) {
	defaultSystemImage := &prismclientv3.ImageIntentResponse{
		Metadata: &prismclientv3.Metadata{
			Kind: ptr.To("image"),
			UUID: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
		},
		Spec: &prismclientv3.Image{
			Name: ptr.To("system_image"),
		},
		Status: &prismclientv3.ImageDefStatus{
			State: ptr.To(""),
		},
	}

	defaultBootstrapImage := &prismclientv3.ImageIntentResponse{
		Metadata: &prismclientv3.Metadata{
			Kind: ptr.To("image"),
			UUID: ptr.To("8c0c9436-f85e-49f4-ac00-782dbfb3c8f7"),
		},
		Spec: &prismclientv3.Image{
			Name: ptr.To("bootstrap_image"),
		},
		Status: &prismclientv3.ImageDefStatus{
			State: ptr.To(""),
		},
	}

	defaultNtnxMachine := &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: infrav1.NutanixMachineSpec{
			Image: &infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
			},
			VCPUsPerSocket: int32(minVCPUsPerSocket),
			MemorySize:     minMachineMemorySize,
			SystemDiskSize: minMachineSystemDiskSize,
			VCPUSockets:    int32(minVCPUSockets),
			DataDisks: []infrav1.NutanixMachineVMDisk{
				{
					DiskSize: resource.MustParse("20Gi"),
					DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
						DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
						AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
					},
					StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
						DiskMode: infrav1.NutanixMachineDiskModeStandard,
						StorageContainer: &infrav1.NutanixResourceIdentifier{
							UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
							Type: infrav1.NutanixIdentifierUUID,
						},
					},
				},
			},
			Subnets: []infrav1.NutanixResourceIdentifier{
				{
					Type: infrav1.NutanixIdentifierName,
					Name: ptr.To("subnet1"),
				},
			},
			Cluster: infrav1.NutanixResourceIdentifier{
				Type: infrav1.NutanixIdentifierUUID,
				UUID: ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
			},
			BootstrapRef: &corev1.ObjectReference{
				Kind: infrav1.NutanixMachineBootstrapRefKindImage,
				Name: "bootstrap_image",
			},
		},
	}

	defaultMachine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test",
			},
		},
		Spec: capiv1.MachineSpec{
			ClusterName: "test",
		},
	}

	defaultNtnxCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: infrav1.NutanixClusterSpec{
			PrismCentral: &credentialTypes.NutanixPrismEndpoint{
				Address: "prism.central.ntnx",
				Port:    9440,
				CredentialRef: &credentialTypes.NutanixCredentialReference{
					Kind:      credentialTypes.SecretKind,
					Name:      "test",
					Namespace: "default",
				},
			},
		},
	}

	tt := []struct {
		name         string
		fixtures     func(*gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *prismclientv3.Client)
		wantDisksLen int
		wantErr      bool
	}{
		{
			name:         "return get disk list",
			wantDisksLen: 3,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *prismclientv3.Client) {
				mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
				mockV3Service.EXPECT().GetImage(gomock.Any(), *defaultSystemImage.Metadata.UUID).Return(defaultSystemImage, nil).MinTimes(1)
				mockV3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(&prismclientv3.ImageListIntentResponse{
					Entities: []*prismclientv3.ImageIntentResponse{
						defaultSystemImage, defaultBootstrapImage,
					},
				}, nil).MinTimes(1)
				mockV3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil).AnyTimes()

				prismClient := &prismclientv3.Client{
					V3: mockV3Service,
				}

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, prismClient
			},
		},
		{
			name:    "return an error if the bootstrap disk is not found",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *prismclientv3.Client) {
				mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
				mockV3Service.EXPECT().GetImage(gomock.Any(), *defaultSystemImage.Metadata.UUID).Return(defaultSystemImage, nil).MinTimes(1)
				mockV3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(&prismclientv3.ImageListIntentResponse{
					Entities: []*prismclientv3.ImageIntentResponse{
						defaultSystemImage,
					},
				}, nil).MinTimes(1)
				mockV3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil).AnyTimes()

				prismClient := &prismclientv3.Client{
					V3: mockV3Service,
				}

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, prismClient
			},
		},
		{
			name:    "return an error if the system disk is not found",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *prismclientv3.Client) {
				mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
				mockV3Service.EXPECT().GetImage(gomock.Any(), *defaultSystemImage.Metadata.UUID).Return(nil, fmt.Errorf("ENTITY_NOT_FOUND")).MinTimes(1)
				mockV3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil).AnyTimes()

				prismClient := &prismclientv3.Client{
					V3: mockV3Service,
				}

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, prismClient
			},
		},
		{
			name:    "return an error if the system disk is marked for deletion",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *prismclientv3.Client) {
				systemImage := &prismclientv3.ImageIntentResponse{
					Metadata: &prismclientv3.Metadata{
						Kind: ptr.To("image"),
						UUID: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
					},
					Spec: &prismclientv3.Image{
						Name: ptr.To("system_image"),
					},
					Status: &prismclientv3.ImageDefStatus{
						State: ptr.To(string(ImageStateDeleteInProgress)),
					},
				}

				mockV3Service := mocknutanixv3.NewMockService(mockCtrl)
				mockV3Service.EXPECT().GetImage(gomock.Any(), *systemImage.Metadata.UUID).Return(systemImage, nil).MinTimes(1)
				mockV3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil).AnyTimes()

				prismClient := &prismclientv3.Client{
					V3: mockV3Service,
				}

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, prismClient
			},
		},
		{
			name:    "return an error if the bootstrap disk is marked for deletion",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *prismclientv3.Client) {
				mockV3Service := mocknutanixv3.NewMockService(mockCtrl)

				bootstrapImage := &prismclientv3.ImageIntentResponse{
					Metadata: &prismclientv3.Metadata{
						Kind: ptr.To("image"),
						UUID: ptr.To("8c0c9436-f85e-49f4-ac00-782dbfb3c8f7"),
					},
					Spec: &prismclientv3.Image{
						Name: ptr.To("bootstrap_image"),
					},
					Status: &prismclientv3.ImageDefStatus{
						State: ptr.To(string(ImageStateDeletePending)),
					},
				}
				mockV3Service.EXPECT().GetImage(gomock.Any(), *defaultSystemImage.Metadata.UUID).Return(defaultSystemImage, nil).MinTimes(1)
				mockV3Service.EXPECT().ListAllImage(gomock.Any(), gomock.Any()).Return(&prismclientv3.ImageListIntentResponse{
					Entities: []*prismclientv3.ImageIntentResponse{
						defaultSystemImage, bootstrapImage,
					},
				}, nil).MinTimes(1)
				mockV3Service.EXPECT().GroupsGetEntities(gomock.Any(), gomock.Any()).Return(defaultStorageContainerGroupsEntities(), nil).AnyTimes()

				prismClient := &prismclientv3.Client{
					V3: mockV3Service,
				}

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, prismClient
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			ntnxMachine, machine, ntnxCluster, prismClient := tc.fixtures(mockCtrl)

			disks, err := getDiskList(&nctx.MachineContext{
				Context:        context.Background(),
				NutanixMachine: ntnxMachine,
				Machine:        machine,
				NutanixCluster: ntnxCluster,
				NutanixClient:  prismClient,
			}, *ntnxMachine.Spec.Cluster.UUID)

			if tc.wantErr != (err != nil) {
				t.Fatal("got unexpected error: ", err)
			}

			if tc.wantDisksLen != len(disks) {
				t.Fatalf("expected %d disks, got %d", tc.wantDisksLen, len(disks))
			}
		})
	}
}
