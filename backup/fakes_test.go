package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// fakeObject is an in-memory S3 object.
type fakeObject struct {
	body     []byte
	metadata map[string]string
	modified time.Time
}

// fakeS3 is an in-memory implementation of S3API for tests.
type fakeS3 struct {
	objects map[string]*fakeObject
	clock   time.Time
	puts    int // number of successful PutObject calls

	// error injection
	listErr   error
	putErr    error
	deleteErr error
	headErr   error
	getErr    error
}

func newFakeS3() *fakeS3 {
	return &fakeS3{
		objects: map[string]*fakeObject{},
		clock:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// seed stores an object with its sha256 recorded in metadata.
func (f *fakeS3) seed(key string, body []byte, modified time.Time) {
	f.objects[key] = &fakeObject{
		body:     body,
		metadata: map[string]string{"sha256": checksum(body)},
		modified: modified,
	}
}

func (f *fakeS3) HeadObject(_ context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if f.headErr != nil {
		return nil, f.headErr
	}
	obj, ok := f.objects[*params.Key]
	if !ok {
		return nil, fmt.Errorf("NotFound: %s", *params.Key)
	}
	return &s3.HeadObjectOutput{Metadata: obj.metadata}, nil
}

func (f *fakeS3) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	obj, ok := f.objects[*params.Key]
	if !ok {
		return nil, fmt.Errorf("NoSuchKey: %s", *params.Key)
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(obj.body))}, nil
}

func (f *fakeS3) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if f.putErr != nil {
		return nil, f.putErr
	}
	body, err := io.ReadAll(params.Body)
	if err != nil {
		return nil, err
	}
	f.objects[*params.Key] = &fakeObject{
		body:     body,
		metadata: params.Metadata,
		modified: f.clock,
	}
	f.clock = f.clock.Add(time.Second)
	f.puts++
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	prefix := ""
	if params.Prefix != nil {
		prefix = *params.Prefix
	}
	var contents []types.Object
	for key, obj := range f.objects {
		if strings.HasPrefix(key, prefix) {
			contents = append(contents, types.Object{
				Key:          aws.String(key),
				LastModified: aws.Time(obj.modified),
			})
		}
	}
	return &s3.ListObjectsV2Output{Contents: contents}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	delete(f.objects, *params.Key)
	return &s3.DeleteObjectOutput{}, nil
}

// staticDump returns a Dumper that always yields body.
func staticDump(body []byte) Dumper {
	return func(context.Context, DatabaseConfig) ([]byte, error) {
		return body, nil
	}
}

// failingDump returns a Dumper that always errors.
func failingDump(err error) Dumper {
	return func(context.Context, DatabaseConfig) ([]byte, error) {
		return nil, err
	}
}

// fixedClock returns a func usable as Handler.now.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}
