package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

type memoryObject struct {
	data []byte
	meta map[string]string
	opts minio.PutObjectOptions
}
type memoryClient struct{ objects map[string]memoryObject }

func (m *memoryClient) PutObject(_ context.Context, bucket, key string, r io.Reader, _ int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	if m.objects == nil {
		m.objects = make(map[string]memoryObject)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return minio.UploadInfo{}, err
	}
	m.objects[bucket+"/"+key] = memoryObject{data: b, meta: opts.UserMetadata, opts: opts}
	return minio.UploadInfo{}, nil
}
func (m *memoryClient) GetObject(_ context.Context, bucket, key string, _ minio.GetObjectOptions) (io.ReadCloser, error) {
	o, ok := m.objects[bucket+"/"+key]
	if !ok {
		return nil, errors.New("object not found")
	}
	return io.NopCloser(bytes.NewReader(o.data)), nil
}
func (m *memoryClient) RemoveObject(_ context.Context, bucket, key string, _ minio.RemoveObjectOptions) error {
	delete(m.objects, bucket+"/"+key)
	return nil
}

func TestEncryptedStorePutGetDeleteAndIntegrity(t *testing.T) {
	m := &memoryClient{}
	s := New(m, "bucket", [32]byte{1})
	ref, err := s.Put(context.Background(), Payload{TenantID: "t", Namespace: "n", ID: "i", Value: map[string]string{"hello": "world"}})
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimPrefix(ref.URI, "object://bucket/")
	obj := m.objects["bucket/"+key]
	if bytes.Contains(obj.data, []byte("world")) {
		t.Fatal("ciphertext contains plaintext")
	}
	if obj.opts.ContentType != "application/octet-stream" {
		t.Fatalf("content type %q", obj.opts.ContentType)
	}
	raw := []byte(`{"hello":"world"}`)
	h := sha256.Sum256(raw)
	if obj.meta["sha256"] != hex.EncodeToString(h[:]) {
		t.Fatalf("metadata hash %q", obj.meta["sha256"])
	}
	got, err := s.Get(context.Background(), ref)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("Get() got %q err %v", got, err)
	}
	obj.data[0] ^= 1
	m.objects["bucket/"+key] = obj
	if _, err := s.Get(context.Background(), ref); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	obj.data[0] ^= 1
	m.objects["bucket/"+key] = obj
	ref.SHA256 = strings.Repeat("0", 64)
	if _, err := s.Get(context.Background(), ref); err == nil {
		t.Fatal("tampered hash accepted")
	}
	ref.SHA256 = hex.EncodeToString(h[:])
	if err := s.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.objects["bucket/"+key]; ok {
		t.Fatal("object not deleted")
	}
}

func TestEncryptedStoreRejectsInvalidAndOversized(t *testing.T) {
	s := New(&memoryClient{}, "bucket", [32]byte{1})
	for _, uri := range []string{"", "http://bucket/x", "object://other/x", "object://bucket/", "object://bucket/a//b"} {
		if _, err := s.Get(context.Background(), Reference{URI: uri}); err == nil {
			t.Errorf("accepted invalid URI %q", uri)
		}
	}
	ref, err := s.Put(context.Background(), Payload{Value: "large"})
	if err != nil {
		t.Fatal(err)
	}
	s.maxBytes = 1
	if _, err := s.Get(context.Background(), ref); err == nil {
		t.Fatal("accepted oversized object")
	}
}
