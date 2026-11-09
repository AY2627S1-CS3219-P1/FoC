package main

import (
	"reflect"
	"testing"
)

// The production ingress contracts require local Secrets only for HTTPS
// listeners. API Gateway terminates HTTPS before the private HTTP listener.
func TestGatewayTLSSecrets(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
		want     []gatewaySecret
		wantErr  bool
	}{
		{
			name: "private HTTP behind API Gateway",
			manifest: `{"kind":"ConfigMap"}
{"kind":"Gateway","metadata":{"name":"foc-public","namespace":"istio-ingress"},"spec":{"listeners":[{"protocol":"HTTP","port":80}]}}`,
		},
		{
			name:     "existing HTTPS certificate in a List",
			manifest: `{"kind":"List","items":[{"kind":"Gateway","metadata":{"name":"foc-public","namespace":"istio-ingress"},"spec":{"listeners":[{"protocol":"HTTPS","tls":{"certificateRefs":[{"name":"foc-public-tls"}]}}]}}]}`,
			want:     []gatewaySecret{{namespace: "istio-ingress", name: "foc-public-tls"}},
		},
		{
			name:     "HTTPS without certificate fails",
			manifest: `{"kind":"Gateway","metadata":{"name":"foc-public","namespace":"istio-ingress"},"spec":{"listeners":[{"protocol":"HTTPS"}]}}`,
			wantErr:  true,
		},
		{name: "missing gateway fails", manifest: `{"kind":"ConfigMap"}`, wantErr: true},
		{name: "invalid JSON fails", manifest: `invalid`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gatewayTLSSecrets(tc.manifest)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
			if err == nil && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Secrets = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPrivateHTTPGatewayPreflightDoesNotReadTLSSecret(t *testing.T) {
	op := testOperator(t)
	fakeCommand(t, "kubectl", `case "$*" in
  *'create --dry-run=client --validate=false -k '*'-o json'*)
    printf '%s' '{"kind":"Gateway","metadata":{"name":"foc-public","namespace":"istio-ingress"},"spec":{"listeners":[{"protocol":"HTTP"}]}}';;
  *) echo "unexpected command: $*" >&2; exit 1;;
esac
`)
	if err := op.checkGatewayTLSSecrets(); err != nil {
		t.Fatal(err)
	}
}
