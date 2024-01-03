package v3

import (
	"fmt"

	"net/http"
	"net/http/httptest"
	"path"

	prismgoclient "github.com/nutanix-cloud-native/prism-go-client"
	nutanixClientV3 "github.com/nutanix-cloud-native/prism-go-client/v3"
)

const (
	baseURLPath = "/api/nutanix/v3/"
)

type TestClient struct {
	*nutanixClientV3.Client

	mux    *http.ServeMux
	server *httptest.Server
}

func NewTestClient() (*TestClient, error) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	cred := prismgoclient.Credentials{
		URL:      server.URL,
		Username: "username",
		Password: "password",
		Endpoint: "0.0.0.0",
	}

	client, err := nutanixClientV3.NewV3Client(cred)
	if err != nil {
		return nil, fmt.Errorf("error creating Nutanix test client: %w", err)
	}
	return &TestClient{client, mux, server}, nil
}

func (c *TestClient) Close() {
	c.server.Close()
}

func (c *TestClient) AddHandler(pattern string, handler func(w http.ResponseWriter, r *http.Request)) {
	c.mux.HandleFunc(pattern, handler)
}

func GetTaskURLPath(uuid string) string {
	return path.Join(baseURLPath, "tasks", uuid)
}
