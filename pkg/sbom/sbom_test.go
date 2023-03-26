package sbom

import (
	"sniffer/pkg/storageclient"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1"
)

const (
	NGINX_WLID string = "wlid://cluster-myCluster/namespace-any/deployment-nginx"
)

func TestGetSBOM(t *testing.T) {
	SBOMClient := CreateSBOMStorageClient(storageclient.CreateSBOMStorageHttpClientMock(), instanceidhandler.InstanceID{})
	err := SBOMClient.GetSBOM(storageclient.NGINX)
	if err != nil {
		t.Fatalf("fail to get sbom")
	}

}

func TestFilterSBOM(t *testing.T) {
	SBOMClient := CreateSBOMStorageClient(storageclient.CreateSBOMStorageHttpClientMock(), instanceidhandler.InstanceID{})
	err := SBOMClient.GetSBOM(storageclient.NGINX)
	if err != nil {
		t.Fatalf("fail to get sbom")
	}
	err = SBOMClient.FilterSBOM(map[string]bool{
		"/usr/share/adduser/adduser.conf": true,
	})
	if err != nil {
		t.Fatalf("fail to filter sbom")
	}

}

func TestStoreFilterSBOM(t *testing.T) {
	SBOMClient := CreateSBOMStorageClient(storageclient.CreateSBOMStorageHttpClientMock(), instanceidhandler.InstanceID{})
	err := SBOMClient.GetSBOM(storageclient.NGINX)
	if err != nil {
		t.Fatalf("fail to get sbom")
	}
	err = SBOMClient.FilterSBOM(map[string]bool{
		"/usr/share/adduser/adduser.conf": true,
	})
	if err != nil {
		t.Fatalf("fail to filter sbom")
	}
	err = SBOMClient.StoreFilterSBOM("anyInstanceID")
	if err != nil {
		t.Fatalf("fail to store filter sbom")
	}

}

func TestStoreFilterSBOMFailure(t *testing.T) {
	SBOMClient := CreateSBOMStorageClient(storageclient.CreateStorageHttpClientFailureMock(), instanceidhandler.InstanceID{})
	err := SBOMClient.GetSBOM(storageclient.NGINX)
	if err != nil {
		t.Fatalf("fail to get sbom")
	}
	err = SBOMClient.FilterSBOM(map[string]bool{
		"/usr/share/adduser/adduser.conf": true,
	})
	if err != nil {
		t.Fatalf("fail to filter sbom")
	}
	err = SBOMClient.StoreFilterSBOM("anyInstanceID")
	if err == nil {
		t.Fatalf("StoreFilterSBOM should fail")
	}

}
