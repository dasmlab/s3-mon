package poller

import (
	"context"
	"fmt"
	"sync"
	"time"

	s3monv1alpha1 "github.com/dasmlab/s3-mon/api/v1alpha1"
	"github.com/dasmlab/s3-mon/internal/metrics"
	"github.com/dasmlab/s3-mon/internal/s3creds"
	"github.com/dasmlab/s3-mon/internal/s3scan"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Manager runs per-CR scrape loops.
type Manager struct {
	client  client.Client
	metrics *metrics.Collector
	log     logr.Logger

	mu    sync.Mutex
	loops map[types.NamespacedName]*loop
}

type loop struct {
	cancel     context.CancelFunc
	generation int64
}

// NewManager creates a poller manager.
func NewManager(c client.Client, m *metrics.Collector, log logr.Logger) *Manager {
	return &Manager{
		client:  c,
		metrics: m,
		log:     log.WithName("poller"),
		loops:   map[types.NamespacedName]*loop{},
	}
}

// Ensure starts or restarts the scrape loop for ep when generation changes.
func (m *Manager) Ensure(ep *s3monv1alpha1.S3Endpoint) {
	key := types.NamespacedName{Namespace: ep.Namespace, Name: ep.Name}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.loops[key]; ok {
		if existing.generation == ep.Generation && !ep.Spec.Suspend {
			return
		}
		existing.cancel()
		delete(m.loops, key)
	}
	if ep.Spec.Suspend {
		m.log.Info("suspended", "endpoint", key.String())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.loops[key] = &loop{cancel: cancel, generation: ep.Generation}

	interval := 5 * time.Minute
	if ep.Spec.Interval != nil && ep.Spec.Interval.Duration > 0 {
		interval = ep.Spec.Interval.Duration
	}
	go m.run(ctx, key, interval)
}

// Stop cancels the loop and clears metrics.
func (m *Manager) Stop(key types.NamespacedName) {
	m.mu.Lock()
	if existing, ok := m.loops[key]; ok {
		existing.cancel()
		delete(m.loops, key)
	}
	m.mu.Unlock()
	m.metrics.ClearEndpoint(key.Namespace, key.Name)
}

func (m *Manager) run(ctx context.Context, key types.NamespacedName, interval time.Duration) {
	m.scrapeOnce(ctx, key)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.log.Info("poller stopped", "endpoint", key.String())
			return
		case <-t.C:
			m.scrapeOnce(ctx, key)
		}
	}
}

func (m *Manager) scrapeOnce(ctx context.Context, key types.NamespacedName) {
	log := m.log.WithValues("endpoint", key.String())
	start := time.Now()

	var ep s3monv1alpha1.S3Endpoint
	if err := m.client.Get(ctx, key, &ep); err != nil {
		log.Error(err, "get S3Endpoint")
		return
	}
	if ep.Spec.Suspend {
		log.Info("skip scrape: suspended")
		return
	}

	var sec corev1.Secret
	secKey := types.NamespacedName{Namespace: ep.Namespace, Name: ep.Spec.CredentialsSecretRef.Name}
	if err := m.client.Get(ctx, secKey, &sec); err != nil {
		m.fail(ctx, &ep, start, fmt.Errorf("get secret: %w", err))
		return
	}
	creds, err := s3creds.FromSecret(&sec)
	if err != nil {
		m.fail(ctx, &ep, start, err)
		return
	}
	if ep.Spec.Endpoint != "" {
		creds.Endpoint = ep.Spec.Endpoint
	}
	if ep.Spec.Region != "" {
		creds.Region = ep.Spec.Region
	}
	if ep.Spec.Insecure != nil {
		creds.Insecure = *ep.Spec.Insecure
	}
	if ep.Spec.ForcePathStyle != nil {
		creds.ForcePathStyle = *ep.Spec.ForcePathStyle
	}

	depth := 3
	if ep.Spec.FolderDepth != nil {
		depth = int(*ep.Spec.FolderDepth)
	}

	log.Info("polling S3 endpoint",
		"s3_endpoint", creds.Endpoint,
		"region", creds.Region,
		"insecure", creds.Insecure,
		"forcePathStyle", creds.ForcePathStyle,
		"folderDepth", depth,
		"bucketAllowlist", ep.Spec.Buckets,
	)

	timeout := 4 * time.Minute
	if ep.Spec.Interval != nil && ep.Spec.Interval.Duration > 30*time.Second {
		timeout = ep.Spec.Interval.Duration - time.Second
	}
	scanCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cli := s3scan.New(creds)
	res, err := cli.Scan(scanCtx, ep.Spec.Buckets, depth)
	dur := time.Since(start).Seconds()
	ts := float64(time.Now().Unix())
	if err != nil {
		m.metrics.SetScrapeMeta(ep.Namespace, ep.Name, false, dur, ts)
		m.fail(ctx, &ep, start, err)
		return
	}

	m.metrics.ApplyResult(ep.Namespace, ep.Name, res)
	m.metrics.SetScrapeMeta(ep.Namespace, ep.Name, true, dur, ts)

	totalBytes := int64(0)
	for _, b := range res.Buckets {
		totalBytes += b.Bytes
		log.Info("bucket stats",
			"bucket", b.Name,
			"bytes", b.Bytes,
			"objects", b.Objects,
			"folders", b.FolderCounts,
		)
	}

	now := metav1.Now()
	ep.Status.Ready = true
	ep.Status.ObservedBuckets = int32(len(res.Buckets))
	ep.Status.LastScrapeTime = &now
	ep.Status.LastSuccessTime = &now
	ep.Status.Message = fmt.Sprintf("ok: %d buckets, %d bytes total (%.1fs)", len(res.Buckets), totalBytes, dur)
	setCond(&ep, "Ready", metav1.ConditionTrue, "ScrapeSucceeded", ep.Status.Message)
	if err := m.patchStatus(ctx, key, func(cur *s3monv1alpha1.S3Endpoint) {
		cur.Status = ep.Status
	}); err != nil {
		log.Error(err, "update status")
	}
	log.Info("scrape succeeded", "buckets", len(res.Buckets), "bytes", totalBytes, "duration", dur)
}

func (m *Manager) fail(ctx context.Context, ep *s3monv1alpha1.S3Endpoint, start time.Time, err error) {
	m.log.Error(err, "scrape failed", "endpoint", ep.Namespace+"/"+ep.Name)
	now := metav1.Now()
	dur := time.Since(start).Seconds()
	m.metrics.SetScrapeMeta(ep.Namespace, ep.Name, false, dur, float64(now.Unix()))
	key := types.NamespacedName{Namespace: ep.Namespace, Name: ep.Name}
	_ = m.patchStatus(ctx, key, func(cur *s3monv1alpha1.S3Endpoint) {
		cur.Status.LastScrapeTime = &now
		cur.Status.Message = err.Error()
		cur.Status.Ready = false
		setCond(cur, "Ready", metav1.ConditionFalse, "ScrapeFailed", err.Error())
	})
}

func (m *Manager) patchStatus(ctx context.Context, key types.NamespacedName, mutate func(*s3monv1alpha1.S3Endpoint)) error {
	var latest s3monv1alpha1.S3Endpoint
	if err := m.client.Get(ctx, key, &latest); err != nil {
		return err
	}
	mutate(&latest)
	return m.client.Status().Update(ctx, &latest)
}

func setCond(ep *s3monv1alpha1.S3Endpoint, typ string, status metav1.ConditionStatus, reason, msg string) {
	now := metav1.Now()
	for i := range ep.Status.Conditions {
		if ep.Status.Conditions[i].Type == typ {
			ep.Status.Conditions[i].Status = status
			ep.Status.Conditions[i].Reason = reason
			ep.Status.Conditions[i].Message = msg
			ep.Status.Conditions[i].LastTransitionTime = now
			ep.Status.Conditions[i].ObservedGeneration = ep.Generation
			return
		}
	}
	ep.Status.Conditions = append(ep.Status.Conditions, metav1.Condition{
		Type:               typ,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		LastTransitionTime: now,
		ObservedGeneration: ep.Generation,
	})
}
