package client

import (
	"fmt"
	"os"
	"strings"

	"k8s.io/klog/v2"

	nutanixClient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/nutanix"
	nutanixClientV3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/nutanix/v3"
)

const (
	ProviderName  = "nutanix"
	debugModeName = "DEBUG_MODE"
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

	cli, err := nutanixClientV3.NewV3Client(cred, debugMode(options))
	if err != nil {
		klog.Errorf("Failed to create the nutanix client. error: %v", err)
		return nil, err
	}

	return cli, nil
}

func debugMode(options ClientOptions) bool {
	//Read environment variable to enable debug mode
	debugModeEnv := getEnvVar(debugModeName)
	if debugModeEnv != "" {
		//See if env var is set to 'true', otherwise default to false
		if strings.ToLower(debugModeEnv) == "true" {
			return true
		} else {
			return false
		}
	}
	// If env var not set -> use options
	return options.Debug
}

func getEnvVar(key string) (val string) {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return
}
