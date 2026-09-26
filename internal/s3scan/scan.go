package s3scan

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/dasmlab/s3-mon/internal/s3creds"
)

// PrefixStat is usage for one common-prefix ("folder").
type PrefixStat struct {
	Prefix   string
	Depth    int
	Bytes    int64
	Objects  int64
	Children int // immediate child folder count under this prefix
}

// BucketStat is aggregate usage for one bucket.
type BucketStat struct {
	Name         string
	Bytes        int64
	Objects      int64
	FolderCounts map[int]int // depth -> number of folders at that depth
	Prefixes     []PrefixStat
}

// Result is a full scrape.
type Result struct {
	Buckets []BucketStat
}

// Client wraps aws-sdk-go-v2 S3.
type Client struct {
	s3   *s3.Client
	logf func(string, ...any)
}

// New builds an S3 client from Creds.
func New(c s3creds.Creds) *Client {
	scheme := "https"
	if c.Insecure {
		scheme = "http"
	}
	base := fmt.Sprintf("%s://%s", scheme, c.Endpoint)

	cfg := aws.Config{
		Region: c.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			c.AccessKey, c.SecretKey, "",
		),
	}
	cli := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(base)
		o.UsePathStyle = c.ForcePathStyle || c.Insecure
	})
	return &Client{
		s3: cli,
		logf: func(f string, a ...any) {
			log.Printf("s3scan: "+f, a...)
		},
	}
}

// Scan lists buckets (or uses allowlist) and gathers size / folder stats.
func (c *Client) Scan(ctx context.Context, allow []string, folderDepth int) (Result, error) {
	if folderDepth < 1 {
		folderDepth = 1
	}
	names := allow
	if len(names) == 0 {
		out, err := c.s3.ListBuckets(ctx, &s3.ListBucketsInput{})
		if err != nil {
			return Result{}, fmt.Errorf("ListBuckets: %w", err)
		}
		for _, b := range out.Buckets {
			names = append(names, aws.ToString(b.Name))
		}
		c.logf("ListBuckets -> %d buckets", len(names))
	}

	var res Result
	for _, name := range names {
		c.logf("scanning bucket=%s depth=%d", name, folderDepth)
		st, err := c.scanBucket(ctx, name, folderDepth)
		if err != nil {
			return Result{}, fmt.Errorf("bucket %s: %w", name, err)
		}
		res.Buckets = append(res.Buckets, st)
	}
	return res, nil
}

func (c *Client) scanBucket(ctx context.Context, bucket string, maxDepth int) (BucketStat, error) {
	st := BucketStat{
		Name:         bucket,
		FolderCounts: map[int]int{},
	}

	// Full object walk for totals.
	var token *string
	for {
		out, err := c.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			ContinuationToken: token,
		})
		if err != nil {
			return st, err
		}
		for _, obj := range out.Contents {
			st.Objects++
			st.Bytes += aws.ToInt64(obj.Size)
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}

	// Prefix / folder walk.
	type node struct {
		prefix string
		depth  int
	}
	queue := []node{{prefix: "", depth: 0}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.depth >= maxDepth {
			continue
		}
		children, err := c.listCommonPrefixes(ctx, bucket, n.prefix)
		if err != nil {
			return st, err
		}
		nextDepth := n.depth + 1
		st.FolderCounts[nextDepth] += len(children)
		for _, child := range children {
			bytes, objs, err := c.sumPrefix(ctx, bucket, child)
			if err != nil {
				return st, err
			}
			st.Prefixes = append(st.Prefixes, PrefixStat{
				Prefix:  child,
				Depth:   nextDepth,
				Bytes:   bytes,
				Objects: objs,
			})
			queue = append(queue, node{prefix: child, depth: nextDepth})
		}
	}
	return st, nil
}

func (c *Client) listCommonPrefixes(ctx context.Context, bucket, prefix string) ([]string, error) {
	var (
		token    *string
		prefixes []string
	)
	for {
		out, err := c.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, p := range out.CommonPrefixes {
			cp := aws.ToString(p.Prefix)
			if cp != "" {
				prefixes = append(prefixes, cp)
			}
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}
	return prefixes, nil
}

func (c *Client) sumPrefix(ctx context.Context, bucket, prefix string) (int64, int64, error) {
	var (
		token   *string
		bytes   int64
		objects int64
	)
	for {
		out, err := c.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return 0, 0, err
		}
		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			// Skip the "folder marker" zero-byte key equal to prefix if present.
			if key == prefix || strings.HasSuffix(key, "/") && aws.ToInt64(obj.Size) == 0 {
				continue
			}
			objects++
			bytes += aws.ToInt64(obj.Size)
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}
	return bytes, objects, nil
}
