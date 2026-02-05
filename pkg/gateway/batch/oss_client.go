package batch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/aliyun/credentials-go/credentials"
)

// OSSClient provides operations for OSS storage
type OSSClient struct {
	client     *oss.Client
	bucket     *oss.Bucket
	bucketName string
}

// NewOSSClient creates a new OSS client
func NewOSSClient(endpoint, bucketName string) (*OSSClient, error) {
	if endpoint == "" {
		// Get region from environment variable
		region := os.Getenv("REGION")
		// Construct OSS endpoint using region
		endpoint = fmt.Sprintf("oss-%s-internal.aliyuncs.com", region)
	}

	clientOptions := []oss.ClientOption{}
	if len(os.Getenv("ALIBABA_CLOUD_CREDENTIALS_URI")) > 0 {
		config := new(credentials.Config).SetType("credentials_uri") // will get credentials from env ALIBABA_CLOUD_CREDENTIALS_URI
		uriCredential, err := credentials.NewCredential(config)
		if err != nil {
			return nil, fmt.Errorf("failed to new uri credential: %v", err)
		}
		provider := newCredentialsUriCredentialsProvider(uriCredential)
		clientOptions = append(clientOptions, oss.SetCredentialsProvider(&provider))
	} else {
		provider, err := oss.NewEnvironmentVariableCredentialsProvider()
		if err != nil {
			return nil, fmt.Errorf("failed to new env credential: %v", err)
		}
		clientOptions = append(clientOptions, oss.SetCredentialsProvider(&provider))
	}

	client, err := oss.New(endpoint, "", "", clientOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OSS client with RAM credentials: %v", err)
	}

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket %s: %v", bucketName, err)
	}

	return &OSSClient{
		client:     client,
		bucket:     bucket,
		bucketName: bucketName,
	}, nil
}

// UploadFile uploads a file to OSS
func (o *OSSClient) UploadFile(ctx context.Context, filePath string, reader io.Reader) error {
	err := o.bucket.PutObject(filePath, reader)
	if err != nil {
		return fmt.Errorf("failed to upload file to OSS: %v", err)
	}
	return nil
}

// DownloadFile downloads a file from OSS
func (o *OSSClient) DownloadFile(ctx context.Context, filePath string, writer io.Writer) error {
	body, err := o.bucket.GetObject(filePath)
	if err != nil {
		return fmt.Errorf("failed to download file from OSS: %v", err)
	}
	defer body.Close()

	_, err = io.Copy(writer, body)
	if err != nil {
		return fmt.Errorf("failed to write downloaded file: %v", err)
	}
	return nil
}

// DeleteFile deletes a file from OSS
func (o *OSSClient) DeleteFile(ctx context.Context, filePath string) error {
	err := o.bucket.DeleteObject(filePath)
	if err != nil {
		return fmt.Errorf("failed to delete file from OSS: %v", err)
	}
	return nil
}

// DeleteFiles deletes multiple files from OSS, handling the 1000 objects limit per request.
// There is no NoSuchKey error for DeleteObjects
func (o *OSSClient) DeleteFiles(ctx context.Context, filePaths []string) error {
	if len(filePaths) == 0 {
		return nil
	}

	// OSS DeleteObjects has a limit of 1000 objects per request
	const maxObjectsPerRequest = 1000

	for i := 0; i < len(filePaths); {
		end := min(i+maxObjectsPerRequest, len(filePaths))

		batch := filePaths[i:end]
		resp, err := o.bucket.DeleteObjects(batch)
		if err != nil {
			return fmt.Errorf("failed to delete batch of files from OSS: %v", err)
		}
		i += len(resp.DeletedObjects)
	}

	return nil
}

// ListObjectsWithPrefix lists objects in OSS with a specific prefix
func (o *OSSClient) ListObjectsWithPrefix(ctx context.Context, prefix string) ([]string, error) {
	var allObjects []string

	// Use ListObjectsV2 to list all objects with the given prefix
	// Start with an empty continuation token
	marker := ""
	for {
		// List objects with the prefix
		result, err := o.bucket.ListObjectsV2(oss.Prefix(prefix), oss.Marker(marker), oss.MaxKeys(1000))
		if err != nil {
			return nil, fmt.Errorf("failed to list objects with prefix %s: %v", prefix, err)
		}

		// Add objects to our list
		for _, object := range result.Objects {
			allObjects = append(allObjects, object.Key)
		}

		// If there are no more objects, break
		if !result.IsTruncated {
			break
		}

		// Set the marker for the next iteration
		marker = result.NextContinuationToken
	}

	return allObjects, nil
}

// GetObjectContent returns the object body content from OSS
func (o *OSSClient) GetObjectContent(ctx context.Context, filePath string) ([]byte, error) {
	body, err := o.bucket.GetObject(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get object from OSS: %v", err)
	}
	defer body.Close()

	content, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object content: %v", err)
	}

	return content, nil
}

// ReadLines reads an object from OSS and splits it into lines
func (o *OSSClient) ReadLines(ctx context.Context, filePath string) ([][]byte, error) {
	content, err := o.GetObjectContent(ctx, filePath)
	if err != nil {
		return nil, err
	}

	// Split content by newlines
	lines := bytes.Split(content, []byte("\n"))

	// Remove the last empty line if the content ends with a newline
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}

	return lines, nil
}

// WriteLines writes lines to a file
func (o *OSSClient) WriteLines(ctx context.Context, filePath string, lines [][]byte) error {
	buf := append(bytes.Join(lines, []byte("\n")), '\n')
	err := o.bucket.PutObject(filePath, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("failed to upload file to OSS: %v", err)
	}
	return nil
}

// AppendToObject downloads an object from a source path and appends it to a destination path
func (o *OSSClient) AppendToObject(ctx context.Context, sourcePath, destinationPath string, appendPosition int64) (int64, error) {
	// Download the object from the source path
	body, err := o.bucket.GetObject(sourcePath)
	if err != nil {
		sErr, ok := err.(oss.ServiceError)
		if ok {
			// ignore non-exist object
			if sErr.Code == "NoSuchKey" {
				return appendPosition, nil
			}
		}
		return 0, fmt.Errorf("failed to download object from OSS: %v", err)
	}
	defer body.Close()

	// Upload the data to the destination path using AppendObject
	nextPosition, err := o.bucket.AppendObject(destinationPath, body, appendPosition)
	if err != nil {
		return 0, fmt.Errorf("failed to append object to OSS: %v", err)
	}

	return nextPosition, nil
}

var (
	_ oss.Credentials         = (*ossCredentials)(nil)
	_ oss.CredentialsProvider = ossCredentialsProvider{}
)

// ossCredentials implements the oss.Credentials interface
type ossCredentials struct {
	AccessKeyId     string
	AccessKeySecret string
	SecurityToken   string
}

func (credentials *ossCredentials) GetAccessKeyID() string {
	return credentials.AccessKeyId
}

func (credentials *ossCredentials) GetAccessKeySecret() string {
	return credentials.AccessKeySecret
}

func (credentials *ossCredentials) GetSecurityToken() string {
	return credentials.SecurityToken
}

// ossCredentialsProvider implements oss.CredentialsProvider interface
type ossCredentialsProvider struct {
	cred credentials.Credential
}

func (defBuild ossCredentialsProvider) GetCredentials() oss.Credentials {
	cred, _ := defBuild.cred.GetCredential()
	if cred == nil {
		return &ossCredentials{}
	}
	return &ossCredentials{
		AccessKeyId:     *cred.AccessKeyId,
		AccessKeySecret: *cred.AccessKeySecret,
		SecurityToken:   *cred.SecurityToken,
	}
}

func newCredentialsUriCredentialsProvider(credential credentials.Credential) ossCredentialsProvider {
	return ossCredentialsProvider{
		cred: credential,
	}
}
