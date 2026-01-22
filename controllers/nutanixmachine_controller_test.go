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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	converged "github.com/nutanix-cloud-native/prism-go-client/converged"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	credentialTypes "github.com/nutanix-cloud-native/prism-go-client/environment/credentials"
	prismclientv3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	clustermgmtconfig "github.com/nutanix/ntnx-api-golang-clients/clustermgmt-go-client/v4/models/clustermgmt/v4/config"
	subnetModels "github.com/nutanix/ntnx-api-golang-clients/networking-go-client/v4/models/networking/v4/config"
	prismModels "github.com/nutanix/ntnx-api-golang-clients/prism-go-client/v4/models/prism/v4/config"
	vmmCommonConfig "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/common/v1/config"
	vmmModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/ahv/config"
	imageModels "github.com/nutanix/ntnx-api-golang-clients/vmm-go-client/v4/models/vmm/v4/content"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	mockconverged "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/converged"
	mockctlclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/ctlclient"
	mockmeta "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sapimachinery"
	mockk8sclient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/mocks/k8sclient"
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
			fdObj       *infrav1.NutanixFailureDomain
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

			fdObj = &infrav1.NutanixFailureDomain{
				TypeMeta: metav1.TypeMeta{
					Kind:       infrav1.NutanixFailureDomainKind,
					APIVersion: infrav1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fd-test",
					Namespace: corev1.NamespaceDefault,
				},
				Spec: infrav1.NutanixFailureDomainSpec{
					PrismElementCluster: infrav1.NutanixResourceIdentifier{
						Type: infrav1.NutanixIdentifierName,
						Name: &r,
					},
					Subnets: []infrav1.NutanixResourceIdentifier{
						{Type: infrav1.NutanixIdentifierName, Name: &r},
					},
				},
			}
		})

		AfterEach(func() {
			// Delete the failure domain object if exists.
			_ = k8sClient.Delete(ctx, fdObj)
		})

		Context("Validate status.failureDomain", func() {
			It("status.failureDomain should not be set if failureDomain is not configured in the owner machine spec", func() {
				mctx := &nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				}
				err := reconciler.checkFailureDomainStatus(mctx)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ntnxMachine.Status.FailureDomain).To(BeNil())
			})

			It("status.failureDomain should be set if failureDomain is configured correctly in the owner machine spec", func() {
				// Create the NutanixFailureDomain object and expect creation success
				g.Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

				machine.Spec.FailureDomain = &fdObj.Name
				ntnxMachine.Spec.Cluster = fdObj.Spec.PrismElementCluster
				ntnxMachine.Spec.Subnets = fdObj.Spec.Subnets
				mctx := &nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				}
				err := reconciler.checkFailureDomainStatus(mctx)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ntnxMachine.Status.FailureDomain).ToNot(BeNil())
				g.Expect(*ntnxMachine.Status.FailureDomain).To(Equal(fdObj.Name))
			})

			It("should error if failureDomain is configured in the owner machine spec and the failureDomain object not found", func() {
				machine.Spec.FailureDomain = &fdObj.Name
				ntnxMachine.Spec.Cluster = fdObj.Spec.PrismElementCluster
				ntnxMachine.Spec.Subnets = fdObj.Spec.Subnets
				mctx := &nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				}
				err := reconciler.checkFailureDomainStatus(mctx)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("failed to fetch the referent failure domain object"))
			})

			It("should error if failureDomain is configured in the owner machine spec and cluster configuration is not consistent", func() {
				// Create the NutanixFailureDomain object and expect creation success
				g.Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

				machine.Spec.FailureDomain = &fdObj.Name
				ntnxMachine.Spec.Cluster = fdObj.Spec.PrismElementCluster
				ntnxMachine.Spec.Cluster.Name = nil
				mctx := &nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				}
				err := reconciler.checkFailureDomainStatus(mctx)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("the NutanixMachine is not consistent with the referenced NutanixFailureDomain"))
			})

			It("should error if failureDomain is configured in the owner machine spec and subnets configuration is not consistent", func() {
				// Create the NutanixFailureDomain object and expect creation success
				g.Expect(k8sClient.Create(ctx, fdObj)).To(Succeed())

				machine.Spec.FailureDomain = &fdObj.Name
				ntnxMachine.Spec.Cluster = fdObj.Spec.PrismElementCluster
				// ntnxMachine.Spec.Subnets is empty
				mctx := &nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				}
				err := reconciler.checkFailureDomainStatus(mctx)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("the NutanixMachine is not consistent with the referenced NutanixFailureDomain"))
			})
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
			It("returns error if invalid machine config is passed with reference to not-exist failure domain", func() {
				machine.Spec.FailureDomain = &r
				err := reconciler.validateMachineConfig(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
				})
				g.Expect(err).To(HaveOccurred())
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
		})
		Context("Can get failure domain spec with the legacy failure domain configuration", func() {
			It("returns a valid failure domain if the legacy failure domains are used", func() {
				ntnxCluster.Spec.FailureDomains = []infrav1.NutanixFailureDomainConfig{ //nolint:staticcheck // this is a test
					{
						Name:    "failure-domain",
						Cluster: fdObj.Spec.PrismElementCluster,
						Subnets: fdObj.Spec.Subnets,
					},
				}
				machine.Spec.FailureDomain = ptr.To("failure-domain")
				fd, err := reconciler.getFailureDomainSpec(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				}, "failure-domain")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(fd).ToNot(BeNil())
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
			SkipNameValidation:      true, // Enable for tests
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

type FilterMatcher struct {
	ContainsExtId string
}

func (m FilterMatcher) Matches(actual any) bool {
	fmt.Printf("=== FilterMatcher.Matches called ===\n")
	fmt.Printf("Looking for ExtId: %s\n", m.ContainsExtId)
	fmt.Printf("Actual type: %T\n", actual)
	fmt.Printf("Actual value: %v\n", actual)

	actualODataOptions, ok := actual.([]converged.ODataOption)
	if !ok {
		fmt.Printf("ERROR: actual is not []converged.ODataOption, got type %T\n", actual)
		return false
	}

	fmt.Printf("actualODataOptions: %v\n", actualODataOptions)
	v4ODataOptions, err := v4Converged.OptsToV4ODataParams(actualODataOptions...)
	if err != nil {
		fmt.Printf("ERROR: failed to convert ODataOptions to V4ODataParams: %v\n", err)
		return false
	}

	fmt.Printf("v4ODataOptions: %v\n", v4ODataOptions)
	if v4ODataOptions.Filter == nil {
		fmt.Printf("ERROR: filter is nil\n")
		return false
	}

	fmt.Printf("v4ODataOptions.Filter: %v\n", *v4ODataOptions.Filter)
	if !strings.Contains(*v4ODataOptions.Filter, m.ContainsExtId) {
		fmt.Printf("ERROR: filter does not contain %s\n", m.ContainsExtId)
		return false
	}

	fmt.Printf("SUCCESS: filter contains %s\n", m.ContainsExtId)
	fmt.Printf("=== FilterMatcher.Matches returning true ===\n")
	return true
}

func (m FilterMatcher) String() string {
	return fmt.Sprintf("filter contains %s", m.ContainsExtId)
}

func TestNutanixClusterReconcilerGetDiskList(t *testing.T) {
	defaultSystemImage := &imageModels.Image{
		ExtId: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
		Name:  ptr.To("system_image"),
	}

	defaultBootstrapImage := &imageModels.Image{
		ExtId: ptr.To("8c0c9436-f85e-49f4-ac00-782dbfb3c8f7"),
		Name:  ptr.To("bootstrap_image"),
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
		fixtures     func(*gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *v4Converged.Client)
		wantDisksLen int
		wantErr      bool
	}{
		{
			name:         "return get disk list",
			wantDisksLen: 3,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
				convergedClientMock := NewMockConvergedClient(mockCtrl)
				convergedClientMock.MockImages.EXPECT().Get(gomock.Any(), *defaultSystemImage.ExtId).Return(defaultSystemImage, nil).MinTimes(1)
				convergedClientMock.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						*defaultSystemImage,
						*defaultBootstrapImage,
					}, nil).MinTimes(1)
				convergedClientMock.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil).MinTimes(1)

				convergedClientMock.MockStorageContainers.EXPECT().List(gomock.Any(), gomock.Any()).Return([]clustermgmtconfig.StorageContainer{
					{
						ContainerExtId: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
						ClusterExtId:   ptr.To("00062e56-b9ac-7253-1946-7cc25586eeee"),
					},
				}, nil)

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, convergedClientMock.Client
			},
		},
		{
			name:    "return an error if the bootstrap disk is not found",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
				convergedClientMock := NewMockConvergedClient(mockCtrl)
				convergedClientMock.MockImages.EXPECT().Get(gomock.Any(), *defaultSystemImage.ExtId).Return(defaultSystemImage, nil).MinTimes(1)
				convergedClientMock.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						*defaultSystemImage,
					}, nil).MinTimes(1)
				convergedClientMock.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil).MinTimes(1)

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, convergedClientMock.Client
			},
		},
		{
			name:    "return an error if the system disk is not found",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
				errorMessage := `Error getting image: failed to get image: API call failed: {"data":{"error":[{"$reserved":{"$fv":"v4.r1"},"$objectType":"vmm.v4.error.AppMessage","message":"Failed to perform the operation as the backend service could not find the entity.","severity":"ERROR","code":"VMM-20005","locale":"en_US"}],"$reserved":{"$fv":"v4.r1"},"$objectType":"vmm.v4.error.ErrorResponse"},"$reserved":{"$fv":"v4.r1"},"$objectType":"vmm.v4.content.GetImageApiResponse"}`
				convergedClientMock := NewMockConvergedClient(mockCtrl)
				convergedClientMock.MockImages.EXPECT().Get(gomock.Any(), *defaultSystemImage.ExtId).Return(
					nil,
					errors.New(errorMessage),
				).MinTimes(1)

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, convergedClientMock.Client
			},
		},
		{
			name:    "return an error if the system disk is marked for deletion",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
				systemImage := &imageModels.Image{
					ExtId: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
					Name:  ptr.To("system_image"),
				}

				convergedClientMock := NewMockConvergedClient(mockCtrl)
				convergedClientMock.MockImages.EXPECT().Get(gomock.Any(), *systemImage.ExtId).Return(systemImage, nil).MinTimes(1)
				runningStatus := prismModels.TASKSTATUS_RUNNING
				convergedClientMock.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]prismModels.Task{
						{
							ExtId:     ptr.To(uuid.New().String()),
							Operation: ptr.To("kImageDelete"),
							Status:    &runningStatus,
							EntitiesAffected: []prismModels.EntityReference{
								{
									ExtId: systemImage.ExtId,
								},
							},
						},
					},
					nil,
				).MinTimes(1)

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, convergedClientMock.Client
			},
		},
		{
			name:    "return an error if the bootstrap disk is marked for deletion",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
				convergedClientMock := NewMockConvergedClient(mockCtrl)
				convergedClientMock.MockImages.EXPECT().Get(gomock.Any(), *defaultSystemImage.ExtId).Return(defaultSystemImage, nil).MinTimes(1)
				convergedClientMock.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						*defaultSystemImage,
						*defaultBootstrapImage,
					}, nil).MinTimes(1)
				queuedStatus := prismModels.TASKSTATUS_QUEUED

				convergedClientMock.MockTasks.EXPECT().List(gomock.Any(), FilterMatcher{ContainsExtId: *defaultSystemImage.ExtId}).Return(
					[]prismModels.Task{}, nil).MinTimes(1)

				convergedClientMock.MockTasks.EXPECT().List(gomock.Any(), FilterMatcher{ContainsExtId: *defaultBootstrapImage.ExtId}).Return(
					[]prismModels.Task{
						{
							ExtId:     ptr.To(uuid.New().String()),
							Operation: ptr.To("kImageDelete"),
							Status:    &queuedStatus,
							EntitiesAffected: []prismModels.EntityReference{
								{
									ExtId: defaultBootstrapImage.ExtId,
								},
							},
						},
					},
					nil,
				).MinTimes(1)

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, convergedClientMock.Client
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			ntnxMachine, machine, ntnxCluster, convergedClient := tc.fixtures(mockCtrl)

			disks, cdRoms, err := getDiskList(&nctx.MachineContext{
				Context:         context.Background(),
				NutanixMachine:  ntnxMachine,
				Machine:         machine,
				NutanixCluster:  ntnxCluster,
				ConvergedClient: convergedClient,
			}, *ntnxMachine.Spec.Cluster.UUID)

			if tc.wantErr != (err != nil) {
				t.Fatal("got unexpected error: ", err)
			}

			if tc.wantDisksLen != len(disks)+len(cdRoms) {
				t.Fatalf("expected %d disks, got %d", tc.wantDisksLen, len(disks)+len(cdRoms))
			}
		})
	}
}

func TestNutanixMachineReconciler_ConvergedClient(t *testing.T) {
	t.Run("should handle converged client initialization failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialTypes.NutanixPrismEndpoint{
					Address: "prismcentral.nutanix.com",
					Port:    9440,
					CredentialRef: &credentialTypes.NutanixCredentialReference{
						Kind:      credentialTypes.SecretKind,
						Name:      "test-credential",
						Namespace: "test-ns",
					},
				},
			},
		}

		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		// Mock the secret lister to return an error
		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(nil, errors.New("secret not found"))
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer.EXPECT().Lister().Return(secretLister)

		// Test the converged client function directly
		_, err := getPrismCentralConvergedV4ClientForCluster(ctx, ntnxCluster, secretInformer, mapInformer)
		assert.Error(t, err)
	})

	t.Run("should successfully initialize converged client", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialTypes.NutanixPrismEndpoint{
					Address: "prismcentral.nutanix.com",
					Port:    9440,
					CredentialRef: &credentialTypes.NutanixCredentialReference{
						Kind:      credentialTypes.SecretKind,
						Name:      "test-credential",
						Namespace: "test-ns",
					},
				},
			},
		}

		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		// Mock the secret lister to return valid credentials
		creds := []credentialTypes.Credential{
			{
				Type: credentialTypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user","password":"password"}}`),
			},
		}
		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)

		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialTypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer.EXPECT().Lister().Return(secretLister)

		// Test the converged client function directly
		client, err := getPrismCentralConvergedV4ClientForCluster(ctx, ntnxCluster, secretInformer, mapInformer)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("should handle converged client initialization with malformed credentials", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialTypes.NutanixPrismEndpoint{
					Address: "prismcentral.nutanix.com",
					Port:    9440,
					CredentialRef: &credentialTypes.NutanixCredentialReference{
						Kind:      credentialTypes.SecretKind,
						Name:      "test-credential",
						Namespace: "test-ns",
					},
				},
			},
		}

		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		// Mock the secret lister to return malformed credentials
		creds := []credentialTypes.Credential{
			{
				Type: credentialTypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user"}}`), // Missing password
			},
		}
		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)

		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialTypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer.EXPECT().Lister().Return(secretLister)

		// Test the converged client function directly
		_, err = getPrismCentralConvergedV4ClientForCluster(ctx, ntnxCluster, secretInformer, mapInformer)
		assert.Error(t, err)
	})
}

func TestNutanixMachineReconciler_ConvergedClientIntegration(t *testing.T) {
	t.Run("should handle converged client initialization in reconcile flow", func(t *testing.T) {
		// This test verifies that the converged client is properly initialized
		// in the reconcile flow by testing the function directly
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialTypes.NutanixPrismEndpoint{
					Address: "prismcentral.nutanix.com",
					Port:    9440,
					CredentialRef: &credentialTypes.NutanixCredentialReference{
						Kind:      credentialTypes.SecretKind,
						Name:      "test-credential",
						Namespace: "test-ns",
					},
				},
			},
		}

		// Create mock informers
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		// Mock the secret lister to return valid credentials
		creds := []credentialTypes.Credential{
			{
				Type: credentialTypes.BasicAuthCredentialType,
				Data: []byte(`{"prismCentral":{"username":"user","password":"password"}}`),
			},
		}
		credsMarshal, err := json.Marshal(creds)
		require.NoError(t, err)

		secret := &corev1.Secret{
			Data: map[string][]byte{
				credentialTypes.KeyName: credsMarshal,
			},
		}

		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(secret, nil)
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer.EXPECT().Lister().Return(secretLister)

		// Test the converged client function directly
		client, err := getPrismCentralConvergedV4ClientForCluster(ctx, ntnxCluster, secretInformer, mapInformer)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("should handle converged client initialization failure in reconcile flow", func(t *testing.T) {
		// This test verifies that converged client initialization failures
		// are properly handled in the reconcile flow
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
			Spec: infrav1.NutanixClusterSpec{
				PrismCentral: &credentialTypes.NutanixPrismEndpoint{
					Address: "prismcentral.nutanix.com",
					Port:    9440,
					CredentialRef: &credentialTypes.NutanixCredentialReference{
						Kind:      credentialTypes.SecretKind,
						Name:      "test-credential",
						Namespace: "test-ns",
					},
				},
			},
		}

		// Create mock informers
		secretInformer := mockk8sclient.NewMockSecretInformer(ctrl)
		mapInformer := mockk8sclient.NewMockConfigMapInformer(ctrl)

		// Mock the secret lister to return an error
		secretNamespaceLister := mockk8sclient.NewMockSecretNamespaceLister(ctrl)
		secretNamespaceLister.EXPECT().Get("test-credential").Return(nil, errors.New("secret not found"))
		secretLister := mockk8sclient.NewMockSecretLister(ctrl)
		secretLister.EXPECT().Secrets("test-ns").Return(secretNamespaceLister)
		secretInformer.EXPECT().Lister().Return(secretLister)

		// Test the converged client function directly
		_, err := getPrismCentralConvergedV4ClientForCluster(ctx, ntnxCluster, secretInformer, mapInformer)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "secret not found")
	})
}

func TestGetSystemDisk(t *testing.T) {
	t.Run("should successfully get system disk with ImageLookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		k8sVersion := "v1.31.4"
		baseOS := "ubuntu"
		imageTemplate := "capx-{{.BaseOS}}-{{.K8sVersion}}"
		expectedImageName := "capx-ubuntu-1.31.4"

		// Create NutanixMachine with ImageLookup
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ImageLookup: &infrav1.NutanixImageLookup{
					BaseOS: baseOS,
					Format: &imageTemplate,
				},
				SystemDiskSize: resource.MustParse("40Gi"),
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1.MachineSpec{
				Version: &k8sVersion,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock converged client
		mockConvergedClient := NewMockConvergedClient(ctrl)
		expectedImage := &imageModels.Image{
			ExtId: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
			Name:  ptr.To(expectedImageName),
		}

		// Mock GetImageByLookup to return the expected image
		mockConvergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]imageModels.Image{*expectedImage}, nil,
		)

		// Mock ImageMarkedForDeletion to return false
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]prismModels.Task{}, nil,
		)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, systemDisk)

		vmDiskIntf := systemDisk.GetBackingInfo()
		assert.NotNil(t, vmDiskIntf)
		vmDisk, ok := vmDiskIntf.(vmmModels.VmDisk)
		assert.Equal(t, true, ok)
		assert.Equal(t, int64(2*21474836480), *vmDisk.DiskSizeBytes)

		imageIntf := vmDisk.DataSource.GetReference()
		assert.NotNil(t, imageIntf)
		imageRef, ok := imageIntf.(vmmModels.ImageReference)
		assert.Equal(t, true, ok)

		assert.Equal(t, *expectedImage.ExtId, *imageRef.ImageExtId)
	})

	t.Run("should handle ImageLookup with GetImageByLookup failure", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		k8sVersion := "v1.31.4"
		baseOS := "ubuntu"
		imageTemplate := "capx-{{.BaseOS}}-{{.K8sVersion}}"

		// Create NutanixMachine with ImageLookup
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ImageLookup: &infrav1.NutanixImageLookup{
					BaseOS: baseOS,
					Format: &imageTemplate,
				},
				SystemDiskSize: resource.MustParse("40Gi"),
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1.MachineSpec{
				Version: &k8sVersion,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock converged client
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock GetImageByLookup to return an error
		mockConvergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			nil, errors.New("failed to find image"),
		)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, systemDisk)
		assert.Contains(t, err.Error(), "failed to find image")
	})

	t.Run("should handle ImageLookup with image marked for deletion", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		k8sVersion := "v1.31.4"
		baseOS := "ubuntu"
		imageTemplate := "capx-{{.BaseOS}}-{{.K8sVersion}}"
		expectedImageName := "capx-ubuntu-1.31.4"

		// Create NutanixMachine with ImageLookup
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ImageLookup: &infrav1.NutanixImageLookup{
					BaseOS: baseOS,
					Format: &imageTemplate,
				},
				SystemDiskSize: resource.MustParse("40Gi"),
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1.MachineSpec{
				Version: &k8sVersion,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock converged client
		mockConvergedClient := NewMockConvergedClient(ctrl)
		expectedImage := &imageModels.Image{
			ExtId: ptr.To("f47ac10b-58cc-4372-a567-0e02b2c3d479"),
			Name:  ptr.To(expectedImageName),
		}

		// Mock GetImageByLookup to return the expected image
		mockConvergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]imageModels.Image{*expectedImage}, nil,
		)

		// Mock ImageMarkedForDeletion to return true (image is being deleted)
		runningStatus := prismModels.TASKSTATUS_RUNNING
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]prismModels.Task{
				{
					ExtId:     ptr.To("task-uuid-123"),
					Operation: ptr.To("kImageDelete"),
					Status:    &runningStatus,
					EntitiesAffected: []prismModels.EntityReference{
						{
							ExtId: expectedImage.ExtId,
						},
					},
				},
			}, nil,
		)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, systemDisk)
		assert.Contains(t, err.Error(), "system disk image")
		assert.Contains(t, err.Error(), "is being deleted")
	})

	t.Run("should handle ImageLookup with template parsing error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		k8sVersion := "v1.31.4"
		baseOS := "ubuntu"
		invalidTemplate := "invalid-template-{{.InvalidField}}"

		// Create NutanixMachine with ImageLookup
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ImageLookup: &infrav1.NutanixImageLookup{
					BaseOS: baseOS,
					Format: &invalidTemplate,
				},
				SystemDiskSize: resource.MustParse("40Gi"),
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1.MachineSpec{
				Version: &k8sVersion,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock converged client
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, systemDisk)
		assert.Contains(t, err.Error(), "failed to substitute string")
	})

	t.Run("should handle ImageLookup with no images found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		k8sVersion := "v1.31.4"
		baseOS := "ubuntu"
		imageTemplate := "capx-{{.BaseOS}}-{{.K8sVersion}}"

		// Create NutanixMachine with ImageLookup
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ImageLookup: &infrav1.NutanixImageLookup{
					BaseOS: baseOS,
					Format: &imageTemplate,
				},
				SystemDiskSize: resource.MustParse("40Gi"),
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1.MachineSpec{
				Version: &k8sVersion,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock converged client
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock GetImageByLookup to return empty list
		mockConvergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]imageModels.Image{}, nil,
		)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, systemDisk)
		assert.Contains(t, err.Error(), "failed to find image with filter")
	})

	t.Run("should handle ImageLookup with multiple images and return latest", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		k8sVersion := "v1.31.4"
		baseOS := "ubuntu"
		imageTemplate := "capx-{{.BaseOS}}-{{.K8sVersion}}"
		expectedImageName := "capx-ubuntu-1.31.4"

		// Create NutanixMachine with ImageLookup
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ImageLookup: &infrav1.NutanixImageLookup{
					BaseOS: baseOS,
					Format: &imageTemplate,
				},
				SystemDiskSize: resource.MustParse("40Gi"),
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1.MachineSpec{
				Version: &k8sVersion,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock converged client
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Create multiple images with different creation times
		olderImage := imageModels.Image{
			ExtId:      ptr.To("older-image-uuid"),
			Name:       ptr.To(expectedImageName),
			CreateTime: ptr.To(time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC)),
		}
		newerImage := imageModels.Image{
			ExtId:      ptr.To("newer-image-uuid"),
			Name:       ptr.To(expectedImageName),
			CreateTime: ptr.To(time.Date(2023, 10, 2, 0, 0, 0, 0, time.UTC)),
		}

		// Mock GetImageByLookup to return multiple images
		mockConvergedClient.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]imageModels.Image{olderImage, newerImage}, nil,
		)

		// Mock ImageMarkedForDeletion to return false
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]prismModels.Task{}, nil,
		)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx)

		// Verify results - should return the newer image
		assert.NoError(t, err)
		assert.NotNil(t, systemDisk)

		vmDiskIntf := systemDisk.GetBackingInfo()
		assert.NotNil(t, vmDiskIntf)
		vmDisk, ok := vmDiskIntf.(vmmModels.VmDisk)
		assert.Equal(t, true, ok)
		assert.Equal(t, int64(2*21474836480), *vmDisk.DiskSizeBytes)

		imageIntf := vmDisk.DataSource.GetReference()
		assert.NotNil(t, imageIntf)
		imageRef, ok := imageIntf.(vmmModels.ImageReference)
		assert.Equal(t, true, ok)

		assert.Equal(t, "newer-image-uuid", *imageRef.ImageExtId)
	})

	t.Run("should handle ImageLookup with nil ImageLookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"

		// Create NutanixMachine without ImageLookup or Image
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				SystemDiskSize: resource.MustParse("40Gi"),
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock converged client
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk - this should panic due to nil nodeOSImage
		// The function has a bug where it doesn't handle the case where both Image and ImageLookup are nil
		assert.Panics(t, func() {
			_, _ = getSystemDisk(rctx)
		})
	})
}

func TestNutanixMachineReconciler_ReconcileDelete(t *testing.T) {
	t.Run("should handle empty VM UUID by removing finalizers", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"

		// Create NutanixMachine with empty VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				// VM UUID is empty
			},
		}

		// Add finalizers to test removal
		ntnxMachine.Finalizers = []string{
			infrav1.NutanixMachineFinalizer,
			infrav1.DeprecatedNutanixMachineFinalizer,
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock client
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, result)
		assert.NotContains(t, ntnxMachine.Finalizers, infrav1.NutanixMachineFinalizer)
		assert.NotContains(t, ntnxMachine.Finalizers, infrav1.DeprecatedNutanixMachineFinalizer)
	})

	t.Run("should handle VM not found by removing finalizers", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		// Add finalizers to test removal
		ntnxMachine.Finalizers = []string{
			infrav1.NutanixMachineFinalizer,
			infrav1.DeprecatedNutanixMachineFinalizer,
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), vmUUID).Return(nil, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, result)
		assert.NotContains(t, ntnxMachine.Finalizers, infrav1.NutanixMachineFinalizer)
		assert.NotContains(t, ntnxMachine.Finalizers, infrav1.DeprecatedNutanixMachineFinalizer)
	})

	t.Run("should handle VM name mismatch by returning error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		wrongVMName := "wrong-vm-name"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock VM with wrong name
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(wrongVMName)
		vm.ExtId = ptr.To(vmUUID)

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), vmUUID).Return(vm, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "did not match Machine name")
		assert.Contains(t, err.Error(), "or NutanixMachineName")
		assert.Equal(t, reconcile.Result{}, result)
	})

	t.Run("should handle VM deletion with converged client error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock VM
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.ExtId = ptr.To(vmUUID)

		// Create mock client
		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), vmUUID).Return(vm, nil)
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), FilterMatcher{ContainsExtId: vmUUID}).Return(nil, nil)
		mockConvergedClient.MockVMs.EXPECT().DeleteAsync(gomock.Any(), vmUUID).Return(nil, errors.New("converged client error"))

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results - should return error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "converged client error")
		assert.Equal(t, reconcile.Result{}, result)
	})

	t.Run("should handle VM deletion without volume groups", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock VM without volume groups
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.Disks = []vmmModels.Disk{}
		vm.ExtId = ptr.To(vmUUID)

		// Create mock client
		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), vmUUID).Return(vm, nil)
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), FilterMatcher{ContainsExtId: vmUUID}).Return(nil, nil)
		// Mock DeleteAsync to return a task
		mockOperation := mockconverged.NewMockOperation[converged.NoEntity](ctrl)
		mockConvergedClient.MockVMs.EXPECT().DeleteAsync(gomock.Any(), vmUUID).Return(mockOperation, nil)
		mockOperation.EXPECT().UUID().Return("task-uuid-123").AnyTimes()

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results - should proceed to VM deletion (volume group detach is handled internally)
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: 5 * time.Second}, result)
	})

	t.Run("should successfully delete VM and requeue", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock VM without volume groups
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.Disks = []vmmModels.Disk{}
		vm.ExtId = ptr.To(vmUUID)

		// Create mock client
		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), vmUUID).Return(vm, nil)
		mockOperation := mockconverged.NewMockOperation[converged.NoEntity](ctrl)
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), FilterMatcher{ContainsExtId: vmUUID}).Return(nil, nil)
		mockConvergedClient.MockVMs.EXPECT().DeleteAsync(gomock.Any(), vmUUID).Return(mockOperation, nil)
		mockOperation.EXPECT().UUID().Return("task-uuid-123").AnyTimes()

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results - should proceed to VM deletion
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: 5 * time.Second}, result)
	})

	t.Run("should handle various error scenarios", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock client
		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), vmUUID).Return(nil, errors.New("VM not found"))

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "error finding VM")
		assert.Equal(t, reconcile.Result{}, result)
	})

	t.Run("should return error when VMHasTaskInProgress fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock VM
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.ExtId = ptr.To(vmUUID)

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), gomock.Any()).Return(vm, nil)
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, errors.New("failed to list tasks"))

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results - should fail with VmHasTaskInProgress error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list tasks")
		assert.Equal(t, reconcile.Result{}, result)
	})

	t.Run("should proceed with deletion when VmHasTaskInProgress returns false", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock VM without volume groups
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.ExtId = ptr.To(vmUUID)

		// Create mock clients

		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock FindVMByUUID to return VM
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), gomock.Any()).Return(vm, nil)

		// Mock VmHasTaskInProgress to return false
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil)

		// Mock DeleteAsync to proceed with deletion since no task is in progress
		mockOperation := mockconverged.NewMockOperation[converged.NoEntity](ctrl)
		mockConvergedClient.MockVMs.EXPECT().DeleteAsync(gomock.Any(), vmUUID).Return(mockOperation, nil)
		mockOperation.EXPECT().UUID().Return("delete-task-uuid-123").AnyTimes()

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results - should proceed with deletion when no task UUID is found
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: 5 * time.Second}, result)
	})

	t.Run("should requeue when VmHasTaskInProgress returns true", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		taskUUID := "ZXJnb24=:b4b17e07-b81c-43f4-9bf5-62149975d58f"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock VM
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.ExtId = ptr.To(vmUUID)

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), gomock.Any()).Return(vm, nil)
		mockConvergedClient.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return(
			[]prismModels.Task{{
				ExtId:  ptr.To(taskUUID),
				Status: ptr.To(prismModels.TASKSTATUS_RUNNING),
			}, {
				ExtId:  ptr.To(taskUUID),
				Status: ptr.To(prismModels.TASKSTATUS_QUEUED),
			}}, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results - should requeue when task is in progress
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: 5 * time.Second}, result)
	})
}

func TestNutanixMachineReconciler_getOrCreateVM(t *testing.T) {
	t.Run("should return existing VM when found by UUID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock FindVM to return existing VM
		expectedVm := vmmModels.NewVm()
		expectedVm.Name = ptr.To(vmName)
		expectedVm.ExtId = ptr.To(vmUUID)
		mockConvergedClient.MockVMs.EXPECT().Get(ctx, vmUUID).Return(expectedVm, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test getOrCreateVM
		vm, err := reconciler.getOrCreateVM(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
		assert.Equal(t, vmUUID, *vm.ExtId)
	})

	t.Run("should return existing VM when found by name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine without VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock FindVMByName
		expectedVM := vmmModels.NewVm()
		expectedVM.Name = ptr.To(vmName)
		expectedVM.ExtId = ptr.To(vmUUID)
		mockConvergedClient.MockVMs.EXPECT().List(ctx, FilterMatcher{ContainsExtId: vmName}).Return([]vmmModels.Vm{*expectedVM}, nil)
		mockConvergedClient.MockVMs.EXPECT().Get(ctx, vmUUID).Return(expectedVM, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test getOrCreateVM
		vm, err := reconciler.getOrCreateVM(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
	})

	t.Run("should return error when FindVM fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"

		// Create NutanixMachine
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock FindVM to return error
		mockConvergedClient.MockVMs.EXPECT().List(ctx, gomock.Any()).Return(nil, errors.New("API error"))

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test getOrCreateVM
		vm, err := reconciler.getOrCreateVM(rctx)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, vm)
		assert.Contains(t, err.Error(), "API error")
	})

	t.Run("should create VM when not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		peUUID := "00056024-f4f2-a6f6-0000-00000000e7f4"
		subnetUUID := "b8c6d9f0-4c5e-4c5e-8c5e-4c5e4c5e4c5e"
		imageUUID := "c5e4c5e4-c5e4-c5e4-c5e4-c5e4c5e4c5e4"
		clusterName := "test-cluster"
		projectUUID := "c5e4c5e4-c5e4-c5e4-c5e4-c5e4c5e4cabc"
		projectName := "test-project"

		// Create NutanixMachine with required specs
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				VCPUSockets:    2,
				VCPUsPerSocket: 1,
				MemorySize:     resource.MustParse("4Gi"),
				SystemDiskSize: resource.MustParse("40Gi"),
				BootType:       infrav1.NutanixBootTypeLegacy,
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierName,
					Name: &projectName,
				},
				Image: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &imageUUID,
				},
				Cluster: infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &peUUID,
				},
				Subnets: []infrav1.NutanixResourceIdentifier{
					{
						Type: infrav1.NutanixIdentifierUUID,
						UUID: &subnetUUID,
					},
				},
				BootstrapRef: &corev1.ObjectReference{
					Kind:      infrav1.NutanixMachineBootstrapRefKindSecret,
					Name:      "bootstrap-secret",
					Namespace: "default",
				},
			},
		}

		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1.MachineSpec{
				Version: ptr.To("v1.28.0"),
			},
			// SystemUUID is not set initially - it only gets set after the VM is created
			// and the node reports its SystemUUID. For this test, we're creating a new VM.
		}

		cluster := &capiv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: "default",
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: "default",
			},
		}

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockV3Client := mocknutanixv3.NewMockService(ctrl)
		v3Client := &prismclientv3.Client{V3: mockV3Client}

		// Mock FindVM to return nil (VM not found)
		// Since SystemUUID is not set, FindVMByName is called - return empty list
		mockConvergedClient.MockVMs.EXPECT().List(ctx, gomock.Any()).Return([]vmmModels.Vm{}, nil)

		// Mock GetCluster for PE UUID (called by GetSubnetAndPEUUIDs -> GetPEUUID)
		mockConvergedClient.MockClusters.EXPECT().Get(ctx, peUUID).Return(&clustermgmtconfig.Cluster{
			ExtId: &peUUID,
		}, nil)

		// Mock GetSubnet (called by GetSubnetAndPEUUIDs -> GetSubnetUUID)
		mockConvergedClient.MockSubnets.EXPECT().Get(ctx, subnetUUID).Return(&subnetModels.Subnet{
			ExtId: &subnetUUID,
		}, nil)

		// Mock category operations (called by getMachineCategoryIdentifiers and GetOrCreateCategories)
		categoryExtId := "category-ext-id"
		createdCategory := &prismModels.Category{
			ExtId: &categoryExtId,
			Key:   ptr.To(infrav1.DefaultCAPICategoryKeyForName),
			Value: ptr.To(clusterName),
		}
		// First call to List returns empty (doesn't exist), subsequent calls return the created category
		gomock.InOrder(
			mockConvergedClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{}, nil),
			mockConvergedClient.MockCategories.EXPECT().Create(ctx, gomock.Any()).Return(createdCategory, nil),
			mockConvergedClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Category{*createdCategory}, nil).AnyTimes(),
		)

		// Mock addVMToProject
		mockV3Client.EXPECT().ListAllProject(gomock.Any(), gomock.Any()).Return(&prismclientv3.ProjectListResponse{
			Entities: []*prismclientv3.Project{
				{
					Spec:     &prismclientv3.ProjectSpec{Name: projectName},
					Metadata: &prismclientv3.Metadata{UUID: &projectUUID},
				},
			},
		}, nil)

		// Mock GetImage (called by getDiskList -> getSystemDisk)
		mockConvergedClient.MockImages.EXPECT().Get(ctx, imageUUID).Return(&imageModels.Image{
			ExtId: &imageUUID,
		}, nil)

		// Mock Tasks.List calls in order:
		// 1. ImageMarkedForDeletion check returns empty
		// 2. GetTaskUUIDFromVM after VM creation returns task with UUID
		mockConvergedClient.MockTasks.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Task{}, nil)

		// Mock CreateVM
		createdVM := vmmModels.NewVm()
		createdVM.Name = ptr.To(vmName)
		createdVM.ExtId = ptr.To(vmUUID)
		mockConvergedClient.MockVMs.EXPECT().Create(ctx, gomock.Any()).Return(createdVM, nil)

		// Mock PowerOnVM
		mockOperation := mockconverged.NewMockOperation[vmmModels.Vm](ctrl)
		mockConvergedClient.MockVMs.EXPECT().PowerOnVM(vmUUID).Return(mockOperation, nil)
		mockOperation.EXPECT().Wait(gomock.Any()).Return(nil, nil)

		// Mock final FindVMByUUID call
		finalVM := vmmModels.NewVm()
		finalVM.Name = ptr.To(vmName)
		finalVM.ExtId = ptr.To(vmUUID)
		finalVM.PowerState = vmmModels.POWERSTATE_ON.Ref()
		mockConvergedClient.MockVMs.EXPECT().Get(ctx, vmUUID).Return(finalVM, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Cluster:         cluster,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			NutanixClient:   v3Client,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create mock Kubernetes client for getBootstrapData
		mockK8sClient := mockctlclient.NewMockClient(ctrl)

		// Mock Get call for bootstrap secret
		bootstrapSecret := &corev1.Secret{
			Data: map[string][]byte{
				"value": []byte("#!/bin/bash\necho 'bootstrap'"),
			},
		}
		mockK8sClient.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, key client.ObjectKey, obj *corev1.Secret, opts ...interface{}) error {
				*obj = *bootstrapSecret
				return nil
			},
		)

		// Create a scheme with the necessary types registered
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1.AddToScheme(scheme)

		// Mock Scheme.Convert for patchMachine
		mockK8sClient.EXPECT().Scheme().Return(scheme).AnyTimes()

		// Mock Patch call for patchMachine (called by syncVmUUID)
		mockK8sClient.EXPECT().Patch(ctx, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		// Create reconciler with mock client
		reconciler := &NutanixMachineReconciler{
			Client: mockK8sClient,
		}

		// Test getOrCreateVM
		vm, err := reconciler.getOrCreateVM(rctx)
		// Verify results
		if err != nil {
			t.Fatalf("getOrCreateVM failed with error: %v", err)
		}
		require.NoError(t, err)
		require.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
		assert.Equal(t, vmUUID, *vm.ExtId)
		assert.Equal(t, vmUUID, ntnxMachine.Status.VmUUID)
		// The providerID should be set with a generated UUID (not the actual VM UUID)
		assert.NotEmpty(t, ntnxMachine.Spec.ProviderID)
		assert.Contains(t, ntnxMachine.Spec.ProviderID, "nutanix://")
		// The providerID should NOT be the same as the VM's actual UUID
		assert.NotEqual(t, fmt.Sprintf("nutanix://%s", vmUUID), ntnxMachine.Spec.ProviderID)
	})
}

func TestNutanixMachineReconciler_assignAddressesToMachine(t *testing.T) {
	t.Run("should populate IP addresses from VM Nics to NutanixMachine addresses", func(t *testing.T) {
		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}

		nic1 := vmmModels.NewNic()
		nic1.NetworkInfo = vmmModels.NewNicNetworkInfo()
		ipv4Config := vmmModels.NewIpv4Config()
		ipv4Config.IpAddress = vmmCommonConfig.NewIPv4Address()
		ipv4Config.IpAddress.Value = ptr.To("10.10.10.10")
		nic1.NetworkInfo.Ipv4Config = ipv4Config

		nic2 := vmmModels.NewNic()
		nic2.NetworkInfo = vmmModels.NewNicNetworkInfo()
		ipv4Info := vmmModels.NewIpv4Info()
		ipv4Ip := vmmCommonConfig.NewIPv4Address()
		ipv4Ip.Value = ptr.To("10.10.10.11")
		ipv4Info.LearnedIpAddresses = []vmmCommonConfig.IPv4Address{*ipv4Ip}
		nic2.NetworkInfo.Ipv4Info = ipv4Info

		nics := []vmmModels.Nic{*nic1, *nic2}
		vm := vmmModels.NewVm()
		vm.Name = ptr.To("vm-name")
		vm.Nics = nics

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, vm)

		assert.Nil(t, err)
		assert.Equal(t, 1+len(nics), len(rctx.NutanixMachine.Status.Addresses))
	})

	t.Run("should fail if no IP addresses are found from Nics", func(t *testing.T) {
		rctx := &nctx.MachineContext{}

		vm := vmmModels.NewVm()
		vm.Name = ptr.To("vm-name")

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, vm)

		assert.NotNil(t, err)
	})
}

func TestNutanixMachineReconciler_VMUUIDPrioritization(t *testing.T) {
	t.Run("should prioritize Machine.Status.NodeInfo.SystemUUID over VmUUID during VM deletion", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		systemUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		vmUUID := "different-uuid-1111-2222-3333-444444444444"

		// Create NutanixMachine with VmUUID in Status
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		// Add finalizers to test removal
		ntnxMachine.Finalizers = []string{
			infrav1.NutanixMachineFinalizer,
		}

		// Create Machine with SystemUUID in NodeInfo
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Status: capiv1.MachineStatus{
				NodeInfo: &corev1.NodeSystemInfo{
					SystemUUID: systemUUID,
				},
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock VM matching the systemUUID (not VmUUID)
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.ExtId = ptr.To(systemUUID)

		mockConvergedClient := NewMockConvergedClient(ctrl)
		// Should get VM by systemUUID, NOT VmUUID
		mockConvergedClient.MockVMs.EXPECT().Get(ctx, systemUUID).Return(vm, nil)
		mockConvergedClient.MockTasks.EXPECT().List(ctx, gomock.Any()).Return([]prismModels.Task{}, nil)

		mockOperation := mockconverged.NewMockOperation[converged.NoEntity](ctrl)
		mockConvergedClient.MockVMs.EXPECT().DeleteAsync(ctx, systemUUID).Return(mockOperation, nil)
		mockOperation.EXPECT().UUID().Return("task-uuid-123").AnyTimes()

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test reconcileDelete - should use systemUUID, not VmUUID
		result, err := reconciler.reconcileDelete(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: 5 * time.Second}, result)
	})

	t.Run("should prioritize Machine.Status.NodeInfo.SystemUUID when finding VM", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		systemUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		vmUUID := "different-uuid-1111-2222-3333-444444444444"

		// Create NutanixMachine with VmUUID in Status
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		// Create Machine with SystemUUID in NodeInfo
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Status: capiv1.MachineStatus{
				NodeInfo: &corev1.NodeSystemInfo{
					SystemUUID: systemUUID,
				},
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock FindVM to return VM matching systemUUID
		expectedVm := vmmModels.NewVm()
		expectedVm.Name = ptr.To(vmName)
		expectedVm.ExtId = ptr.To(systemUUID)
		// Should get VM by systemUUID, NOT VmUUID
		mockConvergedClient.MockVMs.EXPECT().Get(ctx, systemUUID).Return(expectedVm, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test getOrCreateVM - should use systemUUID, not VmUUID
		vm, err := reconciler.getOrCreateVM(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
		assert.Equal(t, systemUUID, *vm.ExtId)
	})

	t.Run("should fall back to VmUUID when Machine.Status.NodeInfo is nil", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VmUUID in Status
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		// Create Machine WITHOUT NodeInfo (fallback scenario)
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Status: capiv1.MachineStatus{
				NodeInfo: nil,
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock FindVM to return VM matching VmUUID
		expectedVm := vmmModels.NewVm()
		expectedVm.Name = ptr.To(vmName)
		expectedVm.ExtId = ptr.To(vmUUID)
		// Should get VM by VmUUID since NodeInfo is nil
		mockConvergedClient.MockVMs.EXPECT().Get(ctx, vmUUID).Return(expectedVm, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test getOrCreateVM - should use VmUUID
		vm, err := reconciler.getOrCreateVM(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
		assert.Equal(t, vmUUID, *vm.ExtId)
	})

	t.Run("should fall back to VmUUID when Machine.Status.NodeInfo.SystemUUID is empty", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

		// Create NutanixMachine with VmUUID in Status
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		// Create Machine with empty SystemUUID (fallback scenario)
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Status: capiv1.MachineStatus{
				NodeInfo: &corev1.NodeSystemInfo{
					SystemUUID: "", // Empty
				},
			},
		}

		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
			},
		}

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Mock FindVM to return VM matching VmUUID
		expectedVm := vmmModels.NewVm()
		expectedVm.Name = ptr.To(vmName)
		expectedVm.ExtId = ptr.To(vmUUID)
		// Should get VM by VmUUID since SystemUUID is empty
		mockConvergedClient.MockVMs.EXPECT().Get(ctx, vmUUID).Return(expectedVm, nil)

		// Create machine context
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Create reconciler
		reconciler := &NutanixMachineReconciler{}

		// Test getOrCreateVM - should use VmUUID
		vm, err := reconciler.getOrCreateVM(rctx)

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
		assert.Equal(t, vmUUID, *vm.ExtId)
	})
}

func TestNutanixMachineReconciler_syncVmUUID(t *testing.T) {
	validUUID1 := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	validUUID2 := "a1b2c3d4-e5f6-4321-9876-543210fedcba"
	invalidUUID := "not-a-valid-uuid"

	t.Run("should sync VmUUID when SystemUUID is different and patchMachine succeeds", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		// Machine with SystemUUID
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1.MachineStatus{
				NodeInfo: &corev1.NodeSystemInfo{
					SystemUUID: validUUID1,
				},
			},
		}

		// NutanixMachine with different VmUUID
		nutanixMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: validUUID2,
			},
		}

		// Create a fake client
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).Build()

		rctx := &nctx.MachineContext{
			Context:        ctx,
			Machine:        machine,
			NutanixMachine: nutanixMachine,
		}

		reconciler := &NutanixMachineReconciler{
			Client: fakeClient,
		}

		// Call syncVmUUID with vmExtId
		err := reconciler.syncVmUUID(rctx, validUUID2)

		// Verify results
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(nutanixMachine.Status.VmUUID).To(Equal(validUUID1), "VmUUID should be synced to SystemUUID (prioritized over vmExtId)")
	})

	t.Run("should not update VmUUID when SystemUUID matches", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		// Machine with SystemUUID
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1.MachineStatus{
				NodeInfo: &corev1.NodeSystemInfo{
					SystemUUID: validUUID1,
				},
			},
		}

		// NutanixMachine with same VmUUID
		nutanixMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: validUUID1,
			},
		}

		// Create a fake client
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).Build()

		rctx := &nctx.MachineContext{
			Context:        ctx,
			Machine:        machine,
			NutanixMachine: nutanixMachine,
		}

		reconciler := &NutanixMachineReconciler{
			Client: fakeClient,
		}

		// Call syncVmUUID with vmExtId
		err := reconciler.syncVmUUID(rctx, validUUID2)

		// Verify results
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(nutanixMachine.Status.VmUUID).To(Equal(validUUID1), "VmUUID should remain unchanged when it already matches SystemUUID")
	})

	t.Run("should fall back to vmExtId when SystemUUID is not available", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		// Machine without NodeInfo
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1.MachineStatus{
				NodeInfo: nil,
			},
		}

		// NutanixMachine with empty VmUUID
		nutanixMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: "",
			},
		}

		// Create a fake client
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).Build()

		rctx := &nctx.MachineContext{
			Context:        ctx,
			Machine:        machine,
			NutanixMachine: nutanixMachine,
		}

		reconciler := &NutanixMachineReconciler{
			Client: fakeClient,
		}

		// Call syncVmUUID with vmExtId
		err := reconciler.syncVmUUID(rctx, validUUID1)

		// Verify results - should set VmUUID from vmExtId
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(nutanixMachine.Status.VmUUID).To(Equal(validUUID1), "VmUUID should be set from vmExtId when SystemUUID is not available")
	})

	t.Run("should fall back to vmExtId when SystemUUID is empty", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		// Machine with empty SystemUUID
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1.MachineStatus{
				NodeInfo: &corev1.NodeSystemInfo{
					SystemUUID: "",
				},
			},
		}

		// NutanixMachine with empty VmUUID
		nutanixMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: "",
			},
		}

		// Create a fake client
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).Build()

		rctx := &nctx.MachineContext{
			Context:        ctx,
			Machine:        machine,
			NutanixMachine: nutanixMachine,
		}

		reconciler := &NutanixMachineReconciler{
			Client: fakeClient,
		}

		// Call syncVmUUID with vmExtId
		err := reconciler.syncVmUUID(rctx, validUUID1)

		// Verify results - should set VmUUID from vmExtId
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(nutanixMachine.Status.VmUUID).To(Equal(validUUID1), "VmUUID should be set from vmExtId when SystemUUID is empty")
	})

	t.Run("should fall back to vmExtId when SystemUUID is invalid", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		// Machine with invalid SystemUUID
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1.MachineStatus{
				NodeInfo: &corev1.NodeSystemInfo{
					SystemUUID: invalidUUID,
				},
			},
		}

		// NutanixMachine with empty VmUUID
		nutanixMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: "",
			},
		}

		// Create a fake client
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).Build()

		rctx := &nctx.MachineContext{
			Context:        ctx,
			Machine:        machine,
			NutanixMachine: nutanixMachine,
		}

		reconciler := &NutanixMachineReconciler{
			Client: fakeClient,
		}

		// Call syncVmUUID with vmExtId
		err := reconciler.syncVmUUID(rctx, validUUID1)

		// Verify results - should set VmUUID from vmExtId when SystemUUID is invalid
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(nutanixMachine.Status.VmUUID).To(Equal(validUUID1), "VmUUID should be set from vmExtId when SystemUUID is invalid")
	})

	t.Run("should sync VmUUID from empty to SystemUUID", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		// Machine with SystemUUID
		machine := &capiv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1.MachineStatus{
				NodeInfo: &corev1.NodeSystemInfo{
					SystemUUID: validUUID1,
				},
			},
		}

		// NutanixMachine with empty VmUUID
		nutanixMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: "",
			},
		}

		// Create a fake client
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).Build()

		rctx := &nctx.MachineContext{
			Context:        ctx,
			Machine:        machine,
			NutanixMachine: nutanixMachine,
		}

		reconciler := &NutanixMachineReconciler{
			Client: fakeClient,
		}

		// Call syncVmUUID with vmExtId (shouldn't be used since SystemUUID is valid)
		err := reconciler.syncVmUUID(rctx, validUUID2)

		// Verify results
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(nutanixMachine.Status.VmUUID).To(Equal(validUUID1), "VmUUID should be set to SystemUUID (not vmExtId)")
	})
}
