# Gateway API CRDs

`standard-install.yaml` is the Gateway API v1.6.0 Standard channel release bundle
from <https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.0/standard-install.yaml>.

Upstream SHA-256: `a557172e8348f758479e9ee4000bbbb4b4aa48302a6b73461823ea5349bad56d`
The vendored copy removes trailing whitespace. Its SHA-256 is
`79e926f16db2fb3e23e632f8a96e29760680edd814c353d06cc47868093573e2`.

The bundle is installed only into the managed local kind cluster. On an external
cluster, the operator checks that the Standard Gateway API v1 resources are
already served and does not replace shared CRDs.
