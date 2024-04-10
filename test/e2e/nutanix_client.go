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
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strconv"

	prismGoClient "github.com/nutanix-cloud-native/prism-go-client"
	prismGoClientV3 "github.com/nutanix-cloud-native/prism-go-client/v3"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	nutanixClient "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
)

const (
	nutanixEndpointVarKey              = "NUTANIX_ENDPOINT"
	nutanixPortVarKey                  = "NUTANIX_PORT"
	nutanixInsecureVarKey              = "NUTANIX_INSECURE"
	nutanixUsernameVarKey              = "NUTANIX_USER"
	nutanixPasswordVarKey              = "NUTANIX_PASSWORD"
	nutanixAdditionalTrustBundleVarKey = "NUTANIX_ADDITIONAL_TRUST_BUNDLE"
)

var (
	nutanixEndpoint              string
	nutanixPort                  string
	nutanixInsecure              string
	nutanixAdditionalTrustBundle string
)

func init() {
	flag.StringVar(&nutanixEndpoint, "e2e.nutanixEndpoint", os.Getenv(nutanixEndpointVarKey), "the Nutanix Prism Central used for e2e tests")
	flag.StringVar(&nutanixPort, "e2e.nutanixPort", os.Getenv(nutanixPortVarKey), "the Nutanix Prism Central port used for e2e tests")
	flag.StringVar(&nutanixInsecure, "e2e.nutanixInsecure", os.Getenv(nutanixInsecureVarKey), "Ignore certificate checks for e2e tests")
	flag.StringVar(&nutanixAdditionalTrustBundle, "e2e.nutanixAdditionalTrustBundle", os.Getenv(nutanixAdditionalTrustBundleVarKey), "Additional trust bundle for e2e tests")
}

func fetchCredentialParameter(key string, config clusterctl.E2EConfig, allowEmpty bool) string {
	value := os.Getenv(key)

	if value == "" && config.HasVariable(key) {
		value = config.GetVariable(key)
	}

	if allowEmpty && value == "" {
		return value
	}
	Expect(value).ToNot(BeEmpty(), "expected parameter %s to be set", key)
	return value
}

type baseAuthCredentials struct {
	username string
	password string
}

func getBaseAuthCredentials(e2eConfig clusterctl.E2EConfig) baseAuthCredentials {
	return baseAuthCredentials{
		username: fetchCredentialParameter(nutanixUsernameVarKey, e2eConfig, false),
		password: fetchCredentialParameter(nutanixPasswordVarKey, e2eConfig, false),
	}
}

func getNutanixCredentials(e2eConfig clusterctl.E2EConfig) (*prismGoClient.Credentials, error) {
	up := getBaseAuthCredentials(e2eConfig)
	if nutanixEndpoint == "" {
		nutanixEndpoint = fetchCredentialParameter(nutanixEndpointVarKey, e2eConfig, false)
	}
	if nutanixPort == "" {
		nutanixPort = fetchCredentialParameter(nutanixPortVarKey, e2eConfig, true)
	}
	if nutanixInsecure == "" {
		nutanixInsecure = fetchCredentialParameter(nutanixInsecureVarKey, e2eConfig, true)
	}
	if nutanixAdditionalTrustBundle == "" {
		nutanixAdditionalTrustBundle = fetchCredentialParameter(nutanixAdditionalTrustBundleVarKey, e2eConfig, true)
	}

	creds := &prismGoClient.Credentials{
		Port:     nutanixPort,
		Endpoint: nutanixEndpoint,
		Username: up.username,
		Password: up.password,
	}
	if nutanixInsecure != "" {
		insecureBool, err := strconv.ParseBool(nutanixInsecure)
		if err != nil {
			return nil, fmt.Errorf("unable to convert value for environment variable %s to bool: %v", nutanixInsecureVarKey, err)
		}
		creds.Insecure = insecureBool
	}
	return creds, nil
}

func initNutanixClient(e2eConfig clusterctl.E2EConfig) (*prismGoClientV3.Client, error) {
	creds, err := getNutanixCredentials(e2eConfig)
	if err != nil {
		return nil, err
	}

	var trustBundle string
	if nutanixAdditionalTrustBundle != "" {
		decodedCert, err := base64.StdEncoding.DecodeString(nutanixAdditionalTrustBundle)
		if err != nil {
			return nil, err
		}
		trustBundle = string(decodedCert)
	}
	client, err := nutanixClient.Build(*creds, trustBundle)
	if err != nil {
		return nil, err
	}

	return client, nil
}
