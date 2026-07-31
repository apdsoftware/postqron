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
	deleteErr    error
	deleteStart  chan string
	deleteWait   <-chan struct{}
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
	start := store.deleteStart
	wait := store.deleteWait
	deleteErr := store.deleteErr
	store.mutex.Unlock()
	if start != nil {
		start <- key
	}
	if wait != nil {
		<-wait
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if deleteErr != nil {
		return deleteErr
	}
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
	if media.Kind != MediaVideo ||
		!media.MoovBeforeMediaData ||
		media.Width != 1080 ||
		media.Height != 1920 ||
		media.DurationSeconds != 30 ||
		media.VideoCodec != "h264" ||
		media.AudioCodec != "aac" ||
		!media.HasAudio {
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
	mvhd := make([]byte, 20)
	mvhd[0] = 0
	binary.BigEndian.PutUint32(mvhd[12:16], 1000)
	binary.BigEndian.PutUint32(mvhd[16:20], 30000)

	videoTkhd := makeTrackHeaderPayload(1080, 1920)
	videoMdhd := makeMediaHeaderPayload(30000)
	videoHdlr := makeHandlerPayload("vide")
	videoStsd := makeSampleDescriptionPayload("avc1")
	videoTrak := makeMP4Box("trak", makeMP4Box("tkhd", videoTkhd), makeMP4Box("mdia",
		makeMP4Box("mdhd", videoMdhd),
		makeMP4Box("hdlr", videoHdlr),
		makeMP4Box("minf", makeMP4Box("stbl", makeMP4Box("stsd", videoStsd))),
	))

	audioTkhd := makeTrackHeaderPayload(0, 0)
	audioMdhd := makeMediaHeaderPayload(30000)
	audioHdlr := makeHandlerPayload("soun")
	audioStsd := makeSampleDescriptionPayload("mp4a")
	audioTrak := makeMP4Box("trak", makeMP4Box("tkhd", audioTkhd), makeMP4Box("mdia",
		makeMP4Box("mdhd", audioMdhd),
		makeMP4Box("hdlr", audioHdlr),
		makeMP4Box("minf", makeMP4Box("stbl", makeMP4Box("stsd", audioStsd))),
	))

	return bytes.Join([][]byte{
		makeMP4Box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2mp41")),
		makeMP4Box("moov", makeMP4Box("mvhd", mvhd), videoTrak, audioTrak),
		makeMP4Box("mdat", []byte{0x00, 0x00, 0x00, 0x00}),
	}, nil)
}

func makeMP4Box(name string, payloads ...[]byte) []byte {
	payload := bytes.Join(payloads, nil)
	result := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
	copy(result[4:8], []byte(name))
	copy(result[8:], payload)
	return result
}

func makeTrackHeaderPayload(width, height int) []byte {
	payload := make([]byte, 84)
	payload[0] = 0
	binary.BigEndian.PutUint32(payload[len(payload)-8:len(payload)-4], uint32(width<<16))
	binary.BigEndian.PutUint32(payload[len(payload)-4:], uint32(height<<16))
	return payload
}

func makeMediaHeaderPayload(duration uint32) []byte {
	payload := make([]byte, 20)
	payload[0] = 0
	binary.BigEndian.PutUint32(payload[12:16], 1000)
	binary.BigEndian.PutUint32(payload[16:20], duration)
	return payload
}

func makeHandlerPayload(handler string) []byte {
	payload := make([]byte, 24)
	copy(payload[8:12], []byte(handler))
	return payload
}

func makeSampleDescriptionPayload(codec string) []byte {
	payload := make([]byte, 24)
	binary.BigEndian.PutUint32(payload[4:8], 1)
	binary.BigEndian.PutUint32(payload[8:12], 16)
	copy(payload[12:16], []byte(codec))
	return payload
}

func containsJSONField(encoded []byte, field string) bool {
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return false
	}
	_, found := object[field]
	return found
}
