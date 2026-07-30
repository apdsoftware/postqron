package composer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeObject struct {
	contentType string
	content     []byte
	retained    bool
}

type fakeObjectStore struct {
	mutex        sync.Mutex
	objects      map[string]fakeObject
	uploadKey    string
	uploadType   string
	uploadSize   int64
	deletedKeys  []string
	retainCalls  []string
	tempCalls    []string
	retainErr    error
	temporaryErr error
}

func newFakeObjectStore() *fakeObjectStore {
	return &fakeObjectStore{objects: make(map[string]fakeObject)}
}

func (store *fakeObjectStore) AuthorizeUpload(
	_ context.Context,
	key, contentType string,
	size int64,
	expiresAt time.Time,
) (SignedObjectURL, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.uploadKey = key
	store.uploadType = contentType
	store.uploadSize = size
	return SignedObjectURL{
		URL: "https://objects.example/upload?signature=test",
		Headers: map[string]string{
			"Content-Type": contentType,
		},
		ExpiresAt: expiresAt,
	}, nil
}

func (store *fakeObjectStore) AuthorizeDownload(
	_ context.Context,
	key string,
	expiresAt time.Time,
) (SignedObjectURL, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if _, found := store.objects[key]; !found {
		return SignedObjectURL{}, ErrNotFound
	}
	return SignedObjectURL{
		URL:       "https://objects.example/download?signature=test",
		Headers:   map[string]string{},
		ExpiresAt: expiresAt,
	}, nil
}

func (store *fakeObjectStore) Stat(
	_ context.Context,
	key string,
) (ObjectInfo, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	object, found := store.objects[key]
	if !found {
		return ObjectInfo{}, ErrNotFound
	}
	return ObjectInfo{
		SizeBytes:   int64(len(object.content)),
		ContentType: object.contentType,
	}, nil
}

func (store *fakeObjectStore) Open(
	_ context.Context,
	key string,
) (io.ReadCloser, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	object, found := store.objects[key]
	if !found {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(object.content)), nil
}

func (store *fakeObjectStore) Retain(_ context.Context, key string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.retainCalls = append(store.retainCalls, key)
	if store.retainErr != nil {
		return store.retainErr
	}
	object, found := store.objects[key]
	if !found {
		return ErrNotFound
	}
	object.retained = true
	store.objects[key] = object
	return nil
}

func (store *fakeObjectStore) MakeTemporary(_ context.Context, key string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.tempCalls = append(store.tempCalls, key)
	if store.temporaryErr != nil {
		return store.temporaryErr
	}
	object, found := store.objects[key]
	if !found {
		return ErrNotFound
	}
	object.retained = false
	store.objects[key] = object
	return nil
}

func (store *fakeObjectStore) Delete(_ context.Context, key string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.objects, key)
	store.deletedKeys = append(store.deletedKeys, key)
	return nil
}

func (store *fakeObjectStore) putAuthorized(content []byte, contentType string) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.objects[store.uploadKey] = fakeObject{
		contentType: contentType,
		content:     append([]byte{}, content...),
	}
}

func TestStreamMediaInspectorUsesObjectBytesAndProducesClientSafeMetadata(
	t *testing.T,
) {
	png := testPNG(t)
	media, err := (StreamMediaInspector{}).Inspect(
		context.Background(),
		bytes.NewReader(png),
		ObjectInfo{SizeBytes: int64(len(png)), ContentType: "image/png"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if media.Kind != MediaImage ||
		media.ContentType != "image/png" ||
		media.Width != 1 ||
		media.Height != 1 ||
		media.InspectionStatus != InspectionReady {
		t.Fatalf("inspected media = %#v", media)
	}
	media.StorageKey = "internal/secret/key"
	encoded, err := json.Marshal(media)
	if err != nil {
		t.Fatal(err)
	}
	if containsJSONField(encoded, "storage_key") ||
		containsJSONField(encoded, "object_key") ||
		containsJSONField(encoded, "provider_credentials") {
		t.Fatalf("client media leaked internal fields: %s", encoded)
	}
}

func TestStreamMediaInspectorRejectsTruncatedOrCorruptImageContainer(t *testing.T) {
	png := testPNG(t)
	for name, content := range map[string][]byte{
		"missing IEND": png[:len(png)-12],
		"invalid CRC":  append(append([]byte{}, png[:len(png)-1]...), png[len(png)-1]^0xff),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (StreamMediaInspector{}).Inspect(
				context.Background(),
				bytes.NewReader(content),
				ObjectInfo{SizeBytes: int64(len(content)), ContentType: "image/png"},
			)
			var fieldError *FieldRuleError
			if !errors.As(err, &fieldError) ||
				fieldError.Code != "media_image_invalid" {
				t.Fatalf("invalid PNG error = %#v", err)
			}
		})
	}
}

func TestStreamMediaInspectorValidatesMP4StructureWithoutBufferingObject(
	t *testing.T,
) {
	mp4 := testMP4()
	media, err := (StreamMediaInspector{}).Inspect(
		context.Background(),
		bytes.NewReader(mp4),
		ObjectInfo{SizeBytes: int64(len(mp4)), ContentType: "video/mp4"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if media.Kind != MediaVideo || !media.MoovBeforeMediaData {
		t.Fatalf("inspected MP4 = %#v", media)
	}
	truncated := mp4[:len(mp4)-1]
	_, err = (StreamMediaInspector{}).Inspect(
		context.Background(),
		bytes.NewReader(truncated),
		ObjectInfo{SizeBytes: int64(len(mp4)), ContentType: "video/mp4"},
	)
	var fieldError *FieldRuleError
	if !errors.As(err, &fieldError) || fieldError.Code != "media_video_invalid" {
		t.Fatalf("truncated MP4 error = %#v", err)
	}
}

func TestUnavailableObjectStoreFailsClosed(t *testing.T) {
	_, err := (unavailableObjectStore{}).AuthorizeUpload(
		context.Background(),
		"key",
		"image/png",
		1,
		time.Now().Add(time.Minute),
	)
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("unconfigured storage error = %v", err)
	}
}

func TestS3ObjectStoreRequiresHTTPSByDefault(t *testing.T) {
	config := S3ObjectStoreConfig{
		Endpoint:        "http://objects.example",
		Region:          "test-region",
		Bucket:          "test-bucket",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
	}
	if _, err := NewS3ObjectStore(config); err == nil {
		t.Fatal("non-loopback HTTP S3 endpoint was accepted by default")
	} else if strings.Contains(err.Error(), config.AccessKeyID) ||
		strings.Contains(err.Error(), config.SecretAccessKey) {
		t.Fatalf("storage error leaked credentials: %v", err)
	}
	config.Endpoint = "http://127.0.0.1:9000"
	if _, err := NewS3ObjectStore(config); err != nil {
		t.Fatalf("loopback development endpoint rejected: %v", err)
	}
	config.Endpoint = "http://objects.example"
	config.AllowInsecureEndpoint = true
	if _, err := NewS3ObjectStore(config); err != nil {
		t.Fatalf("explicit insecure development endpoint rejected: %v", err)
	}
	config.Endpoint = "https://objects.example"
	config.AllowInsecureEndpoint = false
	store, err := NewS3ObjectStore(config)
	if err != nil {
		t.Fatalf("HTTPS endpoint rejected: %v", err)
	}
	signed, err := store.AuthorizeUpload(
		context.Background(),
		"f06/tmp/workspace/media/image.png",
		"image/png",
		64,
		time.Now().Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("sign HTTPS upload: %v", err)
	}
	if !strings.HasPrefix(signed.URL, "https://") ||
		strings.Contains(signed.URL, config.SecretAccessKey) ||
		signed.Headers["Content-Length"] != "64" {
		t.Fatalf("unsafe signed upload contract: url=%q headers=%v", signed.URL, signed.Headers)
	}
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	png, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	return png
}

func testMP4() []byte {
	var result bytes.Buffer
	for _, box := range []struct {
		name    string
		payload []byte
	}{
		{name: "ftyp", payload: []byte("isom0000")},
		{name: "moov"},
		{name: "mdat"},
	} {
		_ = binary.Write(&result, binary.BigEndian, uint32(8+len(box.payload)))
		result.WriteString(box.name)
		result.Write(box.payload)
	}
	return result.Bytes()
}

func containsJSONField(encoded []byte, field string) bool {
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return false
	}
	_, found := object[field]
	return found
}
