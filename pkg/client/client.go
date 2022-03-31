package client

import (
	"fmt"
	"os"

	"k8s.io/klog/v2"

	nutanixClient "github.com/nutanix-core/cluster-api-provider-nutanix/pkg/nutanix"
	nutanixClientV3 "github.com/nutanix-core/cluster-api-provider-nutanix/pkg/nutanix/v3"
)

const (
	ProviderName = "nutanix"
)

type ClientOptions struct {
	Debug bool
}

func Client(options ClientOptions) (*nutanixClientV3.Client, error) {
	username := getEnvVar("NUTANIX_USER")
	password := getEnvVar("NUTANIX_PASSWORD")
	port := getEnvVar("NUTANIX_PORT")
	endpoint := getEnvVar("NUTANIX_ENDPOINT")
	cred := nutanixClient.Credentials{
		URL:      fmt.Sprintf("%s:%s", endpoint, port),
		Username: username,
		Password: password,
		Port:     port,
		Endpoint: endpoint,
		Insecure: true,
	}

	cli, err := nutanixClientV3.NewV3Client(cred, options.Debug)
	if err != nil {
		klog.Errorf("Failed to create the nutanix client. error: %v", err)
		return nil, err
	}

	return cli, nil
}

func getEnvVar(key string) (val string) {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return
}
