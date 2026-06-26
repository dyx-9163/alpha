package httpapi

import "testing"

func TestServerSaveRequestCarriesSecretsIntoStoreModel(t *testing.T) {
	req := serverSaveRequest{
		Name:       "db-1",
		Host:       "10.0.0.1",
		Username:   "root",
		Password:   "secret",
		PrivateKey: "key",
	}
	server := req.toStoreServer()
	if server.Password != "secret" {
		t.Fatalf("password was not carried into store model")
	}
	if server.PrivateKey != "key" {
		t.Fatalf("private key was not carried into store model")
	}
}
