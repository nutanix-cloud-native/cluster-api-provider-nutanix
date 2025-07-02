//go:build e2e

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

package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Nutanix flavor CSI", Label("nutanix-storage-test", "csi"), func() {
	const (
		specName = "cluster-csi"

		nutanixWebhookCAKey        = "WEBHOOK_CA"
		nutanixWebhookCertKey      = "WEBHOOK_CERT"
		nutanixWebhookKeyKey       = "WEBHOOK_KEY"
		nutanixStorageContainerKey = "NUTANIX_STORAGE_CONTAINER"
		nutanixPEIPKey             = "NUTANIX_PRISM_ELEMENT_CLUSTER_IP"
		nutanixPEUsernameKey       = "NUTANIX_PRISM_ELEMENT_CLUSTER_USERNAME"
		nutanixPEPasswordKey       = "NUTANIX_PRISM_ELEMENT_CLUSTER_PASSWORD"
		nutanixPEPort              = "9440"

		csiNamespaceName  = "ntnx-system"
		csiDeploymentName = "nutanix-csi-controller"
		csiProvisioner    = "csi.nutanix.com"
	)

	var (
		namespace        *corev1.Namespace
		clusterName      string
		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult
		cancelWatches    context.CancelFunc
		testHelper       testHelperInterface
		workloadClient   client.Client

		csiSecretName       = util.RandomString(10)
		csiStorageClassName = util.RandomString(10)

		nutanixStorageContainer string
		nutanixPEIP             string
		nutanixPEUsername       string
		nutanixPEPassword       string
	)

	BeforeEach(func() {
		testHelper = newTestHelper(e2eConfig)
		clusterName = testHelper.generateTestClusterName(specName)
		clusterResources = &clusterctl.ApplyClusterTemplateAndWaitResult{}
		Expect(bootstrapClusterProxy).NotTo(BeNil(), "BootstrapClusterProxy can't be nil")
		namespace, cancelWatches = setupSpecNamespace(ctx, specName, bootstrapClusterProxy, artifactFolder)

		nutanixWebhookCA := testHelper.getVariableFromE2eConfig(nutanixWebhookCAKey)
		Expect(nutanixWebhookCA).ToNot(BeEmpty())
		nutanixWebhookCert := testHelper.getVariableFromE2eConfig(nutanixWebhookCertKey)
		Expect(nutanixWebhookCert).ToNot(BeEmpty())
		nutanixWebhookKey := testHelper.getVariableFromE2eConfig(nutanixWebhookKeyKey)
		Expect(nutanixWebhookKey).ToNot(BeEmpty())
		nutanixPEIP = testHelper.getVariableFromE2eConfig(nutanixPEIPKey)
		Expect(nutanixPEIP).ToNot(BeEmpty())
		nutanixPEUsername = testHelper.getVariableFromE2eConfig(nutanixPEUsernameKey)
		Expect(nutanixPEUsername).ToNot(BeEmpty())
		nutanixPEPassword = testHelper.getVariableFromE2eConfig(nutanixPEPasswordKey)
		Expect(nutanixPEPassword).ToNot(BeEmpty())
		nutanixStorageContainer = testHelper.getVariableFromE2eConfig(nutanixStorageContainerKey)
		Expect(nutanixStorageContainer).ToNot(BeEmpty())
	})

	AfterEach(func() {
		dumpSpecResourcesAndCleanup(ctx, specName, bootstrapClusterProxy, artifactFolder, namespace, cancelWatches, clusterResources.Cluster, e2eConfig.GetIntervals, skipCleanup)
	})

	It("Create a cluster with Nutanix CSI and use Nutanix Volumes to create PV", Label("csi2"), func() {
		const flavor = "csi"

		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		By("Fetching workload client")
		workloadProxy := bootstrapClusterProxy.GetWorkloadCluster(ctx, namespace.Name, clusterResources.Cluster.Name)
		workloadClient = workloadProxy.GetClient()

		By("Creating CSI secret")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      csiSecretName,
				Namespace: csiNamespaceName,
			},
			Data: map[string][]byte{
				"key": []byte(fmt.Sprintf("%s:%s:%s:%s", nutanixPEIP, nutanixPEPort, nutanixPEUsername, nutanixPEPassword)),
			},
		}
		Eventually(func() error {
			return workloadClient.Create(ctx, secret)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Checking if CSI namespace exists")
		csiNamespace := &corev1.Namespace{}
		csiNamespaceKey := client.ObjectKey{
			Name: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiNamespaceKey, csiNamespace)).To(Succeed())
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Checking if CSI deployment exists")
		csiDeployment := &appsv1.Deployment{}
		csiDeploymentKey := client.ObjectKey{
			Name:      csiDeploymentName,
			Namespace: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiDeploymentKey, csiDeployment)).To(Succeed())
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Creating CSI Storage class")
		sc := &storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "storage.k8s.io/v1",
				Kind:       "StorageClass",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: csiStorageClassName,
				Annotations: map[string]string{
					"storageclass.kubernetes.io/is-default-class": "true",
				},
			},
			Provisioner: csiProvisioner,
			Parameters: map[string]string{
				"csi.storage.k8s.io/provisioner-secret-name":            csiSecretName,
				"csi.storage.k8s.io/provisioner-secret-namespace":       csiNamespaceName,
				"csi.storage.k8s.io/node-publish-secret-name":           csiSecretName,
				"csi.storage.k8s.io/node-publish-secret-namespace":      csiNamespaceName,
				"csi.storage.k8s.io/controller-expand-secret-name":      csiSecretName,
				"csi.storage.k8s.io/controller-expand-secret-namespace": csiNamespaceName,
				"csi.storage.k8s.io/fstype":                             "ext4",
				"flashMode":                                             "DISABLED",
				"storageContainer":                                      nutanixStorageContainer,
				"chapAuth":                                              "ENABLED",
				"storageType":                                           "NutanixVolumes",
				"description":                                           "CAPX e2e-test",
			},
		}
		Eventually(func() error {
			return workloadClient.Create(ctx, sc)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Creating CSI PVC")
		pvcName := util.RandomString(10)
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: csiNamespaceName,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				StorageClassName: &csiStorageClassName,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}

		Eventually(func() error {
			return workloadClient.Create(ctx, pvc)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Checking CSI PVC status is bound")
		csiPvc := &corev1.PersistentVolumeClaim{}
		csiPvcKey := client.ObjectKey{
			Name:      pvcName,
			Namespace: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiPvcKey, csiPvc)).To(Succeed())
			g.Expect(csiPvc.Status.Phase).To(Equal(corev1.ClaimBound))
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Deleting CSI PVC")
		Expect(workloadClient.Delete(ctx, csiPvc)).To(Succeed())

		By("PASSED!")
	})

	It("Create a cluster with Nutanix CSI 3.0 and use Nutanix Volumes to create PV", Label("csi3"), func() {
		const flavor = "csi3"

		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		By("Fetching workload client")
		workloadProxy := bootstrapClusterProxy.GetWorkloadCluster(ctx, namespace.Name, clusterResources.Cluster.Name)
		workloadClient = workloadProxy.GetClient()

		By("Checking if CSI namespace exists")
		csiNamespace := &corev1.Namespace{}
		csiNamespaceKey := client.ObjectKey{
			Name: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiNamespaceKey, csiNamespace)).To(Succeed())
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Checking if CSI deployment exists")
		csiDeployment := &appsv1.Deployment{}
		csiDeploymentKey := client.ObjectKey{
			Name:      csiDeploymentName,
			Namespace: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiDeploymentKey, csiDeployment)).To(Succeed())
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Creating CSI Storage class")
		sc := &storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "storage.k8s.io/v1",
				Kind:       "StorageClass",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: csiStorageClassName,
				Annotations: map[string]string{
					"storageclass.kubernetes.io/is-default-class": "true",
				},
			},
			Provisioner:          csiProvisioner,
			ReclaimPolicy:        ptr.To(corev1.PersistentVolumeReclaimDelete),
			VolumeBindingMode:    ptr.To(storagev1.VolumeBindingWaitForFirstConsumer),
			AllowVolumeExpansion: ptr.To(false),
			Parameters: map[string]string{
				"csi.storage.k8s.io/provisioner-secret-name":            "nutanix-csi-credentials",
				"csi.storage.k8s.io/provisioner-secret-namespace":       csiNamespaceName,
				"csi.storage.k8s.io/node-publish-secret-name":           "nutanix-csi-credentials",
				"csi.storage.k8s.io/node-publish-secret-namespace":      csiNamespaceName,
				"csi.storage.k8s.io/controller-expand-secret-name":      "nutanix-csi-credentials",
				"csi.storage.k8s.io/controller-expand-secret-namespace": csiNamespaceName,
				"csi.storage.k8s.io/fstype":                             "xfs",
				"flashMode":                                             "DISABLED",
				"hypervisorAttached":                                    "ENABLED",
				"storageContainer":                                      nutanixStorageContainer,
				"storageType":                                           "NutanixVolumes",
				"description":                                           "CAPX e2e-test",
			},
		}
		Eventually(func() error {
			return workloadClient.Create(ctx, sc)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Creating CSI PVC")
		pvcName := util.RandomString(10)
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: csiNamespaceName,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				StorageClassName: ptr.To(csiStorageClassName),
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}

		Eventually(func() error {
			return workloadClient.Create(ctx, pvc)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Creating a pod using the PVC to ensure VG is attached to the VM")
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "busybox-pod",
				Namespace: csiNamespaceName,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:            "busybox",
						Image:           "cgr.dev/chainguard/busybox:latest",
						Command:         []string{"sleep", "3600"},
						ImagePullPolicy: "IfNotPresent",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "csi-volume",
								MountPath: "/data",
							},
						},
					},
				},
				RestartPolicy: "Always",
				Volumes: []corev1.Volume{
					{
						Name: "csi-volume",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvcName,
							},
						},
					},
				},
			},
		}

		Eventually(func() error {
			return workloadClient.Create(ctx, pod)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Checking if the pod is running")
		podKey := client.ObjectKeyFromObject(pod)
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, podKey, pod)).To(Succeed())
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
		})

		By("Checking CSI PVC status is bound")
		csiPvc := &corev1.PersistentVolumeClaim{}
		csiPvcKey := client.ObjectKey{
			Name:      pvcName,
			Namespace: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiPvcKey, csiPvc)).To(Succeed())
			g.Expect(csiPvc.Status.Phase).To(Equal(corev1.ClaimBound))
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Deleting the pod")
		Expect(workloadClient.Delete(ctx, pod)).To(Succeed())

		By("Deleting CSI PVC")
		Expect(workloadClient.Delete(ctx, csiPvc)).To(Succeed())
	})

	It("Create a cluster with Nutanix CSI 3.0 and use Nutanix Volumes to create PV and delete cluster without deleting PVC", Label("csi3"), func() {
		const flavor = "csi3"

		Expect(namespace).NotTo(BeNil())

		By("Creating a workload cluster")
		testHelper.deployClusterAndWait(
			deployClusterParams{
				clusterName:           clusterName,
				namespace:             namespace,
				flavor:                flavor,
				clusterctlConfigPath:  clusterctlConfigPath,
				artifactFolder:        artifactFolder,
				bootstrapClusterProxy: bootstrapClusterProxy,
			}, clusterResources)

		By("Fetching workload client")
		workloadProxy := bootstrapClusterProxy.GetWorkloadCluster(ctx, namespace.Name, clusterResources.Cluster.Name)
		workloadClient = workloadProxy.GetClient()

		By("Checking if CSI namespace exists")
		csiNamespace := &corev1.Namespace{}
		csiNamespaceKey := client.ObjectKey{
			Name: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiNamespaceKey, csiNamespace)).To(Succeed())
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Checking if CSI deployment exists")
		csiDeployment := &appsv1.Deployment{}
		csiDeploymentKey := client.ObjectKey{
			Name:      csiDeploymentName,
			Namespace: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiDeploymentKey, csiDeployment)).To(Succeed())
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Creating CSI Storage class")
		sc := &storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "storage.k8s.io/v1",
				Kind:       "StorageClass",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: csiStorageClassName,
				Annotations: map[string]string{
					"storageclass.kubernetes.io/is-default-class": "true",
				},
			},
			Provisioner:          csiProvisioner,
			ReclaimPolicy:        ptr.To(corev1.PersistentVolumeReclaimDelete),
			VolumeBindingMode:    ptr.To(storagev1.VolumeBindingWaitForFirstConsumer),
			AllowVolumeExpansion: ptr.To(false),
			Parameters: map[string]string{
				"csi.storage.k8s.io/provisioner-secret-name":            "nutanix-csi-credentials",
				"csi.storage.k8s.io/provisioner-secret-namespace":       csiNamespaceName,
				"csi.storage.k8s.io/node-publish-secret-name":           "nutanix-csi-credentials",
				"csi.storage.k8s.io/node-publish-secret-namespace":      csiNamespaceName,
				"csi.storage.k8s.io/controller-expand-secret-name":      "nutanix-csi-credentials",
				"csi.storage.k8s.io/controller-expand-secret-namespace": csiNamespaceName,
				"csi.storage.k8s.io/fstype":                             "xfs",
				"flashMode":                                             "DISABLED",
				"hypervisorAttached":                                    "ENABLED",
				"storageContainer":                                      nutanixStorageContainer,
				"storageType":                                           "NutanixVolumes",
				"description":                                           "CAPX e2e-test",
			},
		}
		Eventually(func() error {
			return workloadClient.Create(ctx, sc)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Creating CSI PVC")
		pvcName := util.RandomString(10)
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: csiNamespaceName,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				StorageClassName: ptr.To(csiStorageClassName),
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}

		Eventually(func() error {
			return workloadClient.Create(ctx, pvc)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Creating a pod using the PVC to ensure VG is attached to the VM")
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "busybox-pod",
				Namespace: csiNamespaceName,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:            "busybox",
						Image:           "cgr.dev/chainguard/busybox:latest",
						Command:         []string{"sleep", "3600"},
						ImagePullPolicy: "IfNotPresent",
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "csi-volume",
								MountPath: "/data",
							},
						},
					},
				},
				RestartPolicy: "Always",
				Volumes: []corev1.Volume{
					{
						Name: "csi-volume",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvcName,
							},
						},
					},
				},
			},
		}

		Eventually(func() error {
			return workloadClient.Create(ctx, pod)
		}, defaultTimeout, defaultInterval).Should(Succeed())

		By("Checking if the pod is running")
		podKey := client.ObjectKeyFromObject(pod)
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, podKey, pod)).To(Succeed())
			g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
		})

		By("Checking CSI PVC status is bound")
		csiPvc := &corev1.PersistentVolumeClaim{}
		csiPvcKey := client.ObjectKey{
			Name:      pvcName,
			Namespace: csiNamespaceName,
		}
		Eventually(func(g Gomega) {
			g.Expect(workloadClient.Get(ctx, csiPvcKey, csiPvc)).To(Succeed())
			g.Expect(csiPvc.Status.Phase).To(Equal(corev1.ClaimBound))
		}, defaultTimeout, defaultInterval).Should(Succeed())
	})
})
