package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	prismGoClient "github.com/nutanix-cloud-native/prism-go-client"
	v4Converged "github.com/nutanix-cloud-native/prism-go-client/converged/v4"
	prismGoClientTypes "github.com/nutanix-cloud-native/prism-go-client/environment/types"

	infrav1 "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/api/v1beta1"
	"github.com/nutanix-cloud-native/cluster-api-provider-nutanix/controllers"
	nctx "github.com/nutanix-cloud-native/cluster-api-provider-nutanix/pkg/context"
)

func main() {
	ctx := context.Background()

	endpoint := mustEnv("NUTANIX_ENDPOINT")
	port := envOr("NUTANIX_PORT", "9440")
	insecureStr := envOr("NUTANIX_INSECURE", "true")
	additionalTrustBundle := os.Getenv("NUTANIX_ADDITIONAL_TRUST_BUNDLE")

	// Use project-scoped user if set, otherwise fall back to NUTANIX_USER.
	// For CI: NUTANIX_PROJECT_SCOPE_USER / NUTANIX_PROJECT_SCOPE_PASSWORD
	user := os.Getenv("NUTANIX_PROJECT_SCOPE_USER")
	pass := os.Getenv("NUTANIX_PROJECT_SCOPE_PASSWORD")
	if user == "" {
		user = mustEnv("NUTANIX_USER")
		pass = mustEnv("NUTANIX_PASSWORD")
	}
	apiKey := os.Getenv("NUTANIX_API_KEY")

	creds := prismGoClient.Credentials{
		Endpoint: endpoint,
		Port:     port,
		URL:      fmt.Sprintf("https://%s:%s", endpoint, port),
		Username: user,
		Password: pass,
		APIKey:   apiKey,
	}
	if insecureStr != "" {
		b, err := strconv.ParseBool(insecureStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid NUTANIX_INSECURE=%q: %v\n", insecureStr, err)
			os.Exit(1)
		}
		creds.Insecure = b
	}

	// Validate like e2e does
	apiCreds := prismGoClientTypes.ApiCredentials{
		APIKey:   apiKey,
		Username: user,
		Password: pass,
	}
	if err := apiCreds.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "invalid credentials: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Using user %q against %s (insecure=%v)\n", user, creds.URL, creds.Insecure)

	convergedClient, err := v4Converged.NewClient(creds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create converged client: %v\n", err)
		os.Exit(1)
	}
	if additionalTrustBundle != "" {
		fmt.Println("Using additional trust bundle (set NUTANIX_INSECURE=false and configure PEM bundle if needed)")
	}

	// 1) List all projects via v4 (what GetProjectV4 ultimately uses under the hood)
	fmt.Println("\n=== v4 Projects.List ===")
	projects, err := convergedClient.Projects.List(ctx)
	if err != nil {
		fmt.Printf("Projects.List failed: %v\n", err)
	} else {
		fmt.Printf("Found %d project(s):\n", len(projects))
		for _, p := range projects {
			fmt.Printf("  - name=%q extId=%q\n", ptrStr(p.Name), ptrStr(p.ExtId))
		}
	}

	// 2) GetProjectV4 for coke and pepsi (same user, same PC)
	for _, name := range []string{"coke", "pepsi"} {
		fmt.Printf("\n=== GetProjectV4(%q) ===\n", name)
		rctx := &nctx.MachineContext{
			Context:        ctx,
			ConvergedClient: convergedClient,
		}
		ref := &infrav1.NutanixResourceIdentifier{
			Type: infrav1.NutanixIdentifierName,
			Name: &name,
		}
		info, err := controllers.GetProjectV4(rctx, ref)
		if err != nil {
			fmt.Printf("GetProjectV4(%q) error: %v\n", name, err)
			continue
		}
		fmt.Printf("GetProjectV4(%q) => extId=%q name=%q\n", name, ptrStr(info.ExtID), ptrStr(info.Name))
	}
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		fmt.Fprintf(os.Stderr, "required env %s not set\n", k)
		os.Exit(1)
	}
	return v
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func ptrStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
