package templates

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2/textlogger"
	"k8s.io/utils/ptr"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	clusterctllog "sigs.k8s.io/cluster-api/cmd/clusterctl/log"
	controlplanev1 "sigs.k8s.io/cluster-api/controlplane/kubeadm/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/kind/pkg/cluster"

	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
)

const (
	defaultNamespace           = "default"
	kindClusterName            = "capi-test"
	localImageRegistryEnv      = "LOCAL_IMAGE_REGISTRY"
	defaultLocalImageRegistry  = "ko.local"
	defaultLocalImageTagFormat = "e2e-%s"
	defaultImageRepo           = "cluster-api-provider-nutanix"
)

var clnt client.Client

func init() {
	format.MaxLength = 100000
	// Add NutanixCluster and NutanixMachine to the scheme
	_ = v1beta1.AddToScheme(scheme.Scheme)
	_ = capiv1.AddToScheme(scheme.Scheme)
	_ = apiextensionsv1.AddToScheme(scheme.Scheme)
	_ = controlplanev1.AddToScheme(scheme.Scheme)
}

func teardownTestEnvironment() error {
	provider := cluster.NewProvider(cluster.ProviderWithDocker())
	return provider.Delete(kindClusterName, "")
}

// getGitCommitHash retrieves the current git commit hash.
func getGitCommitHash() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get git commit hash: %v", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// getEnv retrieves the value of the environment variable or returns a default value if not set.
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}

	return defaultValue
}

// buildImageName constructs the image name
func buildImageName() (string, error) {
	gitCommitHash, err := getGitCommitHash()
	if err != nil {
		return "", err
	}

	localImageRegistry := getEnv(localImageRegistryEnv, defaultLocalImageRegistry)
	imgRepo := fmt.Sprintf("%s/%s", localImageRegistry, defaultImageRepo)
	imgTag := fmt.Sprintf(defaultLocalImageTagFormat, gitCommitHash)
	managerImage := fmt.Sprintf("%s:%s", imgRepo, imgTag)

	return managerImage, nil
}

// loadImageIntoKindCluster loads a Docker image tarball into a kind cluster.
func loadImageIntoKindCluster(imageName string) error {
	cmd := exec.Command("kind", "load", "docker-image", "--name", kindClusterName, imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to load image into kind cluster: %s, %v", string(output), err)
	}

	fmt.Println("Image loaded successfully into kind cluster")
	return nil
}

func setupTestEnvironment() (client.Client, error) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))
	_ = teardownTestEnvironment()

	provider := cluster.NewProvider(cluster.ProviderWithDocker())
	err := provider.Create(kindClusterName, cluster.CreateWithNodeImage("kindest/node:v1.29.2"))
	if err != nil {
		return nil, fmt.Errorf("failed to create Kind cluster: %w", err)
	}

	kubeconfig, err := provider.KubeConfig(kindClusterName, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	tmpKubeconfig, err := os.CreateTemp("", "kubeconfig")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp kubeconfig: %w", err)
	}

	_, err = tmpKubeconfig.Write([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to write to temp kubeconfig: %w", err)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to get restconfig: %w", err)
	}

	// Load the Nutanix image into the Kind cluster
	imageTarPath, err := buildImageName()
	if err != nil {
		return nil, fmt.Errorf("failed to build image tar path: %w", err)
	}

	if err := loadImageIntoKindCluster(imageTarPath); err != nil {
		return nil, fmt.Errorf("failed to load image into Kind cluster: %w", err)
	}

	clusterctllog.SetLogger(textlogger.NewLogger(textlogger.NewConfig()))
	clusterctl.Init(context.Background(), clusterctl.InitInput{
		KubeconfigPath:          tmpKubeconfig.Name(),
		InfrastructureProviders: []string{"nutanix"},
		ClusterctlConfigPath:    "testdata/clusterctl-init.yaml",
	})

	clnt, err := client.New(restConfig, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	objects := getClusterClassObjects()
	for _, obj := range objects {
		if err := clnt.Create(context.Background(), obj); err != nil {
			fmt.Printf("failed to create object %s: %s\n\n", obj, err)
			continue
		}
	}

	return clnt, nil
}

func getObjectsFromYAML(filename string) ([]client.Object, error) {
	// read the file
	f, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Create a new YAML decoder
	decoder := yaml.NewYAMLToJSONDecoder(bytes.NewReader(f))

	// Decode the YAML manifests into client.Object instances
	var objects []client.Object
	for {
		var obj unstructured.Unstructured
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to decode YAML: %w", err)
		}
		if obj.GetNamespace() == "" {
			obj.SetNamespace(defaultNamespace)
		}
		objects = append(objects, &obj)
	}

	return objects, nil
}

func getClusterClassObjects() []client.Object {
	const template = "cluster-template-clusterclass.yaml"
	objects, err := getObjectsFromYAML(template)
	if err != nil {
		return nil
	}

	return objects
}

func getClusterManifest(filePath string) (client.Object, error) {
	f, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var obj unstructured.Unstructured
	if err := yaml.NewYAMLToJSONDecoder(bytes.NewReader(f)).Decode(&obj); err != nil {
		return nil, fmt.Errorf("failed to decode YAML: %w", err)
	}

	obj.SetNamespace(defaultNamespace) // Set the namespace to default
	return &obj, nil
}

func fetchNutanixCluster(clnt client.Client, clusterName string) (*v1beta1.NutanixCluster, error) {
	nutanixClusterList := &v1beta1.NutanixClusterList{}
	if err := clnt.List(context.Background(), nutanixClusterList, &client.ListOptions{Namespace: defaultNamespace}); err != nil {
		return nil, fmt.Errorf("failed to list NutanixCluster: %w", err)
	}

	if len(nutanixClusterList.Items) == 0 {
		return nil, fmt.Errorf("no NutanixCluster found")
	}

	for _, nutanixCluster := range nutanixClusterList.Items {
		if strings.Contains(nutanixCluster.Name, clusterName) {
			return &nutanixCluster, nil
		}
	}

	return nil, fmt.Errorf("matching NutanixCluster not found")
}

func fetchMachineTemplates(clnt client.Client, clusterName string) ([]*v1beta1.NutanixMachineTemplate, error) {
	nutanixMachineTemplateList := &v1beta1.NutanixMachineTemplateList{}
	if err := clnt.List(context.Background(), nutanixMachineTemplateList, &client.ListOptions{Namespace: defaultNamespace}); err != nil {
		return nil, fmt.Errorf("failed to list NutanixMachine: %w", err)
	}

	if len(nutanixMachineTemplateList.Items) == 0 {
		return nil, fmt.Errorf("no NutanixMachine found")
	}

	nmts := make([]*v1beta1.NutanixMachineTemplate, 0)
	for _, nmt := range nutanixMachineTemplateList.Items {
		if nmt.Labels[capiv1.ClusterNameLabel] == clusterName {
			nmts = append(nmts, &nmt)
		}
	}

	if len(nmts) == 0 {
		return nil, fmt.Errorf("no NutanixMachineTemplate found for cluster %s", clusterName)
	}

	return nmts, nil
}

func fetchKubeadmControlPlane(clnt client.Client, clusterName string) (*controlplanev1.KubeadmControlPlane, error) {
	kubeadmControlPlaneList := &controlplanev1.KubeadmControlPlaneList{}
	if err := clnt.List(context.Background(), kubeadmControlPlaneList, &client.ListOptions{Namespace: defaultNamespace}); err != nil {
		return nil, fmt.Errorf("failed to list KubeadmControlPlane: %w", err)
	}

	for _, kcp := range kubeadmControlPlaneList.Items {
		if kcp.Labels[capiv1.ClusterNameLabel] == clusterName {
			return &kcp, nil
		}
	}

	return nil, fmt.Errorf("no KubeadmControlPlane found for cluster %s", clusterName)
}

func fetchControlPlaneMachineTemplate(clnt client.Client, clusterName string) (*v1beta1.NutanixMachineTemplate, error) {
	nmts, err := fetchMachineTemplates(clnt, clusterName)
	if err != nil {
		return nil, err
	}

	kcp, err := fetchKubeadmControlPlane(clnt, clusterName)
	if err != nil {
		return nil, err
	}

	for _, nmt := range nmts {
		if nmt.Name == kcp.Spec.MachineTemplate.InfrastructureRef.Name {
			return nmt, nil
		}
	}

	return nil, fmt.Errorf("no control plane NutanixMachineTemplate found for cluster %s", clusterName)
}

func fetchWorkerMachineTemplates(clnt client.Client, clusterName string) ([]*v1beta1.NutanixMachineTemplate, error) {
	nmts, err := fetchMachineTemplates(clnt, clusterName)
	if err != nil {
		return nil, err
	}

	kcp, err := fetchKubeadmControlPlane(clnt, clusterName)
	if err != nil {
		return nil, err
	}

	workerNmts := make([]*v1beta1.NutanixMachineTemplate, 0)
	for _, nmt := range nmts {
		if nmt.Name == kcp.Spec.MachineTemplate.InfrastructureRef.Name {
			continue
		}

		workerNmts = append(workerNmts, nmt)
	}

	return workerNmts, nil
}

func TestClusterClassTemplateSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		c, err := setupTestEnvironment()
		Expect(err).NotTo(HaveOccurred())
		clnt = c
	})
	AfterSuite(func() {
		Expect(teardownTestEnvironment()).NotTo(HaveOccurred())
	})

	RunSpecs(t, "Template Tests Suite")
}

var _ = Describe("Cluster Class Template Patches Test Suite", Ordered, func() {
	Describe("patches for failure domains", Ordered, func() {
		var obj client.Object
		It("should create the cluster with failure domains", func() {
			clusterManifest := "testdata/cluster-with-failure-domain.yaml"
			o, err := getClusterManifest(clusterManifest)
			Expect(err).NotTo(HaveOccurred())
			obj = o

			Expect(clnt.Create(context.Background(), obj)).NotTo(HaveOccurred())
		})

		It("NutanixCluster should have correct failure domains", func() {
			Eventually(func() (*v1beta1.NutanixCluster, error) {
				return fetchNutanixCluster(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(
				HaveField("Spec.FailureDomains", HaveLen(3)),
				HaveField("Spec.FailureDomains", HaveEach(HaveField("Name", BeAssignableToTypeOf("string")))),
				HaveField("Spec.FailureDomains", HaveEach(HaveField("ControlPlane", HaveValue(Equal(true))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Cluster.Type", HaveValue(Equal(v1beta1.NutanixIdentifierUUID))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Cluster.UUID", HaveValue(Equal("00000000-0000-0000-0000-000000000001"))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Cluster.Type", HaveValue(Equal(v1beta1.NutanixIdentifierName))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Cluster.Name", HaveValue(Equal("cluster1"))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Cluster.Name", HaveValue(Equal("cluster2"))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Subnets", ContainElement(HaveField("Type", HaveValue(Equal(v1beta1.NutanixIdentifierUUID))))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Subnets", ContainElement(HaveField("Name", HaveValue(Equal("subnet1"))))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Subnets", ContainElement(HaveField("Name", HaveValue(Equal("subnet2"))))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Subnets", ContainElement(HaveField("Type", HaveValue(Equal(v1beta1.NutanixIdentifierName))))))),
				HaveField("Spec.FailureDomains", ContainElement(HaveField("Subnets", ContainElement(HaveField("UUID", HaveValue(Equal("00000000-0000-0000-0000-000000000001"))))))),
			))
		})

		It("Control Plane NutanixMachineTemplate should not have the cluster and subnets set", func() {
			Eventually(func() (*v1beta1.NutanixMachineTemplate, error) {
				return fetchControlPlaneMachineTemplate(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(
				HaveField("Spec.Template.Spec.Cluster", BeZero()),
				HaveField("Spec.Template.Spec.Subnets", HaveLen(0))))
		})

		It("should delete the cluster", func() {
			Expect(clnt.Delete(context.Background(), obj)).NotTo(HaveOccurred())
		})
	})

	Describe("patches for control plane endpoint kubevip", func() {
		It("kubevip should have correct control plane endpoint", func() {
			clusterManifest := "testdata/cluster-with-control-plane-endpoint.yaml"
			obj, err := getClusterManifest(clusterManifest)
			Expect(err).NotTo(HaveOccurred())

			err = clnt.Create(context.Background(), obj) // Create the cluster
			Expect(err).NotTo(HaveOccurred())

			var nutanixCluster *v1beta1.NutanixCluster
			Eventually(func() error {
				nutanixCluster, err = fetchNutanixCluster(clnt, obj.GetName())
				return err
			}).Within(time.Minute).Should(Succeed())

			Expect(nutanixCluster.Spec.ControlPlaneEndpoint).NotTo(BeNil())
			Expect(nutanixCluster.Spec.ControlPlaneEndpoint.Host).To(Equal("1.2.3.4"))
			Expect(nutanixCluster.Spec.ControlPlaneEndpoint.Port).To(Equal(int32(6443)))

			var kubeadmcontrolplane *controlplanev1.KubeadmControlPlane
			Eventually(func() error {
				kcp, err := fetchKubeadmControlPlane(clnt, obj.GetName())
				kubeadmcontrolplane = kcp
				return err
			}).Within(time.Minute).Should(Succeed())
			Expect(kubeadmcontrolplane.Spec.KubeadmConfigSpec.PreKubeadmCommands).To(ContainElements(
				"sed -i 's/control_plane_endpoint_ip/1.2.3.4/g' /etc/kubernetes/manifests/kube-vip.yaml",
				"sed -i 's/control_plane_endpoint_port/6443/g' /etc/kubernetes/manifests/kube-vip.yaml",
			))
		})
	})

	Describe("patches for project (with name)", func() {
		It("should have correct project", func() {
			clusterManifest := "testdata/cluster-with-project-name.yaml"
			obj, err := getClusterManifest(clusterManifest)
			Expect(err).NotTo(HaveOccurred())

			err = clnt.Create(context.Background(), obj) // Create the cluster
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() ([]*v1beta1.NutanixMachineTemplate, error) {
				return fetchMachineTemplates(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(HaveLen(2),
				HaveEach(HaveExistingField("Spec.Template.Spec.Project")),
				HaveEach(HaveField("Spec.Template.Spec.Project.Type", Equal(v1beta1.NutanixIdentifierName))),
				HaveEach(HaveField("Spec.Template.Spec.Project.Name", HaveValue(Equal("fake-project"))))))
		})
	})

	Describe("patches for project (with uuid)", func() {
		It("should have correct project", func() {
			clusterManifest := "testdata/cluster-with-project-uuid.yaml"
			obj, err := getClusterManifest(clusterManifest)
			Expect(err).NotTo(HaveOccurred())

			err = clnt.Create(context.Background(), obj) // Create the cluster
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() ([]*v1beta1.NutanixMachineTemplate, error) {
				return fetchMachineTemplates(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(HaveLen(2),
				HaveEach(HaveExistingField("Spec.Template.Spec.Project")),
				HaveEach(HaveField("Spec.Template.Spec.Project.Type", Equal(v1beta1.NutanixIdentifierUUID))),
				HaveEach(HaveField("Spec.Template.Spec.Project.UUID", HaveValue(Equal("00000000-0000-0000-0000-000000000001"))))))
		})
	})

	Describe("patches for additional categories", func() {
		It("should have correct categories", func() {
			clusterManifest := "testdata/cluster-with-additional-categories.yaml"
			obj, err := getClusterManifest(clusterManifest)
			Expect(err).NotTo(HaveOccurred())

			err = clnt.Create(context.Background(), obj) // Create the cluster
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() ([]*v1beta1.NutanixMachineTemplate, error) {
				return fetchMachineTemplates(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(HaveLen(2),
				HaveEach(HaveExistingField("Spec.Template.Spec.AdditionalCategories")),
				HaveEach(HaveField("Spec.Template.Spec.AdditionalCategories", HaveLen(1))),
				HaveEach(HaveField("Spec.Template.Spec.AdditionalCategories", ContainElement(v1beta1.NutanixCategoryIdentifier{
					Key:   "fake-category-key",
					Value: "fake-category-value",
				})))))
		})
	})

	Describe("patches for GPUs", func() {
		It("should have correct GPUs", func() {
			clusterManifest := "testdata/cluster-with-gpu.yaml"
			obj, err := getClusterManifest(clusterManifest)
			Expect(err).NotTo(HaveOccurred())

			err = clnt.Create(context.Background(), obj) // Create the cluster
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() (*v1beta1.NutanixMachineTemplate, error) {
				return fetchControlPlaneMachineTemplate(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(HaveExistingField("Spec.Template.Spec.GPUs"),
				HaveField("Spec.Template.Spec.GPUs", HaveLen(1)),
				HaveField("Spec.Template.Spec.GPUs", ContainElement(v1beta1.NutanixGPU{
					Type:     v1beta1.NutanixGPUIdentifierDeviceID,
					DeviceID: ptr.To(int64(42)),
				}))))

			Eventually(func() ([]*v1beta1.NutanixMachineTemplate, error) {
				return fetchWorkerMachineTemplates(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(HaveLen(1),
				HaveEach(HaveExistingField("Spec.Template.Spec.GPUs")),
				HaveEach(HaveField("Spec.Template.Spec.GPUs", HaveLen(1))),
				HaveEach(HaveField("Spec.Template.Spec.GPUs", ContainElement(v1beta1.NutanixGPU{
					Type: v1beta1.NutanixGPUIdentifierName,
					Name: ptr.To("fake-gpu"),
				}))),
			))
		})
	})

	Describe("patches for subnets", func() {
		It("should have correct subnets", func() {
			clusterManifest := "testdata/cluster-with-subnets.yaml"
			obj, err := getClusterManifest(clusterManifest)
			Expect(err).NotTo(HaveOccurred())

			err = clnt.Create(context.Background(), obj) // Create the cluster
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() (*v1beta1.NutanixMachineTemplate, error) {
				return fetchControlPlaneMachineTemplate(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(HaveExistingField("Spec.Template.Spec.Subnets"),
				HaveField("Spec.Template.Spec.Subnets", HaveLen(2)),
				HaveField("Spec.Template.Spec.Subnets", ContainElement(v1beta1.NutanixResourceIdentifier{
					Type: v1beta1.NutanixIdentifierName,
					Name: ptr.To("shared-subnet"),
				})),
				HaveField("Spec.Template.Spec.Subnets", ContainElement(v1beta1.NutanixResourceIdentifier{
					Type: v1beta1.NutanixIdentifierName,
					Name: ptr.To("controlplane-subnet"),
				}))))

			Eventually(func() ([]*v1beta1.NutanixMachineTemplate, error) {
				return fetchWorkerMachineTemplates(clnt, obj.GetName())
			}).Within(time.Minute).Should(And(HaveLen(1),
				HaveEach(HaveExistingField("Spec.Template.Spec.Subnets")),
				HaveEach(HaveField("Spec.Template.Spec.Subnets", HaveLen(2))),
				HaveEach(HaveField("Spec.Template.Spec.Subnets", ContainElement(v1beta1.NutanixResourceIdentifier{
					Type: v1beta1.NutanixIdentifierName,
					Name: ptr.To("shared-subnet"),
				}))),
				HaveEach(HaveField("Spec.Template.Spec.Subnets", ContainElement(v1beta1.NutanixResourceIdentifier{
					Type: v1beta1.NutanixIdentifierName,
					Name: ptr.To("worker-subnet"),
				}))),
			))
		})
	})
})
