package composer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type S3ObjectStoreConfig struct {
	Endpoint              string
	Region                string
	Bucket                string
	AccessKeyID           string
	SecretAccessKey       string
	PathStyle             bool
	AllowInsecureEndpoint bool
}

type S3ObjectStore struct {
	bucket    string
	client    *s3.Client
	presigner *s3.PresignClient
}

func NewS3ObjectStore(config S3ObjectStoreConfig) (*S3ObjectStore, error) {
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	config.Region = strings.TrimSpace(config.Region)
	config.Bucket = strings.TrimSpace(config.Bucket)
	config.AccessKeyID = strings.TrimSpace(config.AccessKeyID)
	config.SecretAccessKey = strings.TrimSpace(config.SecretAccessKey)
	if config.Endpoint == "" || config.Region == "" || config.Bucket == "" ||
		config.AccessKeyID == "" || config.SecretAccessKey == "" {
		return nil, errors.New("complete S3-compatible composer storage configuration is required")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" ||
		(endpoint.Scheme != "https" && endpoint.Scheme != "http") ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("composer S3 endpoint must be an absolute HTTP(S) URL")
	}
	if endpoint.Scheme == "http" &&
		!isLoopbackEndpoint(endpoint.Hostname()) &&
		!config.AllowInsecureEndpoint {
		return nil, errors.New(
			"composer S3 endpoint must use HTTPS unless an insecure endpoint is explicitly allowed",
		)
	}
	awsConfig := aws.Config{
		Region: config.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			config.AccessKeyID,
			config.SecretAccessKey,
			"",
		),
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(strings.TrimRight(config.Endpoint, "/"))
		options.UsePathStyle = config.PathStyle
	})
	return &S3ObjectStore{
		bucket:    config.Bucket,
		client:    client,
		presigner: s3.NewPresignClient(client),
	}, nil
}

func isLoopbackEndpoint(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.TrimSpace(host))
	return address != nil && address.IsLoopback()
}

func (store *S3ObjectStore) AuthorizeUpload(
	ctx context.Context,
	objectKey, contentType string,
	sizeBytes int64,
	expiresAt time.Time,
) (SignedObjectURL, error) {
	expires := time.Until(expiresAt)
	if expires <= 0 {
		return SignedObjectURL{}, errors.New("composer upload signing expiry must be in the future")
	}
	result, err := store.presigner.PresignPutObject(
		ctx,
		&s3.PutObjectInput{
			Bucket:        aws.String(store.bucket),
			Key:           aws.String(objectKey),
			ContentLength: aws.Int64(sizeBytes),
			ContentType:   aws.String(contentType),
			Tagging:       aws.String("postqron-lifecycle=temporary"),
		},
		func(options *s3.PresignOptions) {
			options.Expires = expires
		},
	)
	if err != nil {
		return SignedObjectURL{}, fmt.Errorf("presign S3 upload: %w", err)
	}
	headers := make(map[string]string, len(result.SignedHeader)+1)
	for key, values := range result.SignedHeader {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	headers["Content-Length"] = strconv.FormatInt(sizeBytes, 10)
	return SignedObjectURL{
		URL:       result.URL,
		Headers:   headers,
		ExpiresAt: expiresAt,
	}, nil
}

func (store *S3ObjectStore) AuthorizeDownload(
	ctx context.Context,
	objectKey string,
	expiresAt time.Time,
) (SignedObjectURL, error) {
	expires := time.Until(expiresAt)
	if expires <= 0 {
		return SignedObjectURL{}, errors.New("composer download signing expiry must be in the future")
	}
	result, err := store.presigner.PresignGetObject(
		ctx,
		&s3.GetObjectInput{
			Bucket: aws.String(store.bucket),
			Key:    aws.String(objectKey),
		},
		func(options *s3.PresignOptions) {
			options.Expires = expires
		},
	)
	if err != nil {
		return SignedObjectURL{}, fmt.Errorf("presign S3 download: %w", err)
	}
	return SignedObjectURL{
		URL:       result.URL,
		Headers:   map[string]string{},
		ExpiresAt: expiresAt,
	}, nil
}

func (store *S3ObjectStore) Stat(
	ctx context.Context,
	objectKey string,
) (ObjectInfo, error) {
	result, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("head S3 object: %w", err)
	}
	if result.ContentLength == nil || *result.ContentLength < 1 {
		return ObjectInfo{}, errors.New("S3 object has an invalid content length")
	}
	return ObjectInfo{
		SizeBytes:   *result.ContentLength,
		ContentType: aws.ToString(result.ContentType),
	}, nil
}

func (store *S3ObjectStore) Open(
	ctx context.Context,
	objectKey string,
) (io.ReadCloser, error) {
	result, err := store.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return nil, fmt.Errorf("get S3 object: %w", err)
	}
	return result.Body, nil
}

func (store *S3ObjectStore) Retain(
	ctx context.Context,
	objectKey string,
) error {
	_, err := store.client.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(objectKey),
		Tagging: &types.Tagging{TagSet: []types.Tag{{
			Key:   aws.String("postqron-lifecycle"),
			Value: aws.String("retained"),
		}}},
	})
	if err != nil {
		return fmt.Errorf("retain S3 object: %w", err)
	}
	return nil
}

func (store *S3ObjectStore) Delete(
	ctx context.Context,
	objectKey string,
) error {
	_, err := store.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(store.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return fmt.Errorf("delete S3 object: %w", err)
	}
	return nil
}
