# VSCode Integration

Some of the unit tests use the [envtest package](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/envtest) from the controller-runtime project to create a temporary Kubernetes API server.

Envtest reads the location of the necessary binaries, i.e. etcd and kube-apiserver, from an environment variable. This environment variable is initialized by our make targets, but VSCode does not run tests using make.

We can use a VSCode task to write the location to a file, and configure vscode-go, which runs the tests, to initialize its environment from this file.

To run envtest-based tests from VSCode, follow these steps:

1. Install setup-envtest (and other build/test dependencies) by running `devbox install`.
2. Copy `settings.json` and `tasks.json` in this directory into the `.vscode` folder at the root of the repository.
3. Restart VSCode.
