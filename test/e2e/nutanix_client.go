//go:build e2e
// +build e2e

/*
Copyright 2021.

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
	"flag"
	"os"
	"strconv"

	prismGoClient "github.com/nutanix-cloud-native/prism-go-client/pkg/nutanix"
	prismGoClientV3 "github.com/nutanix-cloud-native/prism-go-client/pkg/nutanix/v3"
	. "github.com/onsi/gomega"

	nutanixClientHelper "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/client"
)

var (
	nutanixEndpoint string
	nutanixPort     string
	nutanixInsecure string
)

func init() {
	flag.StringVar(&nutanixEndpoint, "e2e.nutanixEndpoint", os.Getenv("NUTANIX_ENDPOINT"), "the Nutanix Prism Central used for e2e tests")
	flag.StringVar(&nutanixPort, "e2e.nutanixPort", os.Getenv("NUTANIX_PORT"), "the Nutanix Prism Central port used for e2e tests")
	flag.StringVar(&nutanixInsecure, "e2e.nutanixInsecure", os.Getenv("NUTANIX_INSECURE"), "Ignore certificate checks for e2e tests")
}

type nutanixCredentials struct {
	nutanixUsername string
	nutanixPassword string
}

func getNutanixCredentialsFromEnvironment() nutanixCredentials {
	nutanixUsername := os.Getenv(nutanixUserKey)
	Expect(nutanixUsername).ToNot(BeEmpty(), "expected environment variable %s to be set", nutanixUserKey)
	nutanixPassword := os.Getenv(nutanixPasswordKey)
	Expect(nutanixPassword).ToNot(BeEmpty(), "expected environment variable %s to be set", nutanixPasswordKey)
	return nutanixCredentials{
		nutanixUsername: nutanixUsername,
		nutanixPassword: nutanixPassword,
	}
}

func initNutanixClient() (*prismGoClientV3.Client, error) {
	insecureBool, err := strconv.ParseBool(nutanixInsecure)
	if err != nil {
		return nil, err
	}

	c := getNutanixCredentialsFromEnvironment()
	creds := prismGoClient.Credentials{
		Insecure: insecureBool,
		Port:     nutanixPort,
		Endpoint: nutanixEndpoint,
		Username: c.nutanixUsername,
		Password: c.nutanixPassword,
	}

	nutanixClient, err := nutanixClientHelper.Client(creds, nutanixClientHelper.ClientOptions{})
	if err != nil {
		return nil, err
	}

	_, err = nutanixClient.V3.GetCurrentLoggedInUser(ctx)
	if err != nil {
		return nil, err
	}

	return nutanixClient, nil
}
