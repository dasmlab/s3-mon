package metrics

import (
	"strconv"
	"sync"

	"github.com/dasmlab/s3-mon/internal/s3scan"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	ns = "s3mon"

	labelEndpointNS = "endpoint_namespace"
	labelEndpoint   = "endpoint"
	labelBucket     = "bucket"
	labelPrefix     = "prefix"
	labelDepth      = "depth"
)

// Collector holds Prometheus metrics for all S3Endpoints.
type Collector struct {
	mu sync.Mutex

	bucketSize    *prometheus.GaugeVec
	bucketObjects *prometheus.GaugeVec
	folderCount   *prometheus.GaugeVec
	prefixSize    *prometheus.GaugeVec
	prefixObjects *prometheus.GaugeVec
	scrapeOK      *prometheus.GaugeVec
	scrapeSeconds *prometheus.GaugeVec
	lastScrape    *prometheus.GaugeVec
}

// New registers metrics on reg.
func New(reg prometheus.Registerer) *Collector {
	epLabels := []string{labelEndpointNS, labelEndpoint}
	bucketLabels := []string{labelEndpointNS, labelEndpoint, labelBucket}
	folderLabels := []string{labelEndpointNS, labelEndpoint, labelBucket, labelDepth}
	prefixLabels := []string{labelEndpointNS, labelEndpoint, labelBucket, labelPrefix, labelDepth}
	c := &Collector{
		bucketSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "bucket_size_bytes",
			Help: "Total size in bytes of an S3 bucket",
		}, bucketLabels),
		bucketObjects: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "bucket_objects",
			Help: "Number of objects in an S3 bucket",
		}, bucketLabels),
		folderCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "folder_count",
			Help: "Number of common-prefix folders at a given depth",
		}, folderLabels),
		prefixSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "prefix_size_bytes",
			Help: "Total size in bytes under a prefix (folder)",
		}, prefixLabels),
		prefixObjects: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "prefix_objects",
			Help: "Number of objects under a prefix (folder)",
		}, prefixLabels),
		scrapeOK: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "scrape_success",
			Help: "1 if last scrape succeeded, else 0",
		}, epLabels),
		scrapeSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "scrape_duration_seconds",
			Help: "Duration of the last scrape",
		}, epLabels),
		lastScrape: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "last_scrape_timestamp",
			Help: "Unix timestamp of the last scrape attempt",
		}, epLabels),
	}
	reg.MustRegister(
		c.bucketSize, c.bucketObjects, c.folderCount,
		c.prefixSize, c.prefixObjects,
		c.scrapeOK, c.scrapeSeconds, c.lastScrape,
	)
	return c
}

// ClearEndpoint drops series for one CR before rewriting.
func (c *Collector) ClearEndpoint(namespace, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	match := prometheus.Labels{labelEndpointNS: namespace, labelEndpoint: name}
	c.bucketSize.DeletePartialMatch(match)
	c.bucketObjects.DeletePartialMatch(match)
	c.folderCount.DeletePartialMatch(match)
	c.prefixSize.DeletePartialMatch(match)
	c.prefixObjects.DeletePartialMatch(match)
}

// SetScrapeMeta updates scrape health gauges.
func (c *Collector) SetScrapeMeta(namespace, name string, ok bool, seconds float64, ts float64) {
	v := 0.0
	if ok {
		v = 1
	}
	c.scrapeOK.WithLabelValues(namespace, name).Set(v)
	c.scrapeSeconds.WithLabelValues(namespace, name).Set(seconds)
	c.lastScrape.WithLabelValues(namespace, name).Set(ts)
}

// ApplyResult replaces per-bucket/prefix gauges for an endpoint.
func (c *Collector) ApplyResult(namespace, name string, res s3scan.Result) {
	c.ClearEndpoint(namespace, name)
	for _, b := range res.Buckets {
		c.bucketSize.WithLabelValues(namespace, name, b.Name).Set(float64(b.Bytes))
		c.bucketObjects.WithLabelValues(namespace, name, b.Name).Set(float64(b.Objects))
		for depth, count := range b.FolderCounts {
			c.folderCount.WithLabelValues(namespace, name, b.Name, itoa(depth)).Set(float64(count))
		}
		for _, p := range b.Prefixes {
			c.prefixSize.WithLabelValues(namespace, name, b.Name, p.Prefix, itoa(p.Depth)).Set(float64(p.Bytes))
			c.prefixObjects.WithLabelValues(namespace, name, b.Name, p.Prefix, itoa(p.Depth)).Set(float64(p.Objects))
		}
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
