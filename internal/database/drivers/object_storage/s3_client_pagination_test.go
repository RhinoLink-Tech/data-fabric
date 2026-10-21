package object_storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestS3ClientListObjectsAllFollowsContinuationToken(t *testing.T) {
	requestTokens := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") != "2" || r.URL.Query().Get("prefix") != "table/" {
			t.Errorf("unexpected ListObjectsV2 request: %s", r.URL.String())
		}
		token := r.URL.Query().Get("continuation-token")
		requestTokens = append(requestTokens, token)
		w.Header().Set("Content-Type", "application/xml")
		if token == "" {
			_, _ = w.Write([]byte(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>test-bucket</Name><Prefix>table/</Prefix><KeyCount>1</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>true</IsTruncated><NextContinuationToken>page-2</NextContinuationToken><Contents><Key>table/a</Key><Size>10</Size></Contents></ListBucketResult>`))
			return
		}
		if token != "page-2" {
			t.Errorf("unexpected continuation token %q", token)
		}
		_, _ = w.Write([]byte(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>test-bucket</Name><Prefix>table/</Prefix><KeyCount>1</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated><Contents><Key>table/b</Key><Size>20</Size></Contents></ListBucketResult>`))
	}))
	defer server.Close()

	client, err := NewS3Client(context.Background(), &S3Config{
		Region: "us-east-1", Bucket: "test-bucket",
		AccessKey: "test-access-key", SecretKey: "test-secret-key",
		EndpointURL: server.URL, ForcePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3Client returned error: %v", err)
	}
	objects, err := client.ListObjectsAll(context.Background(), "table/")
	if err != nil {
		t.Fatalf("ListObjectsAll returned error: %v", err)
	}
	if got, want := []string{objects[0].Key, objects[1].Key}, []string{"table/a", "table/b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("object keys = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(requestTokens, []string{"", "page-2"}) {
		t.Fatalf("continuation tokens = %#v", requestTokens)
	}
}
