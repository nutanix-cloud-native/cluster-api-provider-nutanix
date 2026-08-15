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
	projectModels "github.com/nutanix/ntnx-api-golang-clients/multidomain-go-client/v4/models/multidomain/v4/config"
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
	capiv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // suppress complaining on Deprecated package
	capiv1beta2 "sigs.k8s.io/cluster-api/api/core/v1beta2"
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
			machine     *capiv1beta2.Machine
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
			machine = &capiv1beta2.Machine{ObjectMeta: metav1.ObjectMeta{
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

				machine.Spec.FailureDomain = fdObj.Name
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
				machine.Spec.FailureDomain = fdObj.Name
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

				machine.Spec.FailureDomain = fdObj.Name
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

				machine.Spec.FailureDomain = fdObj.Name
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
			})
		})

		Context("Validates machine config", func() {
			It("should error if no failure domain is present on machine and no subnets are passed", func() {
				err := reconciler.validateMachineConfig(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
				}, nil, nil)
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
				}, nil, nil)
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
				}, nil, nil)
				g.Expect(err).ToNot(HaveOccurred())
			})
			It("returns error if invalid machine config is passed with reference to not-exist failure domain", func() {
				machine.Spec.FailureDomain = r
				err := reconciler.validateMachineConfig(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
				}, nil, nil)
				g.Expect(err).To(HaveOccurred())
			})
		})

		Context("Gets the subnet and PE UUIDs", func() {
			It("should error if nil machine context is passed", func() {
				_, _, err := reconciler.GetSubnetAndPEUUIDs(nil, nil, nil)
				g.Expect(err).To(HaveOccurred())
			})
			It("should error if machine has no failure domain and Prism Element info is missing on nutanix machine", func() {
				_, _, err := reconciler.GetSubnetAndPEUUIDs(&nctx.MachineContext{
					Context:        ctx,
					NutanixMachine: ntnxMachine,
					Machine:        machine,
					NutanixCluster: ntnxCluster,
				}, nil, nil)
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
				}, nil, nil)
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
				}, nil, nil)
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
				machine.Spec.FailureDomain = "failure-domain"
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
	err = capiv1beta2.AddToScheme(scheme)
	require.NoError(t, err)

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetAPIReader().Return(nil).AnyTimes()
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
	mgr.EXPECT().GetAPIReader().Return(mockClient).AnyTimes()

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
	err = capiv1beta2.AddToScheme(scheme)
	require.NoError(t, err)

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetAPIReader().Return(nil).AnyTimes()
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
	mgr.EXPECT().GetAPIReader().Return(mockClient).AnyTimes()

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
	err = capiv1beta2.AddToScheme(scheme)
	require.NoError(t, err)

	cache := mockctlclient.NewMockCache(mockCtrl)

	mgr := mockctlclient.NewMockManager(mockCtrl)
	mgr.EXPECT().GetCache().Return(cache).AnyTimes()
	mgr.EXPECT().GetScheme().Return(scheme).AnyTimes()
	mgr.EXPECT().GetAPIReader().Return(nil).AnyTimes()
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
	mgr.EXPECT().GetAPIReader().Return(mockClient).AnyTimes()

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
		{
			name: "dataDiskErrorDuplicateDeviceIndex",
			dataDisks: func() []infrav1.NutanixMachineVMDisk {
				return []infrav1.NutanixMachineVMDisk{
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
							DeviceIndex: 10,
						},
						StorageConfig: &infrav1.NutanixMachineVMStorageConfig{
							DiskMode: infrav1.NutanixMachineDiskModeStandard,
							StorageContainer: &infrav1.NutanixResourceIdentifier{
								UUID: ptr.To("06b1ce03-f384-4488-9ba1-ae17ebcf1f91"),
								Type: infrav1.NutanixIdentifierUUID,
							},
						},
					},
					{
						DiskSize: resource.MustParse("20Gi"),
						DeviceProperties: &infrav1.NutanixMachineVMDiskDeviceProperties{
							DeviceType:  infrav1.NutanixMachineDiskDeviceTypeDisk,
							AdapterType: infrav1.NutanixMachineDiskAdapterTypeSCSI,
							DeviceIndex: 10,
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
			description: "Verify NutanixMachine with duplicate data disk device index error",
			stepDesc:    "should error on validation due to duplicate device index",
			errCheck: func(g *WithT, err error) {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("index '10' is already in use"))
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
				machine     *capiv1beta2.Machine
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

				machine = &capiv1beta2.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "default",
						Labels: map[string]string{
							"cluster.x-k8s.io/cluster-name": "test",
						},
					},
					Spec: capiv1beta2.MachineSpec{
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
					}, nil, nil)
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
	// gomock probes variadic args both element-by-element and as the full slice;
	// only the []converged.ODataOption form is meaningful here.
	actualODataOptions, ok := actual.([]converged.ODataOption)
	if !ok {
		return false
	}

	v4ODataOptions, err := v4Converged.OptsToV4ODataParams(actualODataOptions...)
	if err != nil {
		return false
	}

	if v4ODataOptions.Filter == nil {
		return false
	}

	return strings.Contains(*v4ODataOptions.Filter, m.ContainsExtId)
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

	defaultMachine := &capiv1beta2.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "test",
			},
		},
		Spec: capiv1beta2.MachineSpec{
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
		name          string
		fixtures      func(*gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client)
		resourceGroup *projectModels.ResourceGroup
		wantDisksLen  int
		wantErr       bool
	}{
		{
			name:         "return get disk list",
			wantDisksLen: 3,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
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
			name:          "return get disk list resolving the storage container via the project resource group",
			wantDisksLen:  3,
			resourceGroup: &projectModels.ResourceGroup{ExtId: ptr.To("rg-uuid")},
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
				convergedClientMock := NewMockConvergedClient(mockCtrl)
				convergedClientMock.MockImages.EXPECT().Get(gomock.Any(), *defaultSystemImage.ExtId).Return(defaultSystemImage, nil).MinTimes(1)
				convergedClientMock.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						*defaultSystemImage,
						*defaultBootstrapImage,
					}, nil).MinTimes(1)
				convergedClientMock.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil).MinTimes(1)

				// With a resource group the storage container must be resolved from the
				// project's resource group (constrained to the machine's PE) instead of
				// the cluster-wide StorageContainers.List API.
				convergedClientMock.MockResourceGroups.EXPECT().ListStorageContainers(gomock.Any(), "rg-uuid").Return(
					[]converged.StorageContainerInfo{
						{
							ExtId: "06b1ce03-f384-4488-9ba1-ae17ebcf1f91",
							Name:  "data-container",
							PrismElement: converged.PrismElementInfo{
								ExtId: "00062e56-b9ac-7253-1946-7cc25586eeee",
								Name:  "pe_cluster",
							},
						},
					}, nil)

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, convergedClientMock.Client
			},
		},
		{
			name:          "return an error if the storage container is not authorized in the project resource group",
			wantErr:       true,
			resourceGroup: &projectModels.ResourceGroup{ExtId: ptr.To("rg-uuid")},
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
				convergedClientMock := NewMockConvergedClient(mockCtrl)
				convergedClientMock.MockImages.EXPECT().Get(gomock.Any(), *defaultSystemImage.ExtId).Return(defaultSystemImage, nil).MinTimes(1)
				convergedClientMock.MockImages.EXPECT().List(gomock.Any(), gomock.Any()).Return(
					[]imageModels.Image{
						*defaultSystemImage,
						*defaultBootstrapImage,
					}, nil).MinTimes(1)
				convergedClientMock.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil).MinTimes(1)

				// The resource group contains the storage container but on a different PE
				// than the machine's, so it must be treated as not authorized.
				convergedClientMock.MockResourceGroups.EXPECT().ListStorageContainers(gomock.Any(), "rg-uuid").Return(
					[]converged.StorageContainerInfo{
						{
							ExtId: "06b1ce03-f384-4488-9ba1-ae17ebcf1f91",
							Name:  "data-container",
							PrismElement: converged.PrismElementInfo{
								ExtId: "11111111-1111-1111-1111-111111111111",
								Name:  "other_pe",
							},
						},
					}, nil)

				return defaultNtnxMachine, defaultMachine, defaultNtnxCluster, convergedClientMock.Client
			},
		},
		{
			name:    "return an error if the bootstrap disk is not found",
			wantErr: true,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
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
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
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
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
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
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
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
		{
			// Regression test: BootstrapRef is nil for part of the reconcile window (it's
			// populated later in Reconcile, not guaranteed set by the time getOrCreateVM's
			// disk-building runs), and getDiskList used to dereference it unconditionally,
			// panicking with a nil pointer dereference instead of just skipping the
			// image-bootstrap cdrom branch.
			name:         "does not panic when BootstrapRef is nil",
			wantDisksLen: 1,
			fixtures: func(mockCtrl *gomock.Controller) (*infrav1.NutanixMachine, *capiv1beta2.Machine, *infrav1.NutanixCluster, *v4Converged.Client) {
				ntnxMachine := defaultNtnxMachine.DeepCopy()
				ntnxMachine.Spec.BootstrapRef = nil
				ntnxMachine.Spec.DataDisks = nil

				convergedClientMock := NewMockConvergedClient(mockCtrl)
				convergedClientMock.MockImages.EXPECT().Get(gomock.Any(), *defaultSystemImage.ExtId).Return(defaultSystemImage, nil).MinTimes(1)
				convergedClientMock.MockTasks.EXPECT().List(gomock.Any(), gomock.Any()).Return([]prismModels.Task{}, nil).MinTimes(1)

				return ntnxMachine, defaultMachine, defaultNtnxCluster, convergedClientMock.Client
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			ntnxMachine, machine, ntnxCluster, convergedClient := tc.fixtures(mockCtrl)

			testProjectExtID := "test-project-ext-id"
			disks, cdRoms, err := getDiskList(&nctx.MachineContext{
				Context:         context.Background(),
				NutanixMachine:  ntnxMachine,
				Machine:         machine,
				NutanixCluster:  ntnxCluster,
				ConvergedClient: convergedClient,
			}, *ntnxMachine.Spec.Cluster.UUID, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, tc.resourceGroup)

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: k8sVersion,
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
		testProjectExtID := "test-project-ext-id"
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: k8sVersion,
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
		testProjectExtID := "test-project-ext-id"
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: k8sVersion,
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
		testProjectExtID := "test-project-ext-id"
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: k8sVersion,
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
		testProjectExtID := "test-project-ext-id"
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: k8sVersion,
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
		testProjectExtID := "test-project-ext-id"
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: k8sVersion,
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
		testProjectExtID := "test-project-ext-id"
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Test getSystemDisk
		systemDisk, err := getSystemDisk(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})

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

		machine := &capiv1beta2.Machine{
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
		testProjectExtID := "test-project-ext-id"
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
			_, _ = getSystemDisk(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})
		})
	})
}

func TestNutanixMachineReconciler_ReconcileDelete(t *testing.T) {
	t.Run("should handle empty VM UUID by removing finalizers", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with empty VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				// VM UUID is empty
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
		}

		// Add finalizers to test removal
		ntnxMachine.Finalizers = []string{
			infrav1.NutanixMachineFinalizer,
			infrav1.DeprecatedNutanixMachineFinalizer,
		}

		machine := &capiv1beta2.Machine{
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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
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

		machine := &capiv1beta2.Machine{
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
		// Return a not found error to simulate VM not existing
		mockConvergedClient.MockVMs.EXPECT().Get(gomock.Any(), vmUUID).Return(nil,
			&converged.APIError{Kind: converged.ErrNotFound, Message: "vm not found"})

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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1beta2.Machine{
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
		vm.Project = vmmModels.NewProjectReference()
		vm.Project.ExtId = &projectUUID

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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1beta2.Machine{
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
		vm.Project = vmmModels.NewProjectReference()
		vm.Project.ExtId = &projectUUID

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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1beta2.Machine{
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
		vm.Project = vmmModels.NewProjectReference()
		vm.Project.ExtId = &projectUUID

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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1beta2.Machine{
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
		vm.Project = vmmModels.NewProjectReference()
		vm.Project.ExtId = &projectUUID

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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1beta2.Machine{
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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1beta2.Machine{
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
		vm.Project = vmmModels.NewProjectReference()
		vm.Project.ExtId = &projectUUID

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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1beta2.Machine{
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
		vm.Project = vmmModels.NewProjectReference()
		vm.Project.ExtId = &projectUUID

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
		projectUUID := "test-project-uuid"

		// Create NutanixMachine with VM UUID
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				ProviderID: fmt.Sprintf("nutanix://%s", vmUUID),
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
			},
			Status: infrav1.NutanixMachineStatus{
				VmUUID: vmUUID,
			},
		}

		machine := &capiv1beta2.Machine{
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
		vm.Project = vmmModels.NewProjectReference()
		vm.Project.ExtId = &projectUUID

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

func TestNutanixMachineReconciler_getOrMintVMCreationRequestID(t *testing.T) {
	t.Run("mints and durably persists a new request ID via a patch that captures the annotation diff", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
		}

		mockK8sClient := mockctlclient.NewMockClient(ctrl)

		var appliedPatch []byte
		mockK8sClient.EXPECT().Patch(ctx, ntnxMachine, gomock.Any()).DoAndReturn(
			func(_ context.Context, obj client.Object, patch client.Patch, _ ...client.PatchOption) error {
				data, err := patch.Data(obj)
				require.NoError(t, err)
				appliedPatch = data
				return nil
			},
		)

		reconciler := &NutanixMachineReconciler{Client: mockK8sClient}
		rctx := &nctx.MachineContext{Context: ctx, NutanixMachine: ntnxMachine}

		requestID, err := reconciler.getOrMintVMCreationRequestID(rctx)
		require.NoError(t, err)

		_, err = uuid.Parse(requestID)
		require.NoError(t, err, "minted request ID should be a valid UUID")

		// The patch sent to the API server must actually contain the new annotation - if the
		// diff were computed against a baseline captured after the mutation, this would be
		// empty and the annotation would never become durable.
		assert.Contains(t, string(appliedPatch), VMCreationRequestIDAnnotation)
		assert.Contains(t, string(appliedPatch), requestID)
		assert.Equal(t, requestID, ntnxMachine.Annotations[VMCreationRequestIDAnnotation])
	})

	t.Run("reuses a previously persisted request ID instead of minting or patching again", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		existingRequestID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
				Annotations: map[string]string{
					VMCreationRequestIDAnnotation: existingRequestID,
				},
			},
		}

		// No Patch expectation is set: a second reconcile that finds the annotation already
		// persisted (e.g. after the first Create failed/timed out) must reuse it as-is rather
		// than minting a new one, so the retried Create stays idempotent against the same task.
		mockK8sClient := mockctlclient.NewMockClient(ctrl)

		reconciler := &NutanixMachineReconciler{Client: mockK8sClient}
		rctx := &nctx.MachineContext{Context: ctx, NutanixMachine: ntnxMachine}

		requestID, err := reconciler.getOrMintVMCreationRequestID(rctx)
		require.NoError(t, err)
		assert.Equal(t, existingRequestID, requestID)
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

		machine := &capiv1beta2.Machine{
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
		testProjectExtID := "test-project-ext-id"

		// Mock FindVM to return existing VM (already powered on)
		expectedVm := vmmModels.NewVm()
		expectedVm.Name = ptr.To(vmName)
		expectedVm.ExtId = ptr.To(vmUUID)
		expectedVm.PowerState = vmmModels.POWERSTATE_ON.Ref()
		expectedVm.Project = vmmModels.NewProjectReference()
		expectedVm.Project.ExtId = &testProjectExtID
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
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

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

		machine := &capiv1beta2.Machine{
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
		testProjectExtID := "test-project-ext-id"

		// Mock FindVMByName (already powered on)
		expectedVM := vmmModels.NewVm()
		expectedVM.Name = ptr.To(vmName)
		expectedVM.ExtId = ptr.To(vmUUID)
		expectedVM.PowerState = vmmModels.POWERSTATE_ON.Ref()
		expectedVM.Project = vmmModels.NewProjectReference()
		expectedVM.Project.ExtId = &testProjectExtID
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
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
	})

	t.Run("should return existing VM even when it is powered off", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		vmUUID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"

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

		machine := &capiv1beta2.Machine{
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
		testProjectExtID := "test-project-ext-id"

		// Mock FindVM to return existing VM that is OFF
		existingVm := vmmModels.NewVm()
		existingVm.Name = ptr.To(vmName)
		existingVm.ExtId = ptr.To(vmUUID)
		existingVm.PowerState = vmmModels.POWERSTATE_OFF.Ref()
		existingVm.Project = vmmModels.NewProjectReference()
		existingVm.Project.ExtId = &testProjectExtID
		mockConvergedClient.MockVMs.EXPECT().Get(ctx, vmUUID).Return(existingVm, nil)
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		reconciler := &NutanixMachineReconciler{}
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

		assert.NoError(t, err)
		assert.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
		assert.Equal(t, vmUUID, *vm.ExtId)
		assert.Equal(t, vmmModels.POWERSTATE_OFF, *vm.PowerState)
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

		machine := &capiv1beta2.Machine{
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
		testProjectExtID := "test-project-ext-id"
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
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: "v1.28.0",
			},
			// SystemUUID is not set initially - it only gets set after the VM is created
			// and the node reports its SystemUUID. For this test, we're creating a new VM.
		}

		cluster := &capiv1beta2.Cluster{
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
			// Pre-set so markClusterCategoryCreated short-circuits; the k8s mock
			// client isn't configured to handle the cluster status Patch path.
			Status: infrav1.NutanixClusterStatus{
				Conditions: capiv1beta1.Conditions{
					{Type: infrav1.ClusterCategoryCreatedCondition, Status: corev1.ConditionTrue},
				},
			},
		}

		// Create mock clients
		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockV3Client := mocknutanixv3.NewMockService(ctrl)
		v3Client := &prismclientv3.Client{V3: mockV3Client}

		// Mock FindVM to return nil (VM not found)
		// Since SystemUUID is not set, FindVMByName is called - return empty list
		mockConvergedClient.MockVMs.EXPECT().List(ctx, gomock.Any()).Return([]vmmModels.Vm{}, nil)

		// Mock PE resolution via the project's resource group (called by
		// GetSubnetAndPEUUIDs -> GetPEUUID -> resolvePEFromResourceGroup). With a
		// resource group, the PE must be looked up from the resource group's placement
		// targets instead of the cluster-wide Clusters.Get API.
		mockConvergedClient.MockResourceGroups.EXPECT().ListPrismElements(ctx, "rg-uuid").Return(
			[]converged.PrismElementInfo{
				{ExtId: peUUID, Name: "pe_cluster"},
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
		// Context is gomock.Any() here, not ctx, because getOrCreateVM wraps it with
		// v4Converged.WithRequestID for the vm-creation-request-id idempotency key.
		mockConvergedClient.MockVMs.EXPECT().Create(gomock.Any(), gomock.Any()).Return(createdVM, nil)

		// Create machine context (PC 7.5 uses V3 project API, so ListAllProject mock is used)
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Cluster:         cluster,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			NutanixClient:   v3Client,
			ConvergedClient: mockConvergedClient.Client,
			PCVersion:       "pc.7.5.0.5",
		}

		// Use a real fake client (not a gomock) so getOrCreateVM's chain of patchMachine
		// calls (via getOrMintVMCreationRequestID and syncVmUUID) exercise the actual
		// v1beta1patch.Helper diffing/patching logic, including the status subresource -
		// a hand-rolled Status()/Patch() mock would need to reimplement that logic to be
		// trustworthy, and would silently stop testing anything the moment it diverged.
		bootstrapSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bootstrap-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"value": []byte("#!/bin/bash\necho 'bootstrap'"),
			},
		}

		// Create a scheme with the necessary types registered
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1beta2.AddToScheme(scheme)
		_ = corev1.AddToScheme(scheme)

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(ntnxMachine, bootstrapSecret).
			WithStatusSubresource(ntnxMachine).
			Build()

		// Create reconciler with fake client
		reconciler := &NutanixMachineReconciler{
			Client: fakeClient,
		}

		// Test getOrCreateVM - use the project UUID from the test setup, with a
		// resolved resource group so PE resolution goes through the project-scoped path.
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &projectUUID, Name: &projectName}, &projectModels.ResourceGroup{ExtId: ptr.To("rg-uuid")})
		// Verify results
		if err != nil {
			t.Fatalf("getOrCreateVM failed with error: %v", err)
		}
		require.NoError(t, err)
		require.NotNil(t, vm)
		assert.Equal(t, vmName, *vm.Name)
		assert.Equal(t, vmUUID, *vm.ExtId)
		assert.Equal(t, vmUUID, ntnxMachine.Status.VmUUID)
		// The providerID should be set using the actual VM UUID
		assert.Equal(t, fmt.Sprintf("nutanix://%s", vmUUID), ntnxMachine.Spec.ProviderID)

		// Re-fetch independently to confirm the request ID, VmUUID and providerID were
		// actually durably persisted via patchMachine, not just mutated on the in-memory
		// object (which would still show these values even if the underlying patch calls
		// silently computed an empty diff and never reached the server).
		persisted := &infrav1.NutanixMachine{}
		require.NoError(t, fakeClient.Get(ctx, client.ObjectKeyFromObject(ntnxMachine), persisted))
		assert.Equal(t, vmUUID, persisted.Status.VmUUID)
		assert.Equal(t, fmt.Sprintf("nutanix://%s", vmUUID), persisted.Spec.ProviderID)
		assert.NotEmpty(t, persisted.Annotations[VMCreationRequestIDAnnotation])
	})

	t.Run("should set failure status when category lookup returns not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		peUUID := "00056024-f4f2-a6f6-0000-00000000e7f4"
		subnetUUID := "b8c6d9f0-4c5e-4c5e-8c5e-4c5e4c5e4c5e"
		imageUUID := "c5e4c5e4-c5e4-c5e4-c5e4-c5e4c5e4c5e4"
		clusterName := "test-cluster"

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: "v1.28.0",
			},
		}

		cluster := &capiv1beta2.Cluster{
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
			// Pre-set so markClusterCategoryCreationFailed short-circuits; the
			// reconciler here has no k8s client to patch the cluster status.
			Status: infrav1.NutanixClusterStatus{
				Conditions: capiv1beta1.Conditions{
					{Type: infrav1.ClusterCategoryCreatedCondition, Status: corev1.ConditionTrue},
				},
			},
		}

		mockConvergedClient := NewMockConvergedClient(ctrl)

		// VM not found, proceed to create flow.
		mockConvergedClient.MockVMs.EXPECT().List(ctx, gomock.Any()).Return([]vmmModels.Vm{}, nil)
		mockConvergedClient.MockClusters.EXPECT().Get(ctx, peUUID).Return(&clustermgmtconfig.Cluster{
			ExtId: &peUUID,
		}, nil)
		mockConvergedClient.MockSubnets.EXPECT().Get(ctx, subnetUUID).Return(&subnetModels.Subnet{
			ExtId: &subnetUUID,
		}, nil)
		mockConvergedClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return(nil,
			&converged.APIError{Kind: converged.ErrNotFound, Message: "category not found"},
		).AnyTimes()

		testProjectExtID := "test-project-ext-id"
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Cluster:         cluster,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Mock Kubernetes client for the vm-creation-request-id annotation patch
		// getOrMintVMCreationRequestID issues before the create flow proceeds.
		mockK8sClient := mockctlclient.NewMockClient(ctrl)
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1beta2.AddToScheme(scheme)
		mockK8sClient.EXPECT().Scheme().Return(scheme).AnyTimes()
		mockK8sClient.EXPECT().Patch(ctx, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		reconciler := &NutanixMachineReconciler{Client: mockK8sClient}
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

		require.Error(t, err)
		assert.Nil(t, vm)
		require.NotNil(t, ntnxMachine.Status.FailureReason)
		assert.Equal(t, createErrorFailureReason, *ntnxMachine.Status.FailureReason)
		require.NotNil(t, ntnxMachine.Status.FailureMessage)
		assert.Contains(t, *ntnxMachine.Status.FailureMessage, "category spec")
	})

	t.Run("should not set failure status when category lookup returns internal error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		ctx := context.Background()
		vmName := "test-vm"
		peUUID := "00056024-f4f2-a6f6-0000-00000000e7f4"
		subnetUUID := "b8c6d9f0-4c5e-4c5e-8c5e-4c5e4c5e4c5e"
		imageUUID := "c5e4c5e4-c5e4-c5e4-c5e4-c5e4c5e4c5e4"
		clusterName := "test-cluster"

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

		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Spec: capiv1beta2.MachineSpec{
				Version: "v1.28.0",
			},
		}

		cluster := &capiv1beta2.Cluster{
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
			// Pre-set so markClusterCategoryCreationFailed short-circuits; the
			// reconciler here has no k8s client to patch the cluster status.
			Status: infrav1.NutanixClusterStatus{
				Conditions: capiv1beta1.Conditions{
					{Type: infrav1.ClusterCategoryCreatedCondition, Status: corev1.ConditionTrue},
				},
			},
		}

		mockConvergedClient := NewMockConvergedClient(ctrl)

		// VM not found, proceed to create flow.
		mockConvergedClient.MockVMs.EXPECT().List(ctx, gomock.Any()).Return([]vmmModels.Vm{}, nil)
		mockConvergedClient.MockClusters.EXPECT().Get(ctx, peUUID).Return(&clustermgmtconfig.Cluster{
			ExtId: &peUUID,
		}, nil)
		mockConvergedClient.MockSubnets.EXPECT().Get(ctx, subnetUUID).Return(&subnetModels.Subnet{
			ExtId: &subnetUUID,
		}, nil)
		mockConvergedClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return(nil,
			&converged.APIError{Kind: converged.ErrInternal, Message: "pc internal error"},
		).AnyTimes()

		testProjectExtID := "test-project-ext-id"
		rctx := &nctx.MachineContext{
			Context:         ctx,
			Cluster:         cluster,
			Machine:         machine,
			NutanixMachine:  ntnxMachine,
			NutanixCluster:  ntnxCluster,
			ConvergedClient: mockConvergedClient.Client,
		}

		// Mock Kubernetes client for the vm-creation-request-id annotation patch
		// getOrMintVMCreationRequestID issues before the create flow proceeds.
		mockK8sClient := mockctlclient.NewMockClient(ctrl)
		scheme := runtime.NewScheme()
		_ = infrav1.AddToScheme(scheme)
		_ = capiv1beta2.AddToScheme(scheme)
		mockK8sClient.EXPECT().Scheme().Return(scheme).AnyTimes()
		mockK8sClient.EXPECT().Patch(ctx, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		reconciler := &NutanixMachineReconciler{Client: mockK8sClient}
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

		require.Error(t, err)
		assert.Nil(t, vm)
		assert.Nil(t, ntnxMachine.Status.FailureReason)
		assert.Nil(t, ntnxMachine.Status.FailureMessage)
	})
}

func TestNutanixMachineReconciler_addCustomAttributes(t *testing.T) {
	const (
		vmName = "test-vm"
		vmUUID = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	)

	newVM := func(customAttrs []string) *vmmModels.Vm {
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.ExtId = ptr.To(vmUUID)
		vm.CustomAttributes = customAttrs
		return vm
	}

	newMachineContext := func(ctrl *gomock.Controller) (*nctx.MachineContext, *MockConvergedClientWrapper) {
		mockConvergedClient := NewMockConvergedClient(ctrl)
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
		}
		return &nctx.MachineContext{
			Context:         context.Background(),
			NutanixMachine:  ntnxMachine,
			ConvergedClient: mockConvergedClient.Client,
		}, mockConvergedClient
	}

	t.Run("should skip API call when custom attribute already present", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, _ := newMachineContext(ctrl)
		vm := newVM([]string{"providerid:" + vmUUID})

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addCustomAttributes(rctx, vm)
		assert.NoError(t, err)
	})

	t.Run("should call API when custom attribute is missing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, mockClient := newMachineContext(ctrl)
		vm := newVM(nil)

		expectedAttr := []string{"providerid:" + vmUUID}
		updatedVM := newVM(expectedAttr)
		mockClient.MockVMs.EXPECT().
			AddVmCustomAttributes(rctx.Context, vmUUID, expectedAttr).
			Return(updatedVM, nil)

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addCustomAttributes(rctx, vm)
		assert.NoError(t, err)
	})

	t.Run("should call API when different custom attributes exist", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, mockClient := newMachineContext(ctrl)
		vm := newVM([]string{"other:attribute"})

		expectedAttr := []string{"providerid:" + vmUUID}
		updatedVM := newVM(append([]string{"other:attribute"}, expectedAttr...))
		mockClient.MockVMs.EXPECT().
			AddVmCustomAttributes(rctx.Context, vmUUID, expectedAttr).
			Return(updatedVM, nil)

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addCustomAttributes(rctx, vm)
		assert.NoError(t, err)
	})

	t.Run("should return error on non-retryable API failure and set failure status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, mockClient := newMachineContext(ctrl)
		vm := newVM(nil)

		apiErr := &converged.APIError{Message: "not found"}
		mockClient.MockVMs.EXPECT().
			AddVmCustomAttributes(rctx.Context, vmUUID, gomock.Any()).
			Return(nil, apiErr)

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addCustomAttributes(rctx, vm)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update custom attributes")
		assert.NotNil(t, rctx.NutanixMachine.Status.FailureReason)
		assert.NotNil(t, rctx.NutanixMachine.Status.FailureMessage)
	})

	t.Run("should return error on retryable API failure without setting failure status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, mockClient := newMachineContext(ctrl)
		vm := newVM(nil)

		networkErr := fmt.Errorf("connection timeout")
		mockClient.MockVMs.EXPECT().
			AddVmCustomAttributes(rctx.Context, vmUUID, gomock.Any()).
			Return(nil, networkErr)

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addCustomAttributes(rctx, vm)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update custom attributes")
		assert.Nil(t, rctx.NutanixMachine.Status.FailureReason)
		assert.Nil(t, rctx.NutanixMachine.Status.FailureMessage)
	})
}

func TestNutanixMachineReconciler_getVMProfileForDeploy_ErrorHandling(t *testing.T) {
	const (
		vmName        = "test-vm"
		vmProfileUUID = "a19f0e7a-4a53-4edc-8da7-9f5a48ea8a01"
		projectExtID  = "00000000-0000-0000-0000-0000000000aa"
	)

	effectiveProject := &nctx.ProjectInfo{ExtID: ptr.To(projectExtID), Name: ptr.To("test-project")}

	newMachineContext := func(ctrl *gomock.Controller) (*nctx.MachineContext, *MockConvergedClientWrapper) {
		mockConvergedClient := NewMockConvergedClient(ctrl)
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				VMProfile: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: ptr.To(vmProfileUUID),
				},
			},
		}

		return &nctx.MachineContext{
			Context:         context.Background(),
			NutanixMachine:  ntnxMachine,
			ConvergedClient: mockConvergedClient.Client,
		}, mockConvergedClient
	}

	t.Run("sets failure status when VM profile has no UUID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, mockClient := newMachineContext(ctrl)
		// Profile is usable by the project but has no UUID set.
		profileWithoutUUID := vmmModels.NewVmProfile()
		profileWithoutUUID.ProjectExtId = ptr.To(projectExtID)
		mockClient.MockVMProfiles.EXPECT().
			Get(rctx.Context, vmProfileUUID).
			Return(profileWithoutUUID, nil)

		reconciler := &NutanixMachineReconciler{}
		_, _, err := reconciler.getVMProfileForDeploy(rctx, vmName, effectiveProject)
		require.Error(t, err)
		assert.NotNil(t, rctx.NutanixMachine.Status.FailureReason)
		assert.NotNil(t, rctx.NutanixMachine.Status.FailureMessage)
	})

	t.Run("does not set failure status on retryable VM profile lookup error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, mockClient := newMachineContext(ctrl)
		mockClient.MockVMProfiles.EXPECT().
			Get(rctx.Context, vmProfileUUID).
			Return(nil, fmt.Errorf("connection timeout"))

		reconciler := &NutanixMachineReconciler{}
		_, _, err := reconciler.getVMProfileForDeploy(rctx, vmName, effectiveProject)
		require.Error(t, err)
		assert.Nil(t, rctx.NutanixMachine.Status.FailureReason)
		assert.Nil(t, rctx.NutanixMachine.Status.FailureMessage)
	})
}

func TestNutanixMachineReconciler_buildDeployParamsFromProfile_CategoryErrorHandling(t *testing.T) {
	const (
		vmName      = "test-vm"
		clusterName = "test-cluster"
		peUUID      = "00056024-f4f2-a6f6-0000-00000000e7f4"
	)

	newMachineContext := func(ctrl *gomock.Controller) (*nctx.MachineContext, *MockConvergedClientWrapper) {
		mockConvergedClient := NewMockConvergedClient(ctrl)
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				BootstrapRef: &corev1.ObjectReference{
					Kind: infrav1.NutanixMachineBootstrapRefKindImage,
				},
			},
		}
		cluster := &capiv1beta2.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterName,
			},
		}
		// Pre-set ClusterCategoryCreatedCondition=True so the markCluster* helpers
		// short-circuit; the reconciler in these tests has no k8s client.
		ntnxCluster := &infrav1.NutanixCluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: "default"},
			Status: infrav1.NutanixClusterStatus{
				Conditions: capiv1beta1.Conditions{
					{Type: infrav1.ClusterCategoryCreatedCondition, Status: corev1.ConditionTrue},
				},
			},
		}

		return &nctx.MachineContext{
			Context:         context.Background(),
			Cluster:         cluster,
			NutanixCluster:  ntnxCluster,
			NutanixMachine:  ntnxMachine,
			ConvergedClient: mockConvergedClient.Client,
		}, mockConvergedClient
	}

	vmProfileNoNics := &vmmModels.VmProfile{}

	t.Run("sets failure status on non-retryable category API error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, mockClient := newMachineContext(ctrl)
		mockClient.MockCategories.EXPECT().
			List(rctx.Context, gomock.Any()).
			Return(nil, &converged.APIError{Message: "not found"}).
			AnyTimes()

		testProjectExtID := "test-project-ext-id"
		reconciler := &NutanixMachineReconciler{}
		_, err := reconciler.buildDeployParamsFromProfile(rctx, vmName, peUUID, nil, vmProfileNoNics, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})
		require.Error(t, err)
		assert.NotNil(t, rctx.NutanixMachine.Status.FailureReason)
		assert.NotNil(t, rctx.NutanixMachine.Status.FailureMessage)
	})

	t.Run("does not set failure status on retryable category API error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		rctx, mockClient := newMachineContext(ctrl)
		mockClient.MockCategories.EXPECT().
			List(rctx.Context, gomock.Any()).
			Return(nil, fmt.Errorf("connection timeout")).
			AnyTimes()

		testProjectExtID := "test-project-ext-id"
		reconciler := &NutanixMachineReconciler{}
		_, err := reconciler.buildDeployParamsFromProfile(rctx, vmName, peUUID, nil, vmProfileNoNics, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")})
		require.Error(t, err)
		assert.Nil(t, rctx.NutanixMachine.Status.FailureReason)
		assert.Nil(t, rctx.NutanixMachine.Status.FailureMessage)
	})
}

func TestNutanixMachineReconciler_assignAddressesToMachine(t *testing.T) {
	newIPv4Address := func(ip string) *vmmCommonConfig.IPv4Address {
		addr := vmmCommonConfig.NewIPv4Address()
		addr.Value = ptr.To(ip)
		return addr
	}

	newNicWithDeprecatedNetworkInfo := func(ip string) *vmmModels.Nic {
		nic := vmmModels.NewNic()
		nic.NetworkInfo = vmmModels.NewNicNetworkInfo()
		nic.NetworkInfo.Ipv4Config = vmmModels.NewIpv4Config()
		nic.NetworkInfo.Ipv4Config.IpAddress = newIPv4Address(ip)
		return nic
	}

	newNicWithVirtualEthernetInfo := func(ip string) *vmmModels.Nic {
		nic := vmmModels.NewNic()
		info := vmmModels.NewVirtualEthernetNicNetworkInfo()
		info.Ipv4Config = vmmModels.NewIpv4Config()
		info.Ipv4Config.IpAddress = newIPv4Address(ip)
		require.NoError(t, nic.SetNicNetworkInfo(*info))
		return nic
	}

	newNicWithDpOffloadInfo := func(ip string) *vmmModels.Nic {
		nic := vmmModels.NewNic()
		info := vmmModels.NewDpOffloadNicNetworkInfo()
		info.Ipv4Config = vmmModels.NewIpv4Config()
		info.Ipv4Config.IpAddress = newIPv4Address(ip)
		require.NoError(t, nic.SetNicNetworkInfo(*info))
		return nic
	}

	newNicWithSriovInfo := func() *vmmModels.Nic {
		nic := vmmModels.NewNic()
		info := vmmModels.NewSriovNicNetworkInfo()
		info.VlanId = ptr.To(100)
		require.NoError(t, nic.SetNicNetworkInfo(*info))
		return nic
	}

	newNicWithLearnedIPs := func(ips ...string) *vmmModels.Nic {
		nic := vmmModels.NewNic()
		info := vmmModels.NewVirtualEthernetNicNetworkInfo()
		info.Ipv4Info = vmmModels.NewIpv4Info()
		for _, ip := range ips {
			info.Ipv4Info.LearnedIpAddresses = append(info.Ipv4Info.LearnedIpAddresses, *newIPv4Address(ip))
		}
		require.NoError(t, nic.SetNicNetworkInfo(*info))
		return nic
	}

	buildVM := func(nics ...*vmmModels.Nic) *vmmModels.Vm {
		vm := vmmModels.NewVm()
		vm.Name = ptr.To("vm-name")
		for _, nic := range nics {
			vm.Nics = append(vm.Nics, *nic)
		}
		return vm
	}

	t.Run("deprecated NetworkInfo with static IP", func(t *testing.T) {
		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM(newNicWithDeprecatedNetworkInfo("10.10.10.10")))

		require.NoError(t, err)
		require.Len(t, rctx.NutanixMachine.Status.Addresses, 2)
		assert.Equal(t, "10.10.10.10", rctx.NutanixMachine.Status.Addresses[0].Address)
		assert.Equal(t, "vm-name", rctx.NutanixMachine.Status.Addresses[1].Address)
	})

	t.Run("new NicNetworkInfo with static IP", func(t *testing.T) {
		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM(newNicWithVirtualEthernetInfo("10.10.10.12")))

		require.NoError(t, err)
		require.Len(t, rctx.NutanixMachine.Status.Addresses, 2)
		assert.Equal(t, "10.10.10.12", rctx.NutanixMachine.Status.Addresses[0].Address)
	})

	t.Run("new NicNetworkInfo with learned IPs", func(t *testing.T) {
		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM(newNicWithLearnedIPs("10.10.10.13")))

		require.NoError(t, err)
		require.Len(t, rctx.NutanixMachine.Status.Addresses, 2)
		assert.Equal(t, "10.10.10.13", rctx.NutanixMachine.Status.Addresses[0].Address)
	})

	t.Run("DpOffloadNicNetworkInfo with static IP", func(t *testing.T) {
		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM(newNicWithDpOffloadInfo("10.10.10.20")))

		require.NoError(t, err)
		require.Len(t, rctx.NutanixMachine.Status.Addresses, 2)
		assert.Equal(t, "10.10.10.20", rctx.NutanixMachine.Status.Addresses[0].Address)
	})

	t.Run("SriovNicNetworkInfo falls back to deprecated NetworkInfo", func(t *testing.T) {
		nic := newNicWithSriovInfo()
		nic.NetworkInfo = vmmModels.NewNicNetworkInfo()
		nic.NetworkInfo.Ipv4Config = vmmModels.NewIpv4Config()
		nic.NetworkInfo.Ipv4Config.IpAddress = newIPv4Address("10.10.10.30")

		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM(nic))

		require.NoError(t, err)
		require.Len(t, rctx.NutanixMachine.Status.Addresses, 2)
		assert.Equal(t, "10.10.10.30", rctx.NutanixMachine.Status.Addresses[0].Address)
	})

	t.Run("SriovNicNetworkInfo without deprecated NetworkInfo yields no addresses", func(t *testing.T) {
		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM(
			newNicWithVirtualEthernetInfo("10.10.10.10"),
			newNicWithSriovInfo(),
		))

		require.NoError(t, err)
		require.Len(t, rctx.NutanixMachine.Status.Addresses, 2)
		assert.Equal(t, "10.10.10.10", rctx.NutanixMachine.Status.Addresses[0].Address)
		assert.Equal(t, "vm-name", rctx.NutanixMachine.Status.Addresses[1].Address)
	})

	t.Run("prefers new NicNetworkInfo over deprecated NetworkInfo", func(t *testing.T) {
		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}

		nic := newNicWithVirtualEthernetInfo("10.10.10.50")
		nic.NetworkInfo = vmmModels.NewNicNetworkInfo()
		nic.NetworkInfo.Ipv4Config = vmmModels.NewIpv4Config()
		nic.NetworkInfo.Ipv4Config.IpAddress = newIPv4Address("10.10.10.99")

		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM(nic))

		require.NoError(t, err)
		require.Len(t, rctx.NutanixMachine.Status.Addresses, 2)
		assert.Equal(t, "10.10.10.50", rctx.NutanixMachine.Status.Addresses[0].Address)
	})

	t.Run("multiple NICs with mixed sources", func(t *testing.T) {
		rctx := &nctx.MachineContext{NutanixMachine: &infrav1.NutanixMachine{}}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM(
			newNicWithDeprecatedNetworkInfo("10.10.10.10"),
			newNicWithVirtualEthernetInfo("10.10.10.11"),
		))

		require.NoError(t, err)
		require.Len(t, rctx.NutanixMachine.Status.Addresses, 3)
	})

	t.Run("fails if no IP addresses found", func(t *testing.T) {
		rctx := &nctx.MachineContext{}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.assignAddressesToMachine(rctx, buildVM())

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unable to determine network interfaces")
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
		projectUUID := "test-project-uuid"
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Spec: infrav1.NutanixMachineSpec{
				Project: &infrav1.NutanixResourceIdentifier{
					Type: infrav1.NutanixIdentifierUUID,
					UUID: &projectUUID,
				},
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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Status: capiv1beta2.MachineStatus{
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

		mockConvergedClient := NewMockConvergedClient(ctrl)

		// Create mock VM matching the systemUUID (not VmUUID)
		vm := vmmModels.NewVm()
		vm.Name = ptr.To(vmName)
		vm.ExtId = ptr.To(systemUUID)
		vm.Project = vmmModels.NewProjectReference()
		vm.Project.ExtId = &projectUUID

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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Status: capiv1beta2.MachineStatus{
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
		testProjectExtID := "test-project-ext-id"

		// Mock FindVM to return VM matching systemUUID (already powered on)
		expectedVm := vmmModels.NewVm()
		expectedVm.Name = ptr.To(vmName)
		expectedVm.ExtId = ptr.To(systemUUID)
		expectedVm.PowerState = vmmModels.POWERSTATE_ON.Ref()
		expectedVm.Project = vmmModels.NewProjectReference()
		expectedVm.Project.ExtId = &testProjectExtID
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
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Status: capiv1beta2.MachineStatus{
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
		testProjectExtID := "test-project-ext-id"

		// Mock FindVM to return VM matching VmUUID (already powered on)
		expectedVm := vmmModels.NewVm()
		expectedVm.Name = ptr.To(vmName)
		expectedVm.ExtId = ptr.To(vmUUID)
		expectedVm.PowerState = vmmModels.POWERSTATE_ON.Ref()
		expectedVm.Project = vmmModels.NewProjectReference()
		expectedVm.Project.ExtId = &testProjectExtID
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
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name: vmName,
			},
			Status: capiv1beta2.MachineStatus{
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
		testProjectExtID := "test-project-ext-id"

		// Mock FindVM to return VM matching VmUUID (already powered on)
		expectedVm := vmmModels.NewVm()
		expectedVm.Name = ptr.To(vmName)
		expectedVm.ExtId = ptr.To(vmUUID)
		expectedVm.PowerState = vmmModels.POWERSTATE_ON.Ref()
		expectedVm.Project = vmmModels.NewProjectReference()
		expectedVm.Project.ExtId = &testProjectExtID
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
		vm, err := reconciler.getOrCreateVM(rctx, &nctx.ProjectInfo{ExtID: &testProjectExtID, Name: ptr.To("test-project")}, nil)

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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1beta2.MachineStatus{
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
		_ = capiv1beta2.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).WithStatusSubresource(nutanixMachine).Build()

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

		// Mutating the in-memory object proves nothing about durability - patchMachine could
		// compute an empty diff and skip the API call entirely while the local pointer still
		// shows the new value. Re-fetch independently from the fake client's store to confirm
		// the patch actually reached the server.
		persisted := &infrav1.NutanixMachine{}
		g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(nutanixMachine), persisted)).To(Succeed())
		g.Expect(persisted.Status.VmUUID).To(Equal(validUUID1), "VmUUID update must be durably persisted, not just mutated in memory")
	})

	t.Run("should not update VmUUID when SystemUUID matches", func(t *testing.T) {
		g := NewWithT(t)
		ctx := context.Background()

		// Machine with SystemUUID
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1beta2.MachineStatus{
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
		_ = capiv1beta2.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).WithStatusSubresource(nutanixMachine).Build()

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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1beta2.MachineStatus{
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
		_ = capiv1beta2.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).WithStatusSubresource(nutanixMachine).Build()

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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1beta2.MachineStatus{
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
		_ = capiv1beta2.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).WithStatusSubresource(nutanixMachine).Build()

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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1beta2.MachineStatus{
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
		_ = capiv1beta2.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).WithStatusSubresource(nutanixMachine).Build()

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
		machine := &capiv1beta2.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-machine",
				Namespace: "default",
			},
			Status: capiv1beta2.MachineStatus{
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
		_ = capiv1beta2.AddToScheme(scheme)
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nutanixMachine).WithStatusSubresource(nutanixMachine).Build()

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

func TestProjectPolicyConstants(t *testing.T) {
	assert.Equal(t, "capx.nutanix.com/project-policy", CAPXProjectPolicyAnnotation)
	assert.Equal(t, "default-only", CAPXProjectPolicyDefaultOnly)
	assert.Equal(t, "unrestricted", CAPXProjectPolicyUnrestricted)
	assert.Equal(t, "single-project", CAPXProjectPolicySingleProject)
	assert.Equal(t, "capx.nutanix.com/project-uuid", CAPXProjectUUIDAnnotation)
}

func TestNutanixMachineReconciler_resolveEffectiveProject(t *testing.T) {
	ctx := context.Background()

	t.Run("returns specified project UUID when ProjectRef is set", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		projectExtID := "specified-project-uuid"
		projectName := "test-project"
		mockConvergedClient.MockProjects.EXPECT().Get(ctx, projectExtID).Return(&projectModels.Project{
			ExtId: ptr.To(projectExtID),
			Name:  ptr.To(projectName),
		}, nil)

		reconciler := &NutanixMachineReconciler{}
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			Machine: &capiv1beta2.Machine{ObjectMeta: metav1.ObjectMeta{
				Name: "test-vm",
			}},
			NutanixMachine: &infrav1.NutanixMachine{
				Spec: infrav1.NutanixMachineSpec{
					Project: &infrav1.NutanixResourceIdentifier{
						UUID: &projectExtID,
					},
				},
			},
			ConvergedClient: mockConvergedClient.Client,
		}

		got, err := reconciler.resolveEffectiveProject(rctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, projectExtID, *got.ExtID)
		assert.Equal(t, projectName, *got.Name)
	})

	t.Run("returns default project UUID when ProjectRef is nil", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		defaultProjectExtID := "default-project-uuid"
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil)

		reconciler := &NutanixMachineReconciler{}
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			Machine: &capiv1beta2.Machine{ObjectMeta: metav1.ObjectMeta{
				Name: "test-vm",
			}},
			NutanixMachine: &infrav1.NutanixMachine{
				Spec: infrav1.NutanixMachineSpec{
					Project: nil,
				},
			},
			ConvergedClient: mockConvergedClient.Client,
		}

		got, err := reconciler.resolveEffectiveProject(rctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, defaultProjectExtID, *got.ExtID)
		assert.Equal(t, nctx.InternalProjectName, *got.Name)
	})

	t.Run("returns error when specified project not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		projectExtID := "non-existent-project"
		mockConvergedClient.MockProjects.EXPECT().Get(ctx, projectExtID).Return(nil, errors.New("project not found"))

		reconciler := &NutanixMachineReconciler{}
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			Machine: &capiv1beta2.Machine{ObjectMeta: metav1.ObjectMeta{
				Name: "test-vm",
			}},
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
				Spec: infrav1.NutanixMachineSpec{
					Project: &infrav1.NutanixResourceIdentifier{
						UUID: &projectExtID,
					},
				},
			},
			NutanixCluster:  &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
			ConvergedClient: mockConvergedClient.Client,
		}

		got, err := reconciler.resolveEffectiveProject(rctx)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "error occurred while searching for project")
	})

	t.Run("returns error when default project fetch fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(nil, errors.New("failed to get default project"))

		reconciler := &NutanixMachineReconciler{}
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			Machine: &capiv1beta2.Machine{ObjectMeta: metav1.ObjectMeta{
				Name: "test-vm",
			}},
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
				Spec: infrav1.NutanixMachineSpec{
					Project: nil,
				},
			},
			NutanixCluster:  &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
			ConvergedClient: mockConvergedClient.Client,
		}

		got, err := reconciler.resolveEffectiveProject(rctx)
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "error occurred while getting default project")
	})
}

func TestNutanixMachineReconciler_validateProjectPolicy(t *testing.T) {
	ctx := context.Background()

	t.Run("unrestricted policy allows any project", func(t *testing.T) {
		reconciler := &NutanixMachineReconciler{}
		projectExtID := "any-project-uuid"
		rctx := &nctx.MachineContext{
			Context: ctx,
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
			},
			ProjectPolicy: CAPXProjectPolicyUnrestricted,
		}

		err := reconciler.validateProjectPolicy(rctx, &nctx.ProjectInfo{ExtID: &projectExtID, Name: ptr.To("any-project")})
		require.NoError(t, err)
	})

	t.Run("default-only policy allows default project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		defaultProjectExtID := "default-project-uuid"
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil)

		reconciler := &NutanixMachineReconciler{}
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
			},
			NutanixCluster:  &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
			ConvergedClient: mockConvergedClient.Client,
			ProjectPolicy:   CAPXProjectPolicyDefaultOnly,
		}

		err := reconciler.validateProjectPolicy(rctx, &nctx.ProjectInfo{ExtID: &defaultProjectExtID, Name: ptr.To("default-project")})
		require.NoError(t, err)
	})

	t.Run("default-only policy rejects non-default project", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockConvergedClient := NewMockConvergedClient(ctrl)
		defaultProjectExtID := "default-project-uuid"
		nonDefaultProjectExtID := "non-default-project-uuid"
		mockConvergedClient.MockProjects.EXPECT().GetDefaultProject(ctx).Return(&projectModels.Project{
			ExtId: ptr.To(defaultProjectExtID),
		}, nil)

		reconciler := &NutanixMachineReconciler{}
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
			},
			NutanixCluster:  &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
			ConvergedClient: mockConvergedClient.Client,
			ProjectPolicy:   CAPXProjectPolicyDefaultOnly,
		}

		err := reconciler.validateProjectPolicy(rctx, &nctx.ProjectInfo{ExtID: &nonDefaultProjectExtID, Name: ptr.To("non-default-project")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "project policy violation")
	})

	t.Run("single-project policy with project-uuid allows matching project", func(t *testing.T) {
		reconciler := &NutanixMachineReconciler{}
		projectExtID := "my-project-uuid"
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
			},
			Cluster: &capiv1beta2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
					Annotations: map[string]string{
						CAPXProjectPolicyAnnotation: CAPXProjectPolicySingleProject,
						CAPXProjectUUIDAnnotation:   projectExtID,
					},
				},
			},
			NutanixCluster: &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
			ProjectPolicy:  CAPXProjectPolicySingleProject,
		}

		err := reconciler.validateProjectPolicy(rctx, &nctx.ProjectInfo{ExtID: &projectExtID, Name: ptr.To("my-project")})
		require.NoError(t, err)
	})

	t.Run("single-project policy with project-uuid rejects non-matching project", func(t *testing.T) {
		reconciler := &NutanixMachineReconciler{}
		expectedProjectExtID := "my-project-uuid"
		actualProjectExtID := "different-project-uuid"
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
			},
			Cluster: &capiv1beta2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
					Annotations: map[string]string{
						CAPXProjectPolicyAnnotation: CAPXProjectPolicySingleProject,
						CAPXProjectUUIDAnnotation:   expectedProjectExtID,
					},
				},
			},
			NutanixCluster: &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
			ProjectPolicy:  CAPXProjectPolicySingleProject,
		}

		err := reconciler.validateProjectPolicy(rctx, &nctx.ProjectInfo{ExtID: &actualProjectExtID, Name: ptr.To("different-project")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "project policy violation")
		assert.Contains(t, err.Error(), `uses project "different-project"`)
	})

	t.Run("single-project policy returns error when project-uuid annotation is missing", func(t *testing.T) {
		reconciler := &NutanixMachineReconciler{}
		projectExtID := "some-project-uuid"
		rctx := &nctx.MachineContext{
			Context:   ctx,
			PCVersion: "7.6",
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
			},
			Cluster: &capiv1beta2.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
					Annotations: map[string]string{
						CAPXProjectPolicyAnnotation: CAPXProjectPolicySingleProject,
					},
				},
			},
			NutanixCluster: &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
			ProjectPolicy:  CAPXProjectPolicySingleProject,
		}

		err := reconciler.validateProjectPolicy(rctx, &nctx.ProjectInfo{ExtID: &projectExtID, Name: ptr.To("some-project")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "single-project policy requires")
		assert.Contains(t, err.Error(), CAPXProjectUUIDAnnotation)
	})

	t.Run("invalid policy returns error", func(t *testing.T) {
		reconciler := &NutanixMachineReconciler{}
		projectExtID := "some-project-uuid"
		rctx := &nctx.MachineContext{
			Context: ctx,
			NutanixMachine: &infrav1.NutanixMachine{
				ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
			},
			NutanixCluster: &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
			ProjectPolicy:  "invalid-policy",
		}

		err := reconciler.validateProjectPolicy(rctx, &nctx.ProjectInfo{ExtID: &projectExtID, Name: ptr.To("some-project")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid project policy")
	})
}

func TestNutanixMachineReconciler_addVMToProject(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error and sets condition when vm is nil", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		projectName := "proj"
		ntnxMachine := &infrav1.NutanixMachine{
			ObjectMeta: metav1.ObjectMeta{Name: "test-machine", Namespace: "default"},
			Spec: infrav1.NutanixMachineSpec{
				Project: &infrav1.NutanixResourceIdentifier{Name: &projectName},
			},
		}
		machine := &capiv1beta2.Machine{ObjectMeta: metav1.ObjectMeta{Name: "test-vm"}}
		rctx := &nctx.MachineContext{
			Context:        ctx,
			Machine:        machine,
			NutanixMachine: ntnxMachine,
			NutanixCluster: &infrav1.NutanixCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "default"}},
		}
		reconciler := &NutanixMachineReconciler{}
		err := reconciler.addVMToProject(rctx, nil, ptr.To("project-ext-id"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "VM cannot be nil")
	})
}

// vhaVM builds a VM whose Categories reference the given category extIds.
func vhaVM(name string, categoryExtIds ...string) *vmmModels.Vm {
	vm := vmmModels.NewVm()
	vm.Name = ptr.To(name)
	vm.ExtId = ptr.To("vm-" + name)
	refs := make([]vmmModels.CategoryReference, 0, len(categoryExtIds))
	for _, extId := range categoryExtIds {
		refs = append(refs, vmmModels.CategoryReference{ExtId: ptr.To(extId)})
	}
	vm.Categories = refs
	return vm
}

const (
	vhaTestNamespace   = "default"
	vhaTestNtnxCluster = "test-ntnxcluster"
	vhaCatValue0       = "k8s-vha-capx-d1-default-0"
	vhaCatValue1       = "k8s-vha-capx-d1-default-1"
	vhaCatExt0         = "vha-ext-a"
	vhaCatExt1         = "vha-ext-b"
)

// vhaTestNutanixCluster returns a NutanixCluster used as the owner of the vHADomain CRs.
func vhaTestNutanixCluster() *infrav1.NutanixCluster {
	return &infrav1.NutanixCluster{
		TypeMeta:   metav1.TypeMeta{Kind: infrav1.NutanixClusterKind, APIVersion: infrav1.GroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{Name: vhaTestNtnxCluster, Namespace: vhaTestNamespace, UID: "ntnx-cluster-uid"},
	}
}

// vhaOwnedDomain returns a NutanixVirtualHADomain owned by the given NutanixCluster whose
// cluster-scope movement group maps the two vHADomain categories (key k8s-vha-native-site).
func vhaOwnedDomain(ncl *infrav1.NutanixCluster) *infrav1.NutanixVirtualHADomain {
	return &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "d1",
			Namespace: vhaTestNamespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: infrav1.GroupVersion.String(),
				Kind:       infrav1.NutanixClusterKind,
				Name:       ncl.Name,
				UID:        ncl.UID,
			}},
		},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MovementGroups: []infrav1.NutanixMovementGroup{{
				Name: clusterScopeMovementGroupName,
				CategoryRecoveryPlans: []infrav1.NutanixCategoryRecoveryPlan{
					{
						Category:         infrav1.NutanixCategoryIdentifier{Key: VHADomainDefaultCategoryKey, Value: vhaCatValue0},
						FailureDomainRef: corev1.LocalObjectReference{Name: "fd-0"},
					},
					{
						Category:         infrav1.NutanixCategoryIdentifier{Key: VHADomainDefaultCategoryKey, Value: vhaCatValue1},
						FailureDomainRef: corev1.LocalObjectReference{Name: "fd-1"},
					},
				},
			}},
		},
	}
}

// expectVHACategoryLookups wires the per-category getCategory (Categories.List by key+value) lookups
// for the cluster's vHADomain CR, resolving each value to its extId. This mirrors the targeted
// lookups the check performs instead of listing every "k8s-vha-native-site" category in PC.
func expectVHACategoryLookups(mockClient *MockConvergedClientWrapper) {
	byValue := map[string]string{
		vhaCatValue0: vhaCatExt0,
		vhaCatValue1: vhaCatExt1,
	}
	mockClient.MockCategories.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, opts ...converged.ODataOption) ([]prismModels.Category, error) {
			for value, extId := range byValue {
				if filterContains(opts, "value eq '"+value+"'") {
					return []prismModels.Category{{
						ExtId: ptr.To(extId),
						Key:   ptr.To(VHADomainDefaultCategoryKey),
						Value: ptr.To(value),
					}}, nil
				}
			}
			return []prismModels.Category{}, nil
		},
	).AnyTimes()
}

// filterContains reports whether the OData filter built from opts contains sub.
func filterContains(opts []converged.ODataOption, sub string) bool {
	params, err := v4Converged.OptsToV4ODataParams(opts...)
	if err != nil || params.Filter == nil {
		return false
	}
	return strings.Contains(*params.Filter, sub)
}

// TestCountVMVHADomainCategories asserts that only the categories belonging to the cluster's vHADomain
// CR (key "k8s-vha-native-site") are counted, regardless of any other categories assigned to the VM.
// This is the core of the implicit CSI/NKP k8s-HA contract: the count must be exactly 1 for a Metro VM.
func TestCountVMVHADomainCategories(t *testing.T) {
	const otherExt = "other-ext-1"

	tests := []struct {
		name string
		vm   *vmmModels.Vm
		want int
	}{
		{
			name: "no categories",
			vm:   vhaVM("n0"),
			want: 0,
		},
		{
			name: "only non-vha categories",
			vm:   vhaVM("n1", otherExt, "other-ext-2"),
			want: 0,
		},
		{
			name: "exactly one vha category (contract satisfied)",
			vm:   vhaVM("n2", otherExt, vhaCatExt0),
			want: 1,
		},
		{
			name: "two vha categories (contract violated)",
			vm:   vhaVM("n3", vhaCatExt0, vhaCatExt1, otherExt),
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			ctx := context.Background()
			mockClient := NewMockConvergedClient(ctrl)
			expectVHACategoryLookups(mockClient)

			ncl := vhaTestNutanixCluster()
			ctlClient := newVHACtlClient(t, ncl, vhaOwnedDomain(ncl))

			rctx := &nctx.MachineContext{
				Context:         ctx,
				ConvergedClient: mockClient.Client,
				NutanixCluster:  ncl,
			}

			got, err := countVMVHADomainCategories(rctx, ctlClient, tt.vm)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCountVMVHADomainCategories_CategoryLookupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	mockClient := NewMockConvergedClient(ctrl)
	mockClient.MockCategories.EXPECT().List(ctx, gomock.Any()).Return(nil, errors.New("boom")).AnyTimes()

	ncl := vhaTestNutanixCluster()
	ctlClient := newVHACtlClient(t, ncl, vhaOwnedDomain(ncl))

	rctx := &nctx.MachineContext{
		Context:         ctx,
		ConvergedClient: mockClient.Client,
		NutanixCluster:  ncl,
	}

	_, err := countVMVHADomainCategories(rctx, ctlClient, vhaVM("n", vhaCatExt0))
	require.Error(t, err)
}

// newVHACtlClient builds a fake controller-runtime client seeded with the given objects.
func newVHACtlClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, infrav1.AddToScheme(scheme))
	require.NoError(t, capiv1beta2.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// TestCheckVHADomainCategory enforces the implicit CSI/NKP k8s-HA contract at reconcile time: a Metro
// VM must carry one and only one category with the vHADomain key "k8s-vha-native-site" belonging to
// the cluster's vHADomain CR. The check is a no-op for non-Metro machines.
func TestCheckVHADomainCategory(t *testing.T) {
	const otherExt = "other-ext-1"

	tests := []struct {
		name          string
		failureDomain string
		vm            *vmmModels.Vm
		expectLookups bool
		wantErr       bool
	}{
		{
			name:          "non-metro machine is a no-op",
			failureDomain: "some-zone",
			vm:            vhaVM("n", vhaCatExt0, vhaCatExt1),
			expectLookups: false,
			wantErr:       false,
		},
		{
			name:          "metro VM with exactly one vha category passes",
			failureDomain: metroFailureDomainPrefix + "metro-1",
			vm:            vhaVM("n", otherExt, vhaCatExt0),
			expectLookups: true,
			wantErr:       false,
		},
		{
			name:          "metro VM with no vha category fails",
			failureDomain: metroFailureDomainPrefix + "metro-1",
			vm:            vhaVM("n", otherExt),
			expectLookups: true,
			wantErr:       true,
		},
		{
			name:          "metro VM with two vha categories fails",
			failureDomain: metroFailureDomainPrefix + "metro-1",
			vm:            vhaVM("n", vhaCatExt0, vhaCatExt1),
			expectLookups: true,
			wantErr:       true,
		},
		{
			name:          "metroSite VM with exactly one vha category passes",
			failureDomain: metroSiteFailureDomainPrefix + "site-1",
			vm:            vhaVM("n", vhaCatExt0),
			expectLookups: true,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			ctx := context.Background()
			mockClient := NewMockConvergedClient(ctrl)
			if tt.expectLookups {
				expectVHACategoryLookups(mockClient)
			}

			ncl := vhaTestNutanixCluster()
			ctlClient := newVHACtlClient(t, ncl, vhaOwnedDomain(ncl))

			rctx := &nctx.MachineContext{
				Context:         ctx,
				ConvergedClient: mockClient.Client,
				NutanixCluster:  ncl,
				Machine: &capiv1beta2.Machine{
					ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: vhaTestNamespace},
					Spec:       capiv1beta2.MachineSpec{FailureDomain: tt.failureDomain},
				},
			}

			r := &NutanixMachineReconciler{Client: ctlClient}
			err := r.checkVHADomainCategory(rctx, tt.vm)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGetMetroSiteFailureDomainSpec_UsesActiveSiteButPreservesNativeLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	g := NewWithT(t)

	const (
		ns          = "default"
		metroName   = "metro-a"
		metroSite   = "metrosite-a"
		fd0Name     = "fd-0"
		fd1Name     = "fd-1"
		fd0PEUUID   = "00000000-0000-0000-0000-000000000010"
		fd1PEUUID   = "00000000-0000-0000-0000-000000000011"
		recoveryPID = "rp-native-fd0"
	)

	scheme := runtime.NewScheme()
	g.Expect(infrav1.AddToScheme(scheme)).To(Succeed())
	g.Expect(capiv1beta2.AddToScheme(scheme)).To(Succeed())

	fd0 := &infrav1.NutanixFailureDomain{
		ObjectMeta: metav1.ObjectMeta{Name: fd0Name, Namespace: ns},
		Spec: infrav1.NutanixFailureDomainSpec{
			PrismElementCluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(fd0PEUUID)},
		},
	}
	fd1 := &infrav1.NutanixFailureDomain{
		ObjectMeta: metav1.ObjectMeta{Name: fd1Name, Namespace: ns},
		Spec: infrav1.NutanixFailureDomainSpec{
			PrismElementCluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(fd1PEUUID)},
		},
	}
	metro := &infrav1.NutanixMetro{
		ObjectMeta: metav1.ObjectMeta{Name: metroName, Namespace: ns},
		Spec: infrav1.NutanixMetroSpec{
			FailureDomains: []corev1.LocalObjectReference{{Name: fd0Name}, {Name: fd1Name}},
		},
	}
	metrositeObj := &infrav1.NutanixMetroSite{
		ObjectMeta: metav1.ObjectMeta{Name: metroSite, Namespace: ns},
		Spec: infrav1.NutanixMetroSiteSpec{
			MetroRef:               corev1.LocalObjectReference{Name: metroName},
			PreferredFailureDomain: corev1.LocalObjectReference{Name: fd0Name},
		},
	}
	nutanixCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: ns, UID: "cluster-uid"},
	}
	vha := &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vha-a",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: infrav1.GroupVersion.String(),
				Kind:       infrav1.NutanixClusterKind,
				Name:       nutanixCluster.Name,
				UID:        nutanixCluster.UID,
			}},
		},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{Name: metroName},
			MovementGroups: []infrav1.NutanixMovementGroup{{
				Name: clusterScopeMovementGroupName,
				CategoryRecoveryPlans: []infrav1.NutanixCategoryRecoveryPlan{
					{
						FailureDomainRef: corev1.LocalObjectReference{Name: fd0Name},
						RecoveryPlan:     infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(recoveryPID)},
					},
				},
			}},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(fd0, fd1, metro, metrositeObj, nutanixCluster, vha).Build()

	mockConvergedClient := NewMockConvergedClient(ctrl)
	mockConvergedClient.MockClusters.EXPECT().Get(gomock.Any(), fd0PEUUID).Return(
		&clustermgmtconfig.Cluster{ExtId: ptr.To(fd0PEUUID), Config: &clustermgmtconfig.ClusterConfigReference{IsAvailable: ptr.To(true)}}, nil,
	).AnyTimes()
	mockConvergedClient.MockClusters.EXPECT().Get(gomock.Any(), fd1PEUUID).Return(
		&clustermgmtconfig.Cluster{ExtId: ptr.To(fd1PEUUID), Config: &clustermgmtconfig.ClusterConfigReference{IsAvailable: ptr.To(true)}}, nil,
	).AnyTimes()

	mockV3Client := mocknutanixv3.NewMockService(ctrl)
	mockV3Client.EXPECT().ListRecoveryPlanJobs(gomock.Any(), gomock.Any()).Return(
		&prismclientv3.RecoveryPlanJobListResponse{
			Entities: []*prismclientv3.RecoveryPlanJobIntentResponse{newRecoveryPlanJobIntentResponse(recoveryPID, fd1PEUUID)},
		}, nil,
	)

	rctx := &nctx.MachineContext{
		Context:         context.Background(),
		NutanixClient:   &prismclientv3.Client{V3: mockV3Client},
		ConvergedClient: mockConvergedClient.Client,
		Cluster:         &capiv1beta2.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: ns}},
		Machine:         &capiv1beta2.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine-a", Namespace: ns}},
		NutanixCluster:  nutanixCluster,
		NutanixMachine:  &infrav1.NutanixMachine{ObjectMeta: metav1.ObjectMeta{Name: "nm-a", Namespace: ns}},
	}

	reconciler := &NutanixMachineReconciler{Client: fakeClient}
	fdSpec, err := reconciler.getMetroSiteFailureDomainSpec(rctx, metroSite)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(ptr.Deref(fdSpec.PrismElementCluster.UUID, "")).To(Equal(fd1PEUUID))
	g.Expect(rctx.NutanixMachine.Labels[metroNativeFailureDomainLabelKey]).To(Equal(fd0Name))
	g.Expect(ptr.Deref(rctx.Datastore[nctx.MetroPreferredFailureDomainName], "")).To(Equal(fd0Name))
}

func TestGetMetroFailureDomainSpec_UsesActiveSiteButPreservesNativeLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	g := NewWithT(t)

	const (
		ns          = "default"
		metroName   = "metro-b"
		fd0Name     = "fd-0"
		fd1Name     = "fd-1"
		fd0PEUUID   = "00000000-0000-0000-0000-000000000020"
		fd1PEUUID   = "00000000-0000-0000-0000-000000000021"
		recoveryPID = "rp-native-fd0-b"
	)

	scheme := runtime.NewScheme()
	g.Expect(infrav1.AddToScheme(scheme)).To(Succeed())
	g.Expect(capiv1beta2.AddToScheme(scheme)).To(Succeed())

	fd0 := &infrav1.NutanixFailureDomain{
		ObjectMeta: metav1.ObjectMeta{Name: fd0Name, Namespace: ns},
		Spec: infrav1.NutanixFailureDomainSpec{
			PrismElementCluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(fd0PEUUID)},
		},
	}
	fd1 := &infrav1.NutanixFailureDomain{
		ObjectMeta: metav1.ObjectMeta{Name: fd1Name, Namespace: ns},
		Spec: infrav1.NutanixFailureDomainSpec{
			PrismElementCluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(fd1PEUUID)},
		},
	}
	metro := &infrav1.NutanixMetro{
		ObjectMeta: metav1.ObjectMeta{Name: metroName, Namespace: ns},
		Spec: infrav1.NutanixMetroSpec{
			FailureDomains: []corev1.LocalObjectReference{{Name: fd0Name}, {Name: fd1Name}},
		},
	}
	nutanixCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-b", Namespace: ns, UID: "cluster-uid-b"},
	}
	vha := &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vha-b",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: infrav1.GroupVersion.String(),
				Kind:       infrav1.NutanixClusterKind,
				Name:       nutanixCluster.Name,
				UID:        nutanixCluster.UID,
			}},
		},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{Name: metroName},
			MovementGroups: []infrav1.NutanixMovementGroup{{
				Name: clusterScopeMovementGroupName,
				CategoryRecoveryPlans: []infrav1.NutanixCategoryRecoveryPlan{
					{
						FailureDomainRef: corev1.LocalObjectReference{Name: fd0Name},
						RecoveryPlan:     infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(recoveryPID)},
					},
				},
			}},
		},
	}
	currentNM := &infrav1.NutanixMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nm-b",
			Namespace: ns,
			Labels: map[string]string{
				capiv1beta2.ClusterNameLabel: "cluster-b",
			},
		},
	}
	currentMachine := &capiv1beta2.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine-b",
			Namespace: ns,
			Labels: map[string]string{
				capiv1beta2.ClusterNameLabel: "cluster-b",
			},
		},
		Spec: capiv1beta2.MachineSpec{
			InfrastructureRef: capiv1beta2.ContractVersionedObjectReference{Name: "nm-b"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(fd0, fd1, metro, nutanixCluster, vha, currentNM, currentMachine).Build()

	mockConvergedClient := NewMockConvergedClient(ctrl)
	mockConvergedClient.MockClusters.EXPECT().Get(gomock.Any(), fd0PEUUID).Return(
		&clustermgmtconfig.Cluster{ExtId: ptr.To(fd0PEUUID), Config: &clustermgmtconfig.ClusterConfigReference{IsAvailable: ptr.To(true)}}, nil,
	).AnyTimes()
	mockConvergedClient.MockClusters.EXPECT().Get(gomock.Any(), fd1PEUUID).Return(
		&clustermgmtconfig.Cluster{ExtId: ptr.To(fd1PEUUID), Config: &clustermgmtconfig.ClusterConfigReference{IsAvailable: ptr.To(true)}}, nil,
	).AnyTimes()

	mockV3Client := mocknutanixv3.NewMockService(ctrl)
	mockV3Client.EXPECT().ListRecoveryPlanJobs(gomock.Any(), gomock.Any()).Return(
		&prismclientv3.RecoveryPlanJobListResponse{
			Entities: []*prismclientv3.RecoveryPlanJobIntentResponse{newRecoveryPlanJobIntentResponse(recoveryPID, fd1PEUUID)},
		}, nil,
	)

	rctx := &nctx.MachineContext{
		Context:         context.Background(),
		NutanixClient:   &prismclientv3.Client{V3: mockV3Client},
		ConvergedClient: mockConvergedClient.Client,
		Cluster:         &capiv1beta2.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster-b", Namespace: ns}},
		Machine:         currentMachine,
		NutanixCluster:  nutanixCluster,
		NutanixMachine:  currentNM,
	}

	reconciler := &NutanixMachineReconciler{Client: fakeClient, APIReader: fakeClient}
	fdSpec, err := reconciler.getMetroFailureDomainSpec(rctx, metroName)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(ptr.Deref(fdSpec.PrismElementCluster.UUID, "")).To(Equal(fd1PEUUID))
	g.Expect(rctx.NutanixMachine.Labels[metroNativeFailureDomainLabelKey]).To(Equal(fd0Name))
	g.Expect(ptr.Deref(rctx.Datastore[nctx.MetroPreferredFailureDomainName], "")).To(Equal(fd0Name))
}

func TestGetMetroSiteFailureDomainSpec_FallbacksToNativeWhenRecoveryPlanJobLookupFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	g := NewWithT(t)

	const (
		ns          = "default"
		metroName   = "metro-c"
		metroSite   = "metrosite-c"
		fd0Name     = "fd-0"
		fd1Name     = "fd-1"
		fd0PEUUID   = "00000000-0000-0000-0000-000000000030"
		fd1PEUUID   = "00000000-0000-0000-0000-000000000031"
		recoveryPID = "rp-native-fd0-c"
	)

	scheme := runtime.NewScheme()
	g.Expect(infrav1.AddToScheme(scheme)).To(Succeed())
	g.Expect(capiv1beta2.AddToScheme(scheme)).To(Succeed())

	fd0 := &infrav1.NutanixFailureDomain{
		ObjectMeta: metav1.ObjectMeta{Name: fd0Name, Namespace: ns},
		Spec: infrav1.NutanixFailureDomainSpec{
			PrismElementCluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(fd0PEUUID)},
		},
	}
	fd1 := &infrav1.NutanixFailureDomain{
		ObjectMeta: metav1.ObjectMeta{Name: fd1Name, Namespace: ns},
		Spec: infrav1.NutanixFailureDomainSpec{
			PrismElementCluster: infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(fd1PEUUID)},
		},
	}
	metro := &infrav1.NutanixMetro{
		ObjectMeta: metav1.ObjectMeta{Name: metroName, Namespace: ns},
		Spec: infrav1.NutanixMetroSpec{
			FailureDomains: []corev1.LocalObjectReference{{Name: fd0Name}, {Name: fd1Name}},
		},
	}
	metrositeObj := &infrav1.NutanixMetroSite{
		ObjectMeta: metav1.ObjectMeta{Name: metroSite, Namespace: ns},
		Spec: infrav1.NutanixMetroSiteSpec{
			MetroRef:               corev1.LocalObjectReference{Name: metroName},
			PreferredFailureDomain: corev1.LocalObjectReference{Name: fd0Name},
		},
	}
	nutanixCluster := &infrav1.NutanixCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-c", Namespace: ns, UID: "cluster-uid-c"},
	}
	vha := &infrav1.NutanixVirtualHADomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vha-c",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: infrav1.GroupVersion.String(),
				Kind:       infrav1.NutanixClusterKind,
				Name:       nutanixCluster.Name,
				UID:        nutanixCluster.UID,
			}},
		},
		Spec: infrav1.NutanixVirtualHADomainSpec{
			MetroRef: corev1.LocalObjectReference{Name: metroName},
			MovementGroups: []infrav1.NutanixMovementGroup{{
				Name: clusterScopeMovementGroupName,
				CategoryRecoveryPlans: []infrav1.NutanixCategoryRecoveryPlan{
					{
						FailureDomainRef: corev1.LocalObjectReference{Name: fd0Name},
						RecoveryPlan:     infrav1.NutanixResourceIdentifier{Type: infrav1.NutanixIdentifierUUID, UUID: ptr.To(recoveryPID)},
					},
				},
			}},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(fd0, fd1, metro, metrositeObj, nutanixCluster, vha).Build()

	mockConvergedClient := NewMockConvergedClient(ctrl)
	mockConvergedClient.MockClusters.EXPECT().Get(gomock.Any(), fd0PEUUID).Return(
		&clustermgmtconfig.Cluster{ExtId: ptr.To(fd0PEUUID), Config: &clustermgmtconfig.ClusterConfigReference{IsAvailable: ptr.To(true)}}, nil,
	).AnyTimes()

	mockV3Client := mocknutanixv3.NewMockService(ctrl)
	mockV3Client.EXPECT().ListRecoveryPlanJobs(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("temporary prism error"))

	rctx := &nctx.MachineContext{
		Context:         context.Background(),
		NutanixClient:   &prismclientv3.Client{V3: mockV3Client},
		ConvergedClient: mockConvergedClient.Client,
		Cluster:         &capiv1beta2.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster-c", Namespace: ns}},
		Machine:         &capiv1beta2.Machine{ObjectMeta: metav1.ObjectMeta{Name: "machine-c", Namespace: ns}},
		NutanixCluster:  nutanixCluster,
		NutanixMachine:  &infrav1.NutanixMachine{ObjectMeta: metav1.ObjectMeta{Name: "nm-c", Namespace: ns}},
	}

	reconciler := &NutanixMachineReconciler{Client: fakeClient}
	fdSpec, err := reconciler.getMetroSiteFailureDomainSpec(rctx, metroSite)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(ptr.Deref(fdSpec.PrismElementCluster.UUID, "")).To(Equal(fd0PEUUID))
	g.Expect(rctx.NutanixMachine.Labels[metroNativeFailureDomainLabelKey]).To(Equal(fd0Name))
	g.Expect(ptr.Deref(rctx.Datastore[nctx.MetroPreferredFailureDomainName], "")).To(Equal(fd0Name))
}

func newRecoveryPlanJobIntentResponse(recoveryPlanUUID, activePEUUID string) *prismclientv3.RecoveryPlanJobIntentResponse {
	return &prismclientv3.RecoveryPlanJobIntentResponse{
		Status: &prismclientv3.RecoveryPlanJobDefStatus{
			Resources: &prismclientv3.RecoveryPlanJobResources{
				RecoveryPlanReference: &prismclientv3.Reference{UUID: ptr.To(recoveryPlanUUID)},
				ExecutionParameters: &prismclientv3.RecoveryPlanJobResourcesExecutionParameters{
					RecoveryAvailabilityZoneList: []*prismclientv3.AvailabilityZoneList{{
						ClusterReferenceList: []*prismclientv3.Reference{{UUID: ptr.To(activePEUUID)}},
					}},
				},
			},
		},
	}
}
