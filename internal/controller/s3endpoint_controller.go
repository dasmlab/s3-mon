/*
Copyright 2026 dasmlab.
*/

package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	s3monv1alpha1 "github.com/dasmlab/s3-mon/api/v1alpha1"
	"github.com/dasmlab/s3-mon/internal/poller"
)

const finalizer = "s3mon.dasmlab.org/finalizer"

// S3EndpointReconciler reconciles an S3Endpoint object.
type S3EndpointReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Pollers *poller.Manager
}

// +kubebuilder:rbac:groups=s3mon.dasmlab.org,resources=s3endpoints,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=s3mon.dasmlab.org,resources=s3endpoints/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=s3mon.dasmlab.org,resources=s3endpoints/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *S3EndpointReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ep s3monv1alpha1.S3Endpoint
	if err := r.Get(ctx, req.NamespacedName, &ep); err != nil {
		if apierrors.IsNotFound(err) {
			r.Pollers.Stop(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !ep.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&ep, finalizer) {
			r.Pollers.Stop(types.NamespacedName{Namespace: ep.Namespace, Name: ep.Name})
			controllerutil.RemoveFinalizer(&ep, finalizer)
			if err := r.Update(ctx, &ep); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&ep, finalizer) {
		controllerutil.AddFinalizer(&ep, finalizer)
		if err := r.Update(ctx, &ep); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Touch secret to surface NotFound early in status via poller.
	var sec corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: ep.Namespace, Name: ep.Spec.CredentialsSecretRef.Name}, &sec); err != nil {
		logger.Error(err, "credentials secret not readable yet", "secret", ep.Spec.CredentialsSecretRef.Name)
	}

	r.Pollers.Ensure(&ep)
	logger.Info("ensured S3Endpoint poller", "name", ep.Name, "secret", ep.Spec.CredentialsSecretRef.Name)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *S3EndpointReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&s3monv1alpha1.S3Endpoint{}).
		Named("s3endpoint").
		Complete(r)
}
