package httpserver

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	s3monv1alpha1 "github.com/dasmlab/s3-mon/api/v1alpha1"
)

//go:embed templates/*
var templateFS embed.FS

const jsonKeyError = "error"

// Server exposes Prometheus metrics, health, JSON CRUD API, and a simple HTML UI.
type Server struct {
	Client client.Client
	Reg    *prometheus.Registry
}

// NewRouter builds the Gin engine used by the operator HTTP listener.
func (s *Server) NewRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})
	if s.Reg != nil {
		r.GET("/metrics", gin.WrapH(promhttp.HandlerFor(s.Reg, promhttp.HandlerOpts{})))
	}

	api := r.Group("/api/v1")
	{
		api.GET("/s3endpoints", s.listEndpoints)
		api.GET("/s3endpoints/:namespace/:name", s.getEndpoint)
		api.POST("/s3endpoints", s.createEndpoint)
		api.PUT("/s3endpoints/:namespace/:name", s.updateEndpoint)
		api.DELETE("/s3endpoints/:namespace/:name", s.deleteEndpoint)
	}

	tmpl := template.Must(template.New("").ParseFS(mustSub(templateFS, "templates"), "*.html"))
	r.SetHTMLTemplate(tmpl)
	r.GET("/", s.uiIndex)
	r.GET("/ui", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })

	return r
}

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

type endpointView struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	SecretName      string `json:"secretName"`
	Endpoint        string `json:"endpoint,omitempty"`
	Region          string `json:"region,omitempty"`
	Interval        string `json:"interval,omitempty"`
	FolderDepth     *int32 `json:"folderDepth,omitempty"`
	Suspend         bool   `json:"suspend"`
	Ready           bool   `json:"ready"`
	ObservedBuckets int32  `json:"observedBuckets"`
	Message         string `json:"message,omitempty"`
	LastSuccess     string `json:"lastSuccess,omitempty"`
	LastScrape      string `json:"lastScrape,omitempty"`
}

type createRequest struct {
	Name      string `json:"name" binding:"required"`
	Namespace string `json:"namespace" binding:"required"`
	Spec      struct {
		CredentialsSecretRef struct {
			Name string `json:"name" binding:"required"`
		} `json:"credentialsSecretRef" binding:"required"`
		Endpoint       string   `json:"endpoint"`
		Region         string   `json:"region"`
		Insecure       *bool    `json:"insecure"`
		ForcePathStyle *bool    `json:"forcePathStyle"`
		Interval       string   `json:"interval"`
		FolderDepth    *int32   `json:"folderDepth"`
		Buckets        []string `json:"buckets"`
		Suspend        bool     `json:"suspend"`
	} `json:"spec" binding:"required"`
}

func toView(ep *s3monv1alpha1.S3Endpoint) endpointView {
	v := endpointView{
		Name:            ep.Name,
		Namespace:       ep.Namespace,
		SecretName:      ep.Spec.CredentialsSecretRef.Name,
		Endpoint:        ep.Spec.Endpoint,
		Region:          ep.Spec.Region,
		FolderDepth:     ep.Spec.FolderDepth,
		Suspend:         ep.Spec.Suspend,
		Ready:           ep.Status.Ready,
		ObservedBuckets: ep.Status.ObservedBuckets,
		Message:         ep.Status.Message,
	}
	if ep.Spec.Interval != nil {
		v.Interval = ep.Spec.Interval.Duration.String()
	}
	if ep.Status.LastSuccessTime != nil {
		v.LastSuccess = ep.Status.LastSuccessTime.UTC().Format(time.RFC3339)
	}
	if ep.Status.LastScrapeTime != nil {
		v.LastScrape = ep.Status.LastScrapeTime.UTC().Format(time.RFC3339)
	}
	return v
}

func (s *Server) listEndpoints(c *gin.Context) {
	ns := strings.TrimSpace(c.Query("namespace"))
	list := &s3monv1alpha1.S3EndpointList{}
	opts := []client.ListOption{}
	if ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	if err := s.Client.List(c.Request.Context(), list, opts...); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{jsonKeyError: err.Error()})
		return
	}
	out := make([]endpointView, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, toView(&list.Items[i]))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func (s *Server) getEndpoint(c *gin.Context) {
	ep, err := s.fetch(c.Request.Context(), c.Param("namespace"), c.Param("name"))
	if err != nil {
		writeK8sErr(c, err)
		return
	}
	c.JSON(http.StatusOK, toView(ep))
}

func (s *Server) createEndpoint(c *gin.Context) {
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{jsonKeyError: err.Error()})
		return
	}
	ep := &s3monv1alpha1.S3Endpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
		Spec: s3monv1alpha1.S3EndpointSpec{
			CredentialsSecretRef: s3monv1alpha1.SecretReference{Name: req.Spec.CredentialsSecretRef.Name},
			Endpoint:             req.Spec.Endpoint,
			Region:               req.Spec.Region,
			Insecure:             req.Spec.Insecure,
			ForcePathStyle:       req.Spec.ForcePathStyle,
			FolderDepth:          req.Spec.FolderDepth,
			Buckets:              req.Spec.Buckets,
			Suspend:              req.Spec.Suspend,
		},
	}
	if req.Spec.Interval != "" {
		d, err := time.ParseDuration(req.Spec.Interval)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{jsonKeyError: fmt.Sprintf("invalid interval: %v", err)})
			return
		}
		ep.Spec.Interval = &metav1.Duration{Duration: d}
	}
	if err := s.Client.Create(c.Request.Context(), ep); err != nil {
		writeK8sErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, toView(ep))
}

func (s *Server) updateEndpoint(c *gin.Context) {
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{jsonKeyError: err.Error()})
		return
	}
	ns, name := c.Param("namespace"), c.Param("name")
	ep, err := s.fetch(c.Request.Context(), ns, name)
	if err != nil {
		writeK8sErr(c, err)
		return
	}
	ep.Spec.CredentialsSecretRef.Name = req.Spec.CredentialsSecretRef.Name
	ep.Spec.Endpoint = req.Spec.Endpoint
	ep.Spec.Region = req.Spec.Region
	ep.Spec.Insecure = req.Spec.Insecure
	ep.Spec.ForcePathStyle = req.Spec.ForcePathStyle
	ep.Spec.FolderDepth = req.Spec.FolderDepth
	ep.Spec.Buckets = req.Spec.Buckets
	ep.Spec.Suspend = req.Spec.Suspend
	if req.Spec.Interval != "" {
		d, err := time.ParseDuration(req.Spec.Interval)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{jsonKeyError: fmt.Sprintf("invalid interval: %v", err)})
			return
		}
		ep.Spec.Interval = &metav1.Duration{Duration: d}
	} else {
		ep.Spec.Interval = nil
	}
	if err := s.Client.Update(c.Request.Context(), ep); err != nil {
		writeK8sErr(c, err)
		return
	}
	c.JSON(http.StatusOK, toView(ep))
}

func (s *Server) deleteEndpoint(c *gin.Context) {
	ep, err := s.fetch(c.Request.Context(), c.Param("namespace"), c.Param("name"))
	if err != nil {
		writeK8sErr(c, err)
		return
	}
	if err := s.Client.Delete(c.Request.Context(), ep); err != nil {
		writeK8sErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) fetch(ctx context.Context, ns, name string) (*s3monv1alpha1.S3Endpoint, error) {
	ep := &s3monv1alpha1.S3Endpoint{}
	err := s.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, ep)
	return ep, err
}

func writeK8sErr(c *gin.Context, err error) {
	switch {
	case apierrors.IsNotFound(err):
		c.JSON(http.StatusNotFound, gin.H{jsonKeyError: err.Error()})
	case apierrors.IsAlreadyExists(err):
		c.JSON(http.StatusConflict, gin.H{jsonKeyError: err.Error()})
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		c.JSON(http.StatusBadRequest, gin.H{jsonKeyError: err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{jsonKeyError: err.Error()})
	}
}

func (s *Server) uiIndex(c *gin.Context) {
	ns := strings.TrimSpace(c.Query("namespace"))
	list := &s3monv1alpha1.S3EndpointList{}
	opts := []client.ListOption{}
	if ns != "" {
		opts = append(opts, client.InNamespace(ns))
	}
	_ = s.Client.List(c.Request.Context(), list, opts...)
	rows := make([]endpointView, 0, len(list.Items))
	for i := range list.Items {
		rows = append(rows, toView(&list.Items[i]))
	}
	c.HTML(http.StatusOK, "index.html", gin.H{
		"Title":     "s3-mon",
		"Namespace": ns,
		"Items":     rows,
		"Count":     len(rows),
		"DepthHint": strconv.Itoa(3),
	})
}
