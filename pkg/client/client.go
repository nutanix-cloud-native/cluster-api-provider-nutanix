package client

import (
	"context"
	"fmt"
	"os"
	"strings"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	nutanixClient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/nutanix"
	nutanixClientV3 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/nutanix/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	ctrlClient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultEndpointPort = "9440"
	ProviderName        = "nutanix"
	debugModeName       = "DEBUG_MODE"
	nutanixUsernameKey  = "NUTANIX_USER"
	nutanixPasswordKey  = "NUTANIX_PASSWORD"
)

type ClientOptions struct {
	Debug bool
}

func Client(cred nutanixClient.Credentials, options ClientOptions) (*nutanixClientV3.Client, error) {
	if cred.Username == "" {
		errorMsg := fmt.Errorf("could not create client because username was not set")
		klog.Error(errorMsg)
		return nil, errorMsg
	}
	if cred.Password == "" {
		errorMsg := fmt.Errorf("could not create client because password was not set")
		klog.Error(errorMsg)
		return nil, errorMsg
	}
	if cred.Port == "" {
		cred.Port = defaultEndpointPort
	}
	if cred.URL == "" {
		cred.URL = fmt.Sprintf("%s:%s", cred.Endpoint, cred.Port)
	}
	cli, err := nutanixClientV3.NewV3Client(cred, debugMode(options))
	if err != nil {
		klog.Errorf("Failed to create the nutanix client. error: %v", err)
		return nil, err
	}

	return cli, nil
}

func GetConnectionInfo(client ctrlClient.Client, ctx context.Context, nutanixCluster *infrav1.NutanixCluster) (*nutanixClient.Credentials, error) {
	prismCentralInfo := nutanixCluster.Spec.PrismCentral
	if prismCentralInfo.Address == "" {
		return nil, fmt.Errorf("cannot get credentials if Prism Address is not set")
	}
	if prismCentralInfo.Port == 0 {
		return nil, fmt.Errorf("cannot get credentials if Prism Port is not set")
	}
	var credentials *nutanixClient.Credentials
	var err error
	credentialRef := prismCentralInfo.CredentialRef
	if credentialRef == nil {
		klog.Infof("Using credential information from manager environment for cluster %s", nutanixCluster.ClusterName)
		credentials, err = getCredentialsFromEnv()
	} else {
		credentials, err = getCredentialsFromCredentialRef(client, ctx, nutanixCluster)
	}
	if err != nil {
		errorMsg := fmt.Errorf("error occurred fetching credentials for cluster %s: %v", nutanixCluster.ClusterName, err)
		klog.Error(errorMsg)
		return nil, errorMsg
	}
	credentials.Insecure = prismCentralInfo.Insecure
	credentials.Port = fmt.Sprint(prismCentralInfo.Port)
	credentials.Endpoint = prismCentralInfo.Address
	return credentials, nil
}

func GetCredentialRefForCluster(nutanixCluster *infrav1.NutanixCluster) (*infrav1.NutanixCredentialReference, error) {
	if nutanixCluster == nil {
		return nil, fmt.Errorf("cannot get credential reference if nutanix cluster object is nil")
	}
	prismCentralinfo := nutanixCluster.Spec.PrismCentral
	if prismCentralinfo.CredentialRef == nil {
		return nil, nil
	}
	if prismCentralinfo.CredentialRef.Kind != infrav1.SecretKind {
		return nil, nil
	}
	return prismCentralinfo.CredentialRef, nil
}

func getCredentialsFromCredentialRef(client ctrlClient.Client, ctx context.Context, nutanixCluster *infrav1.NutanixCluster) (*nutanixClient.Credentials, error) {
	klog.Infof("using credential ref defined in cluster %s", nutanixCluster.ClusterName)
	credentialRef, err := GetCredentialRefForCluster(nutanixCluster)
	if err != nil {
		errorMsg := fmt.Errorf("error occurred fetching credential ref from cluster %s: %v", nutanixCluster.ClusterName, err)
		klog.Error(errorMsg)
		return nil, errorMsg
	}
	if credentialRef == nil {
		errorMsg := fmt.Errorf("cannot use credentialRef to fetch credentials if it is nil for cluster %s", nutanixCluster.ClusterName)
		klog.Error(errorMsg)
		return nil, errorMsg
	}
	secret := &corev1.Secret{}
	var secretKey ctrlClient.ObjectKey

	//allows for future types
	switch credentialRef.Kind {
	case infrav1.SecretKind:
		secretKey = ctrlClient.ObjectKey{
			Namespace: nutanixCluster.Namespace,
			Name:      credentialRef.Name,
		}
	default:
		return nil, fmt.Errorf("unsupported type %s used for credential ref", credentialRef.Kind)
	}

	if err := client.Get(ctx, secretKey, secret); err != nil {
		errorMsg := fmt.Errorf("error occurred getting secret %s in namespace %s: %v", credentialRef.Name, nutanixCluster.Namespace, err)
		klog.Error(errorMsg)
		return nil, errorMsg
	}
	return &nutanixClient.Credentials{
		Username: getKeyFromSecret(secret, nutanixUsernameKey),
		Password: getKeyFromSecret(secret, nutanixPasswordKey),
	}, nil
}

func getCredentialsFromEnv() (*nutanixClient.Credentials, error) {
	username := getEnvVar(nutanixUsernameKey)
	if username == "" {
		return nil, fmt.Errorf("failed to fetch NUTANIX_USER env variable")
	}
	password := getEnvVar(nutanixPasswordKey)
	if password == "" {
		return nil, fmt.Errorf("failed to fetch NUTANIX_PASSWORD env variable")
	}

	return &nutanixClient.Credentials{
		Username: username,
		Password: password,
	}, nil
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

func getKeyFromSecret(secret *corev1.Secret, key string) string {
	if secret.Data != nil {
		if val, ok := secret.Data[key]; ok {
			return string(val)
		}
	}
	return ""
}
